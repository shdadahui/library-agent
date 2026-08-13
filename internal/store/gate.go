package store

import (
	"database/sql"
	"errors"
)

// GateLog 门禁通行记录。
type GateLog struct {
	ID         int64  `json:"id"`
	PatronID   int64  `json:"patron_id"`
	Direction  string `json:"direction"`   // in / out
	Gate       string `json:"gate"`        // 闸口：东门 / 西门 / 北门
	VerifiedBy string `json:"verified_by"` // card / qr / face
	CreatedAt  string `json:"created_at"`
}

// ErrGateNoRecord 无通行记录。
var ErrGateNoRecord = errors.New("无通行记录")

// InsertGateLog 写入通行记录。
func (s *Store) InsertGateLog(g *GateLog) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO gate_logs(patron_id,direction,gate,verified_by,created_at) VALUES(?,?,?,?,?)`,
		g.PatronID, g.Direction, g.Gate, g.VerifiedBy, g.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// LastGateDirection 读者最近一条通行方向（"" 表示无记录）。
func (s *Store) LastGateDirection(patronID int64) (string, error) {
	var d string
	err := s.DB.QueryRow(`SELECT direction FROM gate_logs WHERE patron_id=? ORDER BY id DESC LIMIT 1`, patronID).Scan(&d)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return d, nil
}

// InLibraryCount 当前在馆人数：最近一条通行记录为 in 的读者数。
func (s *Store) InLibraryCount() (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM (
	  SELECT g.patron_id FROM gate_logs g
	  JOIN (SELECT patron_id, MAX(id) AS mid FROM gate_logs GROUP BY patron_id) last
	    ON last.patron_id = g.patron_id AND last.mid = g.id
	  WHERE g.direction='in'
	) t`).Scan(&n)
	return n, err
}

// RecentGateLogs 最近通行记录（含读者名，JOIN patrons）。
func (s *Store) RecentGateLogs(limit int) ([]GateLog, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.DB.Query(`SELECT id,patron_id,direction,gate,verified_by,created_at FROM gate_logs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GateLog{}
	for rows.Next() {
		var g GateLog
		if err := rows.Scan(&g.ID, &g.PatronID, &g.Direction, &g.Gate, &g.VerifiedBy, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GateStats 门禁统计。
type GateStats struct {
	InToday   int `json:"in_today"`
	OutToday  int `json:"out_today"`
	InLibrary int `json:"in_library"`
}

// GateStatsToday 今日进出统计。
func (s *Store) GateStatsToday(today string) (*GateStats, error) {
	st := &GateStats{}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM gate_logs WHERE direction='in' AND created_at LIKE ?`, today+"%").Scan(&st.InToday); err != nil {
		return nil, err
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM gate_logs WHERE direction='out' AND created_at LIKE ?`, today+"%").Scan(&st.OutToday); err != nil {
		return nil, err
	}
	n, err := s.InLibraryCount()
	if err != nil {
		return nil, err
	}
	st.InLibrary = n
	return st, nil
}
