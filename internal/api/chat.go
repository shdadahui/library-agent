package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/shdadahui/library-agent/internal/agent"
	"github.com/shdadahui/library-agent/internal/auth"
	"github.com/shdadahui/library-agent/internal/store"
)

// ChatRequest 聊天请求体。
// 身份来自登录令牌，历史会话按 conversation_id 从数据库加载并持久化。
type ChatRequest struct {
	ConversationID *int64 `json:"conversation_id"`
	Message        string `json:"message"`
}

// maxContextMessages 注入上下文的最近消息条数（控制 token 消耗）。
const maxContextMessages = 20

// chatRatePerMin 每用户每分钟最大对话请求数（防恶意刷 token 成本）。
const chatRatePerMin = 30

// handleChat SSE 流式聊天端点。
// 事件流：event: message / tool_call / tool_result / done / error，data 为 JSON。
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	user := currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "请先登录")
		return
	}
	// 频率限流：每用户每分钟 N 次（Redis 计数，复用会话存储）
	if err := s.Auth.CheckRate("chat_rate:", user.ID, chatRatePerMin, time.Minute); err != nil {
		s.metrics.IncRateLimited()
		writeErr(w, http.StatusTooManyRequests, err.Error())
		return
	}
	var body ChatRequest
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Message == "" {
		writeErr(w, http.StatusBadRequest, "message 不能为空")
		return
	}
	patron, err := s.Svc.Patron(user.PatronID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "读者不存在")
		return
	}

	// 会话归属：指定会话须属于当前用户；未指定则自动新建
	conversationID := body.ConversationID
	if conversationID != nil {
		convo, err := s.Svc.GetConversation(*conversationID)
		if err != nil || convo.UserID != user.ID {
			writeErr(w, http.StatusNotFound, "会话不存在")
			return
		}
	}
	isNewConversation := conversationID == nil
	if isNewConversation {
		convo, err := s.Svc.CreateConversation(user.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		conversationID = &convo.ID
	}
	cid := *conversationID

	// 历史上下文（仅 user/assistant 文本，最近 N 条；超长时压缩早期消息）
	history := []agent.Message{}
	if msgs, err := s.Svc.ListMessages(cid); err == nil {
		from := 0
		if len(msgs) > maxContextMessages {
			from = len(msgs) - maxContextMessages
		}
		for _, m := range msgs[from:] {
			history = append(history, agent.Message{Role: m.Role, Content: m.Content})
		}
		// 若历史总量超限（含被裁掉的部分），压缩早期消息为摘要
		if len(msgs) > maxContextMessages*2 {
			history = agent.SummarizeHistory(history, maxContextMessages)
		}
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

	// SSE 写入互斥：心跳 goroutine 与事件回调并发写同一 writer
	var wmu sync.Mutex
	writeSSE := func(evType string, data any) {
		wmu.Lock()
		defer wmu.Unlock()
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evType, b)
		flusher.Flush()
	}
	// 心跳：LLM 长生成期间每 15s 发注释行，防止 nginx/网关断连
	hbStop := make(chan struct{})
	defer close(hbStop)
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-hbStop:
				return
			case <-t.C:
				wmu.Lock()
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
				wmu.Unlock()
			}
		}
	}()

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
		writeSSE(ev.Type, ev.Data)
	}

	finalText, runErr := s.Loop.Run(r.Context(), patron, history, body.Message, emit)
	latency := time.Since(start)
	// token 用量统计（本轮累计）
	promptTok := int64(s.Loop.Usage.PromptTokens)
	completionTok := int64(s.Loop.Usage.CompletionTokens)
	if promptTok > 0 || completionTok > 0 {
		s.metrics.AddTokens(promptTok, completionTok)
	}

	// 持久化本轮消息（user + assistant）
	_ = s.Svc.AddMessage(cid, "user", body.Message)
	reply := finalText
	if reply == "" {
		reply = "（无回复）"
	}
	_ = s.Svc.AddMessage(cid, "assistant", reply)
	if isNewConversation {
		_ = s.Svc.RenameConversation(cid, body.Message)
	}

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
		"time":            time.Now().Format(time.RFC3339),
		"user":            user.Username,
		"patron_id":       patron.ID,
		"conversation_id": cid,
		"input":           body.Message,
		"tools":           tools,
		"output":          reply,
		"latency_ms":      latency.Milliseconds(),
		"prompt_tokens":   promptTok,
		"completion_tokens": completionTok,
		"error":           errMsg,
	})

	// 前端需要知道会话 ID（新建会话时）
	if isNewConversation {
		writeSSE("conversation_id", map[string]any{"conversation_id": cid})
	}
}

var _ = store.Now
var _ = auth.ErrRateLimited
