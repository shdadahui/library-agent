package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/shdadahui/library-agent/internal/agent"
)

// ChatRequest 聊天请求体。
type ChatRequest struct {
	Message  string `json:"message"`
	PatronID int64  `json:"patron_id"`
}

// handleChat SSE 流式聊天端点。
// 事件流：event: message / tool_call / tool_result / done / error，data 为 JSON。
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var body ChatRequest
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Message == "" {
		writeErr(w, http.StatusBadRequest, "message 不能为空")
		return
	}
	patron, err := s.Svc.Patron(body.PatronID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "读者不存在，请先选择读者")
		return
	}

	// SSE 响应头
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "当前连接不支持流式输出")
		return
	}

	emit := func(ev agent.Event) {
		data, _ := json.Marshal(ev.Data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
		flusher.Flush()
	}

	// 请求 ctx 直接取自 r.Context()，客户端断开会自动取消 LLM 请求
	_, _ = s.Loop.Run(r.Context(), patron, body.Message, emit)
}
