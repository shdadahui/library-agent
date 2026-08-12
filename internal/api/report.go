package api

import (
	"net/http"
	"strconv"
)

// handleMyReport 个人阅读报告（登录用户）。
func (s *Server) handleMyReport(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	rep, err := s.Svc.ReadingReport(user.PatronID)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// handleHotBooks 借阅热门榜。
func (s *Server) handleHotBooks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 10
	}
	hot, err := s.Svc.HotBooks(limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, hot)
}

// handleNewBooks 新书上架。
func (s *Server) handleNewBooks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 10
	}
	books, err := s.Svc.NewBooks(limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, books)
}
