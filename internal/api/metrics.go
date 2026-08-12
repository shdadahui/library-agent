package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

// Metrics 运行时统计（监控端点 /api/metrics 输出）。
type Metrics struct {
	mu                sync.Mutex
	Chats             int            `json:"chats"`              // 完成的对话轮次
	ToolCalls         map[string]int `json:"tool_calls"`         // 各工具调用次数
	ToolErrors        int            `json:"tool_errors"`        // 工具执行失败次数
	LLMErrors         int            `json:"llm_errors"`         // LLM 请求失败次数
	TotalLatencyMs    int64          `json:"total_latency_ms"`   // 对话累计耗时
	PromptTokens      int64          `json:"prompt_tokens"`      // 累计输入 token（成本可见）
	CompletionTokens  int64          `json:"completion_tokens"`  // 累计输出 token
	RateLimitedReqs   int            `json:"rate_limited_reqs"`  // 被限流的请求数
	StartTime         time.Time      `json:"-"`
}

// NewMetrics 创建统计器。
func NewMetrics() *Metrics {
	return &Metrics{ToolCalls: map[string]int{}, StartTime: time.Now()}
}

func (m *Metrics) IncChats()               { m.mu.Lock(); m.Chats++; m.mu.Unlock() }
func (m *Metrics) IncTool(name string)     { m.mu.Lock(); m.ToolCalls[name]++; m.mu.Unlock() }
func (m *Metrics) IncToolErrors()          { m.mu.Lock(); m.ToolErrors++; m.mu.Unlock() }
func (m *Metrics) IncLLMErrors()           { m.mu.Lock(); m.LLMErrors++; m.mu.Unlock() }
func (m *Metrics) AddLatency(ms int64)     { m.mu.Lock(); m.TotalLatencyMs += ms; m.mu.Unlock() }
func (m *Metrics) IncRateLimited()         { m.mu.Lock(); m.RateLimitedReqs++; m.mu.Unlock() }
func (m *Metrics) AddTokens(prompt, completion int64) {
	m.mu.Lock()
	m.PromptTokens += prompt
	m.CompletionTokens += completion
	m.mu.Unlock()
}

// Snapshot 返回可序列化快照。
func (m *Metrics) Snapshot() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	avg := float64(0)
	if m.Chats > 0 {
		avg = float64(m.TotalLatencyMs) / float64(m.Chats) / 1000
	}
	tools := map[string]int{}
	for k, v := range m.ToolCalls {
		tools[k] = v
	}
	return map[string]any{
		"uptime_sec":        int(time.Since(m.StartTime).Seconds()),
		"chats":             m.Chats,
		"tool_calls":        tools,
		"tool_errors":       m.ToolErrors,
		"llm_errors":        m.LLMErrors,
		"avg_latency_s":     avg,
		"prompt_tokens":     m.PromptTokens,
		"completion_tokens": m.CompletionTokens,
		"rate_limited":      m.RateLimitedReqs,
	}
}

// statusRecorder 记录响应状态码。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush 透传 Flush，保证 SSE 流式输出在中间件包装下可用。
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// withLogging 请求日志中间件。
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "dur", time.Since(start).Round(time.Millisecond).String())
	})
}

// handleMetrics 监控端点。
func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.metrics.Snapshot())
}

// appendChatLog 追加一条对话日志（JSON Lines，data/logs/chat-YYYYMMDD.jsonl）。
func (s *Server) appendChatLog(entry map[string]any) {
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_ = os.MkdirAll("data/logs", 0o755)
	f, err := os.OpenFile(chatLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

func chatLogPath() string {
	return "data/logs/" + "chat-" + time.Now().Format("2006-01-02") + ".jsonl"
}
