package api

import (
	"net/http"
)

// POST /api/gate/scan —— 门禁扫码（入馆/出馆，当前登录用户）
func (s *Server) handleGateScan(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "请先登录")
		return
	}
	var body struct {
		Direction string `json:"direction"` // in / out
		Gate      string `json:"gate"`      // 东门/西门/北门
	}
	if !decodeBody(w, r, &body) {
		return
	}
	res, err := s.Svc.GateScan(u.PatronID, body.Direction, body.Gate)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// GET /api/gate/status —— 门禁状态（在馆人数 + 今日进出 + 最近记录）
func (s *Server) handleGateStatus(w http.ResponseWriter, _ *http.Request) {
	st, err := s.Svc.GateStatus()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}
