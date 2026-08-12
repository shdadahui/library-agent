package api

import (
	"net/http"

	"github.com/shdadahui/library-agent/internal/store"
)

// handleListConversations 会话列表。
func (s *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	convos, err := s.Svc.ListConversations(user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, convos)
}

// handleCreateConversation 新建会话。
func (s *Server) handleCreateConversation(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	convo, err := s.Svc.CreateConversation(user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, convo)
}

// handleConversationMessages 会话消息列表（归属校验）。
func (s *Server) handleConversationMessages(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	convo, err := s.Svc.GetConversation(id)
	if err != nil || convo.UserID != user.ID {
		writeErr(w, http.StatusNotFound, "会话不存在")
		return
	}
	msgs, err := s.Svc.ListMessages(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}

// handleDeleteConversation 删除会话。
func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	convo, err := s.Svc.GetConversation(id)
	if err != nil || convo.UserID != user.ID {
		writeErr(w, http.StatusNotFound, "会话不存在")
		return
	}
	if err := s.Svc.DeleteConversation(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

var _ = store.Now
