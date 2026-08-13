package store

import (
	"database/sql"
	"errors"
	"time"
)

// Seat 座位（区域 + 排/列网格布局）。
type Seat struct {
	ID       int64  `json:"id"`
	SeatNo   string `json:"seat_no"`
	Area     string `json:"area"`
	SeatType string `json:"seat_type"` // 普通 / 带插座 / 研讨间 / 窗边
	Status   string `json:"status"`    // available / occupied（当前实时占用）
	RowPos   int    `json:"row"`
	ColPos   int    `json:"col"`
}

// SeatReservation 座位预约记录。
type SeatReservation struct {
	ID          int64  `json:"id"`
	SeatID      int64  `json:"seat_id"`
	PatronID    int64  `json:"patron_id"`
	ReserveDate string `json:"reserve_date"` // YYYY-MM-DD
	Slot        string `json:"slot"`         // morning / afternoon / evening
	Status      string `json:"status"`       // active / cancelled / checked_in / expired
	CreatedAt   string `json:"created_at"`
}

// 座位相关错误。
var (
	ErrSeatNotFound       = errors.New("座位不存在")
	ErrReservationNotFound = errors.New("预约记录不存在")
)

// ListSeats 座位列表（可按区域/类型过滤）。
func (s *Store) ListSeats(area, seatType string) ([]Seat, error) {
	q := `SELECT id,seat_no,area,seat_type,status,row_pos,col_pos FROM seats WHERE 1=1`
	var args []any
	if area != "" {
		q += ` AND area=?`
		args = append(args, area)
	}
	if seatType != "" {
		q += ` AND seat_type=?`
		args = append(args, seatType)
	}
	q += ` ORDER BY area, row_pos, col_pos`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Seat{}
	for rows.Next() {
		var st Seat
		if err := rows.Scan(&st.ID, &st.SeatNo, &st.Area, &st.SeatType, &st.Status, &st.RowPos, &st.ColPos); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// GetSeat 按 ID 取座位。
func (s *Store) GetSeat(id int64) (*Seat, error) {
	row := s.DB.QueryRow(`SELECT id,seat_no,area,seat_type,status,row_pos,col_pos FROM seats WHERE id=?`, id)
	var st Seat
	if err := row.Scan(&st.ID, &st.SeatNo, &st.Area, &st.SeatType, &st.Status, &st.RowPos, &st.ColPos); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrSeatNotFound
		}
		return nil, err
	}
	return &st, nil
}

