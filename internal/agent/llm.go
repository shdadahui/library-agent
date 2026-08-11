// Package agent 实现 LLM 客户端与 tool calling 编排循环。
// 客户端零依赖：仅用标准库 net/http 直连 OpenAI 兼容 /chat/completions（仿 tutorialsmith）。
package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message 对话消息（OpenAI 兼容）。
type Message struct {
	Role       string      `json:"role"` // system / user / assistant / tool
	Content    string      `json:"content,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
	Name       string      `json:"name,omitempty"`
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
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	Temperature float64   `json:"temperature"`
	Stream      bool      `json:"stream"`
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

// ChatNonStream 非流式对话，返回助手完整消息。
func (c *Client) ChatNonStream(ctx context.Context, req ChatRequest) (*Message, error) {
	req.Stream = false
	if req.Model == "" {
		req.Model = c.model
	}
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("请求 LLM 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("LLM 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
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

// ChatStream 流式对话：逐块回调 content 增量与聚合后的工具调用。
// 返回聚合出的工具调用（可能为空）与最终文本内容。
func (c *Client) ChatStream(ctx context.Context, req ChatRequest, onContent func(string)) ([]ToolCall, string, error) {
	req.Stream = true
	if req.Model == "" {
		req.Model = c.model
	}
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("请求 LLM 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, "", fmt.Errorf("LLM 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	acc := map[int]*ToolAcc{} // index → 累积
	var order []int
	var fullText strings.Builder

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
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // 忽略无法解析的块
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
		return nil, fullText.String(), fmt.Errorf("读取流式响应失败: %w", err)
	}
	tools := []ToolCall{}
	for _, idx := range order {
		a := acc[idx]
		tools = append(tools, ToolCall{ID: a.ID, Type: "function", Function: Function{Name: a.Name, Arguments: a.Arguments}})
	}
	return tools, fullText.String(), nil
}
