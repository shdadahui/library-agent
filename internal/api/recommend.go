package api

import (
	"net/http"
	"strconv"
)

// handleRecommend 智能推荐（登录用户基于借阅历史；?taste= 按兴趣主题）。
func (s *Server) handleRecommend(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	taste := r.URL.Query().Get("taste")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	recs, err := s.Svc.RecommendForPatron(user.PatronID, taste, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, recs)
}
