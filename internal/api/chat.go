package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/shdadahui/library-agent/internal/agent"
)

// HistoryMsg 历史对话消息（前端携带，用于多轮上下文）。
type HistoryMsg struct {
	Role    string `json:"role"` // user / assistant
	Content string `json:"content"`
}

// ChatRequest 聊天请求体。
type ChatRequest struct {
	Message  string       `json:"message"`
	PatronID int64        `json:"patron_id"`
	History  []HistoryMsg `json:"history,omitempty"`
}

// handleChat SSE 流式聊天端点。
// 事件流：event: message / tool_call / tool_result / done / error，data 为 JSON。
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
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

	// 历史消息转为 agent.Message（仅 user/assistant 文本）
	history := make([]agent.Message, 0, len(body.History))
	for _, h := range body.History {
		history = append(history, agent.Message{Role: h.Role, Content: h.Content})
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

	tools := []string{}
	var toolErr bool
	emit := func(ev agent.Event) {
		if ev.Type == "tool_call" {
			if m, ok := ev.Data.(map[string]any); ok {
				if name, ok := m["name"].(string); ok {
					tools = append(tools, name)
					s.metrics.IncTool(name)
				}
			}
		}
		if ev.Type == "error" {
			toolErr = true
		}
		data, _ := json.Marshal(ev.Data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
		flusher.Flush()
	}

	// 请求 ctx 直接取自 r.Context()，客户端断开会自动取消 LLM 请求
	finalText, runErr := s.Loop.Run(r.Context(), patron, history, body.Message, emit)
	latency := time.Since(start)

	// 监控与日志
	s.metrics.IncChats()
	s.metrics.AddLatency(latency.Milliseconds())
	if runErr != nil {
		s.metrics.IncLLMErrors()
	}
	if toolErr {
		s.metrics.IncToolErrors()
	}
	errMsg := ""
	if runErr != nil {
		errMsg = runErr.Error()
	}
	s.appendChatLog(map[string]any{
		"time":       time.Now().Format(time.RFC3339),
		"patron_id":  patron.ID,
		"patron":     patron.Name,
		"input":      body.Message,
		"tools":      tools,
		"output":     finalText,
		"latency_ms": latency.Milliseconds(),
		"error":      errMsg,
	})
}