// InsertSeat 插入座位（按 seat_no 幂等，已存在返回其 ID）。
func (s *Store) InsertSeat(se *Seat) (int64, error) {
	var id int64
	err := s.DB.QueryRow(`SELECT id FROM seats WHERE seat_no=?`, se.SeatNo).Scan(&id)
	if err == nil {
		return id, nil
	}
	res, err := s.DB.Exec(`INSERT INTO seats(seat_no,area,seat_type,status,row_pos,col_pos) VALUES(?,?,?,?,?,?)`,
		se.SeatNo, se.Area, se.SeatType, se.Status, se.RowPos, se.ColPos)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AvailableSeats 指定日期时段未被预约的座位。
func (s *Store) AvailableSeats(date, slot string) ([]Seat, error) {
	q := `SELECT id,seat_no,area,seat_type,status,row_pos,col_pos FROM seats
	      WHERE id NOT IN (
	        SELECT seat_id FROM seat_reservations
	        WHERE reserve_date=? AND slot=? AND status IN ('active','checked_in')
	      ) ORDER BY area, row_pos, col_pos`
	rows, err := s.DB.Query(q, date, slot)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Seat{}
	for rows.Next() {
		var st Seat
		if err := rows.Scan(&st.ID, &st.SeatNo, &st.Area, &st.SeatType, &st.Status, &st.RowPos, &st.ColPos); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// 座位冲突错误（原子插入时返回）。
var (
	ErrSeatReservedConflict = errors.New("该座位在该时段已被预约")
	ErrSeatQuotaConflict    = errors.New("同一读者一天最多预约 1 个座位")
)

// CreateSeatReservation 原子预约：单条 INSERT..WHERE NOT EXISTS 同时校验
// "同座位同时段冲突"与"同读者同日已有预约"，杜绝并发双预约
// （SQLite 单连接串行写 / MySQL 语句级原子，跨驱动安全）。
func (s *Store) CreateSeatReservation(r *SeatReservation) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO seat_reservations(seat_id,patron_id,reserve_date,slot,status,created_at)
		SELECT ?,?,?,?,?,?
		WHERE NOT EXISTS (
			SELECT 1 FROM seat_reservations
			WHERE seat_id=? AND reserve_date=? AND slot=? AND status IN ('active','checked_in')
		)
		AND NOT EXISTS (
			SELECT 1 FROM seat_reservations
			WHERE patron_id=? AND reserve_date=? AND status IN ('active','checked_in')
		)`,
		r.SeatID, r.PatronID, r.ReserveDate, r.Slot, r.Status, r.CreatedAt,
		r.SeatID, r.ReserveDate, r.Slot,
		r.PatronID, r.ReserveDate)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		// 区分冲突类型（仅用于报错文案；判断本身已是原子的）
		conflict, _ := s.SeatReservationConflict(r.SeatID, r.ReserveDate, r.Slot)
		if conflict {
			return 0, ErrSeatReservedConflict
		}
		return 0, ErrSeatQuotaConflict
	}
	return res.LastInsertId()
}

// GetSeatReservation 按 ID 取预约。
func (s *Store) GetSeatReservation(id int64) (*SeatReservation, error) {
	row := s.DB.QueryRow(`SELECT id,seat_id,patron_id,reserve_date,slot,status,created_at FROM seat_reservations WHERE id=?`, id)
	var r SeatReservation
	if err := row.Scan(&r.ID, &r.SeatID, &r.PatronID, &r.ReserveDate, &r.Slot, &r.Status, &r.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrReservationNotFound
		}
		return nil, err
	}
	return &r, nil
}

// PatronSeatReservations 读者预约列表（activeOnly=true 时只看 active/checked_in）。
func (s *Store) PatronSeatReservations(patronID int64, activeOnly bool) ([]SeatReservation, error) {
	q := `SELECT id,seat_id,patron_id,reserve_date,slot,status,created_at FROM seat_reservations WHERE patron_id=?`
	if activeOnly {
		q += ` AND status IN ('active','checked_in')`
	}
	q += ` ORDER BY reserve_date DESC, id DESC`
	rows, err := s.DB.Query(q, patronID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SeatReservation{}
	for rows.Next() {
		var r SeatReservation
		if err := rows.Scan(&r.ID, &r.SeatID, &r.PatronID, &r.ReserveDate, &r.Slot, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SeatReservationConflict 同座位同时段是否已有未取消预约。
func (s *Store) SeatReservationConflict(seatID int64, date, slot string) (bool, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM seat_reservations WHERE seat_id=? AND reserve_date=? AND slot=? AND status IN ('active','checked_in')`,
		seatID, date, slot).Scan(&n)
	return n > 0, err
}

// UpdateSeatReservationStatus 更新预约状态（仅当当前状态匹配时）。
func (s *Store) UpdateSeatReservationStatus(id int64, from, to string) (bool, error) {
	res, err := s.DB.Exec(`UPDATE seat_reservations SET status=? WHERE id=? AND status=?`, to, id, from)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// UpdateSeatStatus 更新座位实时占用状态。
func (s *Store) UpdateSeatStatus(id int64, status string) error {
	_, err := s.DB.Exec(`UPDATE seats SET status=? WHERE id=?`, status, id)
	return err
}

// slotEnd 各时段结束时间（HH:MM）。
var slotEnd = map[string]string{
	"morning": "12:00", "afternoon": "17:00", "evening": "22:00",
}

// seatReservationExpired 判断预约是否已过期：
// - 预约日期早于今天 → 过期
// - 今天且时段已结束且未签到（active）→ 过期（超时未到，自动释放）
// - 今天已签到（checked_in）且时段已结束 → 释放实时占用（记录保留表示用过）
func seatReservationExpired(date, slot, status string) bool {
	today := Now()
	if date < today {
		return true
	}
	if date > today {
		return false
	}
	end, ok := slotEnd[slot]
	if !ok {
		return false
	}
	return time.Now().Format("15:04") >= end
}

// ExpireStaleSeatReservations 惰性清理过期预约：
// - active 过期 → 置 expired
// - checked_in 时段结束 → 释放座位实时占用（occupied → available）
// 返回处理的记录数。幂等，可在任何座位查询前调用。
func (s *Store) ExpireStaleSeatReservations() (int, error) {
	rows, err := s.DB.Query(`SELECT id, seat_id, reserve_date, slot, status
		FROM seat_reservations WHERE status IN ('active','checked_in')`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var expireIDs, releaseSeatIDs []int64
	for rows.Next() {
		var id, seatID int64
		var date, slot, status string
		if err := rows.Scan(&id, &seatID, &date, &slot, &status); err != nil {
			return 0, err
		}
		if !seatReservationExpired(date, slot, status) {
			continue
		}
		if status == "active" {
			expireIDs = append(expireIDs, id)
		} else {
			releaseSeatIDs = append(releaseSeatIDs, seatID)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range expireIDs {
		if r, err := s.DB.Exec(`UPDATE seat_reservations SET status='expired' WHERE id=? AND status='active'`, id); err == nil {
			if c, _ := r.RowsAffected(); c > 0 {
				n++
			}
		}
	}
	for _, seatID := range releaseSeatIDs {
		if r, err := s.DB.Exec(`UPDATE seats SET status='available' WHERE id=? AND status='occupied'`, seatID); err == nil {
			if c, _ := r.RowsAffected(); c > 0 {
				n++
			}
		}
	}
	return n, nil
}
