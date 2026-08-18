package api

import (
	"net/http"
	"strconv"
)

// ---- 读者社交：收藏 / 评分 ----

// handleToggleFavorite 收藏/取消收藏（登录读者本人）。
func (s *Server) handleToggleFavorite(w http.ResponseWriter, r *http.Request) {
	pid, ok := currentPatronID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "请先登录")
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	fav, err := s.Svc.ToggleFavorite(pid, id)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"favorited": fav})
}

// handleMyFavorites 我的收藏列表。
func (s *Server) handleMyFavorites(w http.ResponseWriter, r *http.Request) {
	pid, ok := currentPatronID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "请先登录")
		return
	}
	favs, err := s.Svc.MyFavorites(pid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": favs})
}

// handleRateBook 评分（登录读者本人，1-5 星）。
func (s *Server) handleRateBook(w http.ResponseWriter, r *http.Request) {
	pid, ok := currentPatronID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "请先登录")
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Score int `json:"score"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	avg, count, err := s.Svc.RateBook(pid, id, body.Score)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"avg": avg, "count": count})
}

// handleBiblioRating 书目均分（公开）。
func (s *Server) handleBiblioRating(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	avg, count, err := s.Svc.BiblioRating(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"biblio_id": id, "avg": avg, "count": count})
}

// handleAdminSetVip 管理端设置 VIP。
func (s *Server) handleAdminSetVip(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Vip bool `json:"vip"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.Svc.SetVip(id, body.Vip); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "vip": body.Vip})
}

var _ = strconv.Itoa // keep import
