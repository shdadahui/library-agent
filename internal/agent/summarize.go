package agent

import "strings"

// SummarizeHistory 历史消息压缩：超过 max 条时，将最早的旧消息合并为一条 system 摘要，
// 保留最近 max 条原文。纯规则实现（不额外消耗 LLM token）。
func SummarizeHistory(history []Message, max int) []Message {
	if len(history) <= max {
		return history
	}
	keep := history[len(history)-max:]
	old := history[:len(history)-max]
	var sb strings.Builder
	sb.WriteString("以下是更早的对话摘要（供上下文衔接，无需重复回答）：")
	for i, m := range old {
		if i > 0 {
			sb.WriteString("；")
		}
		role := "用户"
		if m.Role == "assistant" {
			role = "助手"
		}
		content := m.Content
		if r := []rune(content); len(r) > 60 {
			content = string(r[:60]) + "…"
		}
		sb.WriteString(role + "说：" + content)
	}
	summary := Message{Role: "system", Content: sb.String()}
	out := make([]Message, 0, len(keep)+1)
	out = append(out, summary)
	out = append(out, keep...)
	return out
}
