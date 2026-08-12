package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/shdadahui/library-agent/internal/store"
)

// 座位时段定义。
type SlotDef struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Start string `json:"start"` // HH:MM
	End   string `json:"end"`   // HH:MM
}

// SeatSlots 全馆统一时段。
var SeatSlots = []SlotDef{
	{Key: "morning", Label: "上午 08:00-12:00", Start: "08:00", End: "12:00"},
	{Key: "afternoon", Label: "下午 13:00-17:00", Start: "13:00", End: "17:00"},
	{Key: "evening", Label: "晚上 18:00-22:00", Start: "18:00", End: "22:00"},
}

// 座位业务错误。
var (
	ErrSeatSlotInvalid    = errors.New("无效的预约时段")
	ErrSeatDatePast       = errors.New("不能预约过去的日期")
	ErrSeatAlreadyReserved = errors.New("该座位在该时段已被预约")
	ErrSeatQuotaReached   = errors.New("同一读者一天最多预约 1 个座位，请先取消已有预约")
	ErrSeatReservationClosed = errors.New("该预约已关闭，无法操作")
	ErrSeatReservationNotMine = errors.New("只能操作自己的座位预约")
	ErrSeatCheckinTime    = errors.New("只能在预约时段内签到")
	ErrSeatCheckinDate    = errors.New("只能在预约当天签到")
)

