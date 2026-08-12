package service

import (
	"errors"

	"github.com/shdadahui/library-agent/internal/store"
)

// 门禁错误。
var (
	ErrGateAlreadyIn  = errors.New("您已在馆内，请勿重复扫码")
	ErrGateNotIn      = errors.New("您当前不在馆内，请先入馆")
	ErrGateDirection  = errors.New("无效的通行方向（in/out）")
)

// GateScanResult 扫码通行结果。
type GateScanResult struct {
	ID        int64  `json:"id"`
	Patron    string `json:"patron"`
	Direction string `json:"direction"` // in / out
	Gate      string `json:"gate"`
	Time      string `json:"time"`
	InLibrary int    `json:"in_library"`          // 通行后当前在馆人数
	Warnings  []string `json:"warnings,omitempty"` // 逾期/罚款提示（不拦截通行）
}

// GateScan 门禁扫码：入馆/出馆。
func (s *Service) GateScan(patronID int64, direction, gate string) (*GateScanResult, error) {
	if direction != "in" && direction != "out" {
		return nil, ErrGateDirection
	}
	if gate == "" {
		gate = "东门"
	}
	p, err := s.st.GetPatron(patronID)
	if err != nil {
		return nil, ErrPatronNotFound
	}
	last, err := s.st.LastGateDirection(patronID)
	if err != nil {
		return nil, err
	}
	if direction == "in" && last == "in" {
		return nil, ErrGateAlreadyIn
	}
	if direction == "out" && last != "in" {
		return nil, ErrGateNotIn
	}
	g := &store.GateLog{
		PatronID: patronID, Direction: direction, Gate: gate,
		VerifiedBy: "qr", CreatedAt: store.NowDateTime(),
	}
	id, err := s.st.InsertGateLog(g)
	if err != nil {
		return nil, err
	}
	res := &GateScanResult{ID: id, Patron: p.Name, Direction: direction, Gate: gate, Time: g.CreatedAt}
	if direction == "in" {
		res.Warnings = s.gateWarnings(patronID)
	}
	n, err := s.st.InLibraryCount()
	if err == nil {
		res.InLibrary = n
	}
	return res, nil
}

// gateWarnings 入馆时的读者状态提示（不影响通行）。
func (s *Service) gateWarnings(patronID int64) []string {
	var warns []string
	if sum, err := s.st.SumUnpaidFines(patronID); err == nil && sum > 0 {
		warns = append(warns, "您有未缴罚款，请及时缴纳以免影响借阅")
	}
	if loans, err := s.st.ActiveLoans(patronID); err == nil {
		today := store.Now()
		overdue := 0
		for _, l := range loans {
			if l.DueDate < today {
				overdue++
			}
		}
		if overdue > 0 {
			warns = append(warns, "您有逾期未还的图书，请尽快归还")
		}
	}
	return warns
}

// GateStatusView 门禁状态。
type GateStatusView struct {
	*store.GateStats
	Recent []GateLogView `json:"recent"`
}

// GateLogView 通行记录视图（含读者名）。
type GateLogView struct {
	store.GateLog
	PatronName string `json:"patron_name"`
}

// GateStatus 门禁状态：在馆人数 + 今日进出 + 最近记录。
func (s *Service) GateStatus() (*GateStatusView, error) {
	stats, err := s.st.GateStatsToday(store.Now())
	if err != nil {
		return nil, err
	}
	logs, err := s.st.RecentGateLogs(15)
	if err != nil {
		return nil, err
	}
	views := make([]GateLogView, 0, len(logs))
	for _, g := range logs {
		v := GateLogView{GateLog: g}
		if p, err := s.st.GetPatron(g.PatronID); err == nil {
			v.PatronName = p.Name
		}
		views = append(views, v)
	}
	return &GateStatusView{GateStats: stats, Recent: views}, nil
}
