package api

import (
	"net/http"

	"github.com/shdadahui/library-agent/internal/service"
	"github.com/shdadahui/library-agent/internal/store"
)

// GET /api/seats?area=&type= —— 座位列表
func (s *Server) handleSeats(w http.ResponseWriter, r *http.Request) {
	seats, err := s.Svc.Seats(r.URL.Query().Get("area"), r.URL.Query().Get("type"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, seats)
}

// GET /api/seats/areas —— 区域统计 + 时段定义
func (s *Server) handleSeatMeta(w http.ResponseWriter, _ *http.Request) {
	areas, err := s.Svc.SeatAreas()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"areas": areas, "slots": service.SeatSlots})
}

// GET /api/seats/available?date=&slot= —— 可预约座位
func (s *Server) handleAvailableSeats(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	slot := r.URL.Query().Get("slot")
	if date == "" {
		date = store.Now()
	}
	seats, err := s.Svc.AvailableSeats(date, slot)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, seats)
}

// POST /api/seats/reserve —— 预约座位（当前登录用户）
func (s *Server) handleReserveSeat(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "请先登录")
		return
	}
	var body struct {
		SeatID int64  `json:"seat_id"`
		Date   string `json:"date"`
		Slot   string `json:"slot"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Date == "" {
		body.Date = store.Now()
	}
	res, err := s.Svc.ReserveSeat(u.PatronID, body.SeatID, body.Date, body.Slot)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

// POST /api/seat-reservations/{id}/cancel —— 取消预约
func (s *Server) handleCancelSeatReservation(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "请先登录")
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Svc.CancelSeatReservation(u.PatronID, id); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "已取消预约"})
}

// POST /api/seat-reservations/{id}/checkin —— 签到占座
func (s *Server) handleCheckinSeat(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "请先登录")
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.Svc.CheckinSeat(u.PatronID, id)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// GET /api/me/seat-reservations?active=1 —— 我的预约
func (s *Server) handleMySeatReservations(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "请先登录")
		return
	}
	active := r.URL.Query().Get("active") == "1"
	rs, err := s.Svc.MySeatReservations(u.PatronID, active)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rs)
}