// SeatArea 区域与座位数（前端展示）。
type SeatArea struct {
	Area  string `json:"area"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// SeatView 座位视图（含当前占用状态）。
type SeatView struct {
	store.Seat
	Occupied bool `json:"occupied"` // 当前实时占用（seat.status == occupied）
}

// SeatAreas 区域统计。
func (s *Service) SeatAreas() ([]SeatArea, error) {
	seats, err := s.st.ListSeats("", "")
	if err != nil {
		return nil, err
	}
	m := map[string]int{}
	for _, se := range seats {
		m[se.Area]++
	}
	labels := map[string]string{
		"3F 阅览区": "三楼·开架阅览区",
		"2F 自习区": "二楼·自习/考研区",
		"1F 研讨间": "一楼·小组研讨间",
	}
	out := []SeatArea{}
	for area, n := range m {
		label := labels[area]
		if label == "" {
			label = area
		}
		out = append(out, SeatArea{Area: area, Label: label, Count: n})
	}
	return out, nil
}

// SeatByID 按 ID 取单个座位。
func (s *Service) SeatByID(id int64) (*store.Seat, error) {
	return s.st.GetSeat(id)
}

// Seats 座位列表。
func (s *Service) Seats(area, seatType string) ([]SeatView, error) {
	seats, err := s.st.ListSeats(area, seatType)
	if err != nil {
		return nil, err
	}
	out := make([]SeatView, 0, len(seats))
	for _, se := range seats {
		out = append(out, SeatView{Seat: se, Occupied: se.Status == "occupied"})
	}
	return out, nil
}

// AvailableSeats 指定日期时段的可预约座位。
func (s *Service) AvailableSeats(date, slot string) ([]SeatView, error) {
	if !validSlot(slot) {
		return nil, ErrSeatSlotInvalid
	}
	seats, err := s.st.AvailableSeats(date, slot)
	if err != nil {
		return nil, err
	}
	out := make([]SeatView, 0, len(seats))
	for _, se := range seats {
		out = append(out, SeatView{Seat: se, Occupied: se.Status == "occupied"})
	}
	return out, nil
}

// ReserveSeat 预约座位。
func (s *Service) ReserveSeat(patronID, seatID int64, date, slot string) (*store.SeatReservation, error) {
	if !validSlot(slot) {
		return nil, ErrSeatSlotInvalid
	}
	if date < store.Now() {
		return nil, ErrSeatDatePast
	}
	if _, err := s.st.GetSeat(seatID); err != nil {
		return nil, err
	}
	// 同读者同日已有预约
	existing, err := s.st.PatronSeatReservations(patronID, true)
	if err != nil {
		return nil, err
	}
	for _, r := range existing {
		if r.ReserveDate == date {
			return nil, ErrSeatQuotaReached
		}
	}
	// 同座位同时段冲突
	conflict, err := s.st.SeatReservationConflict(seatID, date, slot)
	if err != nil {
		return nil, err
	}
	if conflict {
		return nil, ErrSeatAlreadyReserved
	}
	r := &store.SeatReservation{
		SeatID: seatID, PatronID: patronID, ReserveDate: date, Slot: slot,
		Status: "active", CreatedAt: store.NowDateTime(),
	}
	id, err := s.st.CreateSeatReservation(r)
	if err != nil {
		return nil, err
	}
	return s.st.GetSeatReservation(id)
}

// CancelSeatReservation 取消预约。
func (s *Service) CancelSeatReservation(patronID, resID int64) error {
	r, err := s.st.GetSeatReservation(resID)
	if err != nil {
		return err
	}
	if r.PatronID != patronID {
		return ErrSeatReservationNotMine
	}
	if r.Status != "active" {
		return ErrSeatReservationClosed
	}
	ok, err := s.st.UpdateSeatReservationStatus(resID, "active", "cancelled")
	if err != nil {
		return err
	}
	if !ok {
		return ErrSeatReservationClosed
	}
	return nil
}

// CheckinSeat 签到占座（仅预约当天 + 当前时段）。
func (s *Service) CheckinSeat(patronID, resID int64) (*store.SeatReservation, error) {
	r, err := s.st.GetSeatReservation(resID)
	if err != nil {
		return nil, err
	}
	if r.PatronID != patronID {
		return nil, ErrSeatReservationNotMine
	}
	if r.Status != "active" {
		return nil, ErrSeatReservationClosed
	}
	if r.ReserveDate != store.Now() {
		return nil, ErrSeatCheckinDate
	}
	if !slotNow(r.Slot) {
		return nil, fmt.Errorf("%s（预约时段 %s 未到或已过）", ErrSeatCheckinTime, slotLabel(r.Slot))
	}
	ok, err := s.st.UpdateSeatReservationStatus(resID, "active", "checked_in")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrSeatReservationClosed
	}
	// 占用座位（实时状态）
	_ = s.st.UpdateSeatStatus(r.SeatID, "occupied")
	return s.st.GetSeatReservation(resID)
}

// SeatReservationView 预约视图（含座位信息）。
type SeatReservationView struct {
	store.SeatReservation
	SeatNo   string `json:"seat_no"`
	Area     string `json:"area"`
	SeatType string `json:"seat_type"`
	SlotLabel string `json:"slot_label"`
}

// MySeatReservations 我的预约（含座位信息）。
func (s *Service) MySeatReservations(patronID int64, activeOnly bool) ([]SeatReservationView, error) {
	rs, err := s.st.PatronSeatReservations(patronID, activeOnly)
	if err != nil {
		return nil, err
	}
	out := make([]SeatReservationView, 0, len(rs))
	for _, r := range rs {
		v := SeatReservationView{SeatReservation: r, SlotLabel: slotLabel(r.Slot)}
		if se, err := s.st.GetSeat(r.SeatID); err == nil {
			v.SeatNo, v.Area, v.SeatType = se.SeatNo, se.Area, se.SeatType
		}
		out = append(out, v)
	}
	return out, nil
}

// ---- 时段工具 ----

func validSlot(slot string) bool {
	for _, s := range SeatSlots {
		if s.Key == slot {
			return true
		}
	}
	return false
}

func slotLabel(slot string) string {
	for _, s := range SeatSlots {
		if s.Key == slot {
			return s.Label
		}
	}
	return slot
}

// slotNow 当前时间是否处于该时段内。
func slotNow(slot string) bool {
	now := time.Now()
	cur := now.Format("15:04")
	for _, s := range SeatSlots {
		if s.Key == slot {
			return cur >= s.Start && cur < s.End
		}
	}
	return false
}
