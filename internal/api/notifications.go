package api

import (
	"net/http"

	"github.com/shdadahui/library-agent/internal/store"
)

// handleMyNotifications 我的通知（含未读数）。
func (s *Server) handleMyNotifications(w http.ResponseWriter, r *http.Request) {
	pid, ok := currentPatronID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "请先登录")
		return
	}
	view, err := s.Svc.Notifications(pid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleMarkNotificationsRead 全部标记已读。
func (s *Server) handleMarkNotificationsRead(w http.ResponseWriter, r *http.Request) {
	pid, ok := currentPatronID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "请先登录")
		return
	}
	if err := s.Svc.MarkNotificationsRead(pid); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleCancelHold 取消预约（本人；等待中可取消）。
func (s *Server) handleCancelHold(w http.ResponseWriter, r *http.Request) {
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
	if err := s.Svc.CancelHold(pid, id); err != nil {
		code := http.StatusConflict
		if err == store.ErrHoldNotCancelable {
			code = http.StatusConflict
		}
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
