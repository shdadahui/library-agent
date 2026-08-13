// Package agent 实现 LLM 客户端与 tool calling 编排循环。
// 客户端零依赖：仅用标准库 net/http 直连 OpenAI 兼容 /chat/completions（仿 tutorialsmith）。
package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message 对话消息（OpenAI 兼容）。
type Message struct {
	Role       string     `json:"role"` // system / user / assistant / tool
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall 工具调用（由助手消息携带）。
type ToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

// Function 工具调用的函数名与参数（参数为 JSON 字符串）。
type Function struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool 工具定义（OpenAI 格式）。
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction 工具函数元信息。
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ChatRequest 请求体。
type ChatRequest struct {
	Model         string         `json:"model"`
	Messages      []Message      `json:"messages"`
	Tools         []Tool         `json:"tools,omitempty"`
	Temperature   float64        `json:"temperature"`
	Stream        bool           `json:"stream"`
	StreamOptions map[string]any `json:"stream_options,omitempty"` // include_usage 获取 token 用量
}

// Usage token 用量（流式响应经 stream_options.include_usage 返回）。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatResult 流式对话聚合结果。
type ChatResult struct {
	ToolCalls []ToolCall
	Content   string
	Usage     Usage // 可能为零（供应商不支持时）
}

// StreamChunk 流式响应片段。
type StreamChunk struct {
	Content string    // 文本增量
	ToolAcc []ToolAcc // 工具调用增量（跨 chunk 合并）
}

// ToolAcc 工具调用增量。
type ToolAcc struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

// Client OpenAI 兼容客户端。
type Client struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

// NewClient 创建客户端。
func NewClient(baseURL, apiKey, model string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		http:    &http.Client{Timeout: 180 * time.Second},
	}
}

// ChatNonStream 非流式对话，返回助手完整消息（带重试退避）。
func (c *Client) ChatNonStream(ctx context.Context, req ChatRequest) (*Message, error) {
	req.Stream = false
	if req.Model == "" {
		req.Model = c.model
	}
	body, _ := json.Marshal(req)
	resp, err := c.doChatRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析 LLM 响应失败: %w", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("LLM 错误: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("LLM 未返回 choices")
	}
	msg := out.Choices[0].Message
	return &msg, nil
}

// maxLLMAttempts 请求最大尝试次数（1 次原始 + 2 次重试）。
const maxLLMAttempts = 3

// doChatRequest 发送请求；429/5xx/网络错误按指数退避重试（1s/2s/4s），4xx 业务错误不重试。
func (c *Client) doChatRequest(ctx context.Context, body []byte) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < maxLLMAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("请求 LLM 失败: %w", err)
			if ctx.Err() != nil {
				return nil, lastErr // 用户取消，不重试
			}
			continue // 网络错误可重试
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		msg := fmt.Sprintf("LLM 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = errors.New(msg)
			continue // 限流/服务端错误可重试
		}
		return nil, errors.New(msg) // 4xx 业务错误不重试
	}
	return nil, fmt.Errorf("LLM 请求重试 %d 次仍失败: %w", maxLLMAttempts, lastErr)
}

// ChatStream 流式对话：逐块回调 content 增量与聚合后的工具调用。
// 带指数退避重试（限流/5xx/网络错误）与 token 用量采集。
func (c *Client) ChatStream(ctx context.Context, req ChatRequest, onContent func(string)) (*ChatResult, error) {
	req.Stream = true
	req.StreamOptions = map[string]any{"include_usage": true}
	if req.Model == "" {
		req.Model = c.model
	}
	body, _ := json.Marshal(req)
	resp, err := c.doChatRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	acc := map[int]*ToolAcc{} // index → 累积
	var order []int
	var fullText strings.Builder
	var usage Usage

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
					// 流式 tool_calls 每项带 index，与完整消息的 ToolCall 不同
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *Usage `json:"usage"` // 最后一个块携带（include_usage 开启时）
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // 忽略无法解析的块
		}
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		d := chunk.Choices[0].Delta
		if d.Content != "" {
			fullText.WriteString(d.Content)
			onContent(d.Content)
		}
		for _, tc := range d.ToolCalls {
			// OpenAI 流式 tool_calls 片段：只有第一个片段带 id 和 function.name
			a, ok := acc[tc.Index]
			if !ok {
				a = &ToolAcc{Index: tc.Index}
				acc[tc.Index] = a
				order = append(order, tc.Index)
			}
			if tc.ID != "" {
				a.ID = tc.ID
			}
			if tc.Function.Name != "" {
				a.Name = tc.Function.Name
			}
			a.Arguments += tc.Function.Arguments
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取流式响应失败: %w", err)
	}
	tools := []ToolCall{}
	for _, idx := range order {
		a := acc[idx]
		tools = append(tools, ToolCall{ID: a.ID, Type: "function", Function: Function{Name: a.Name, Arguments: a.Arguments}})
	}
	return &ChatResult{ToolCalls: tools, Content: fullText.String(), Usage: usage}, nil
}
