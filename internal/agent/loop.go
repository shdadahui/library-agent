package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/shdadahui/library-agent/internal/config"
	"github.com/shdadahui/library-agent/internal/service"
	"github.com/shdadahui/library-agent/internal/store"
)

// Event 流式事件（推送给 SSE）。
type Event struct {
	Type string `json:"type"` // message / tool_call / tool_result / done / error
	Data any    `json:"data"`
}

// Loop Agent 编排器。
type Loop struct {
	Client  *Client
	Svc     *service.Service
	Cfg     *config.Config
	Tools   []*ToolDef
	MaxIter int
	Usage   Usage // 最近一次 Run 的累计 token 用量（供 metrics/日志消费）
}

// NewLoop 创建编排器。
// 若配置的供应商缺少 API Key，自动回退 mock 模式（保证可演示）。
func NewLoop(cfg *config.Config, svc *service.Service) *Loop {
	p := cfg.Active()
	if !p.IsMock() && p.APIKey() == "" {
		log.Printf("供应商 %s 未配置 API Key（环境变量 %s），已回退 mock 模式", cfg.ActiveProvider, p.APIKeyEnv)
		p = config.Provider{DefaultModel: "mock"}
	}
	return &Loop{
		Client:  NewClient(p.BaseURL, p.APIKey(), p.DefaultModel),
		Svc:     svc,
		Cfg:     cfg,
		Tools:   AllTools(),
		MaxIter: cfg.MaxIterations,
	}
}

// Run 执行一次对话：LLM tool calling 循环。
// history 为之前的对话（user/assistant 文本消息，不含工具中间过程），用于多轮上下文；
// emit 用于推送流式事件；返回最终回复文本。
func (l *Loop) Run(ctx context.Context, patron *store.Patron, history []Message, userMsg string, emit func(Event)) (string, error) {
	// 意图预过滤：无关主题/纯闲聊直接本地回复，不调用 LLM（节省 token）
	if reply, ok := l.preFilterReply(userMsg); ok {
		emit(Event{Type: "message", Data: map[string]string{"delta": reply}})
		emit(Event{Type: "done", Data: map[string]any{}})
		return reply, nil
	}
	l.Usage = Usage{} // 清零本轮统计

	system := l.buildSystemPrompt(patron)
	messages := []Message{{Role: "system", Content: system}}
	for _, h := range history {
		if h.Role != "user" && h.Role != "assistant" {
			continue
		}
		if strings.TrimSpace(h.Content) == "" {
			continue
		}
		messages = append(messages, h)
	}
	messages = append(messages, Message{Role: "user", Content: userMsg})

	if l.Cfg.Active().IsMock() {
		return l.runMock(ctx, patron, userMsg, emit), nil
	}

	var finalText strings.Builder
	for iter := 0; iter < l.MaxIter; iter++ {
		req := ChatRequest{
			Messages:    messages,
			Tools:       ToOpenAI(l.Tools),
			Temperature: l.Cfg.Temperature,
		}
		var res *ChatResult
		var err error
		// LLM 偶发返回空响应：无内容且无工具调用时重试（最多 2 次）
		for attempt := 0; attempt < 2; attempt++ {
			res, err = l.Client.ChatStream(ctx, req, func(delta string) {
				finalText.WriteString(delta)
				emit(Event{Type: "message", Data: map[string]string{"delta": delta}})
			})
			if err != nil {
				break
			}
			if len(res.ToolCalls) > 0 || res.Content != "" {
				break
			}
		}
		if err != nil {
			emit(Event{Type: "error", Data: map[string]string{"message": err.Error()}})
			return finalText.String(), err
		}
		// token 用量累计（供应商支持时）
		l.Usage.PromptTokens += res.Usage.PromptTokens
		l.Usage.CompletionTokens += res.Usage.CompletionTokens
		toolCalls := res.ToolCalls
		content := res.Content
		if len(toolCalls) == 0 {
			if content == "" {
				content = "抱歉，我暂时无法处理这个请求，请换个说法试试。"
				finalText.WriteString(content)
				emit(Event{Type: "message", Data: map[string]string{"delta": content}})
			}
			break // 无工具调用，对话完成
		}
		// 记录助手消息（含工具调用），随后逐个执行
		asm := Message{Role: "assistant", Content: content, ToolCalls: toolCalls}
		messages = append(messages, asm)
		for _, tc := range toolCalls {
			result := l.executeTool(ctx, patron, tc, emit)
			messages = append(messages, Message{
				Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: result,
			})
		}
		// 续跑下一轮：助手看到工具结果后决定是继续调用还是最终答复
		emit(Event{Type: "message", Data: map[string]string{"delta": "\n"}})
	}
	emit(Event{Type: "done", Data: map[string]any{}})
	return finalText.String(), nil
}

// maxToolResultLen 回传 LLM 的工具结果最大长度（超长截断，控制上下文 token）。
const maxToolResultLen = 2000

// executeTool 执行单个工具调用并推送事件，返回 JSON 字符串结果（超长截断）。
// 执行前按工具 schema 校验参数（required/类型/枚举），不合法则回传错误让 LLM 修正。
func (l *Loop) executeTool(ctx context.Context, patron *store.Patron, tc ToolCall, emit func(Event)) string {
	def := FindTool(l.Tools, tc.Function.Name)
	emit(Event{Type: "tool_call", Data: map[string]any{
		"id": tc.ID, "name": tc.Function.Name, "arguments": tc.Function.Arguments,
	}})
	if def == nil {
		msg := fmt.Sprintf("未知工具: %s", tc.Function.Name)
		emit(Event{Type: "tool_result", Data: map[string]any{"id": tc.ID, "name": tc.Function.Name, "error": msg}})
		return jsonMsg(map[string]any{"error": msg})
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		msg := "工具参数解析失败: " + err.Error()
		emit(Event{Type: "tool_result", Data: map[string]any{"id": tc.ID, "name": tc.Function.Name, "error": msg}})
		return jsonMsg(map[string]any{"error": msg})
	}
	// 工具参数中若缺 patron_id 且工具需要，自动注入当前读者（身份由会话注入）
	if _, need := args["patron_id"]; !need && patron != nil {
		args["patron_id"] = patron.ID
	}
	// schema 校验：required/类型/枚举（不合法回传错误，让 LLM 自行修正后重试）
	if err := validateArgs(def, args); err != nil {
		msg := "工具参数不合法: " + err.Error()
		emit(Event{Type: "tool_result", Data: map[string]any{"id": tc.ID, "name": tc.Function.Name, "error": msg}})
		return jsonMsg(map[string]any{"error": msg})
	}
	result, err := def.Handler(ctx, l.Svc, args)
	if err != nil {
		msg := err.Error()
		emit(Event{Type: "tool_result", Data: map[string]any{"id": tc.ID, "name": tc.Function.Name, "error": msg}})
		return jsonMsg(map[string]any{"error": msg})
	}
	out := jsonMsg(result)
	if len(out) > maxToolResultLen {
		out = out[:maxToolResultLen] + "\n…(结果过长已截断，共 " + itoaSafe(len(out)) + " 字符)"
	}
	emit(Event{Type: "tool_result", Data: map[string]any{"id": tc.ID, "name": tc.Function.Name, "result": result}})
	return out
}

// itoaSafe 整数转字符串（工具结果截断提示用）。
func itoaSafe(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// validateArgs 按工具 JSON Schema 校验参数：required 存在性、类型、枚举。
// 校验失败返回错误，由 executeTool 回传 LLM 让其修正参数。
func validateArgs(def *ToolDef, args map[string]any) error {
	if def.Parameters == nil {
		return nil
	}
	props, _ := def.Parameters["properties"].(map[string]any)
	if reqList, ok := def.Parameters["required"].([]any); ok {
		for _, r := range reqList {
			name, _ := r.(string)
			if _, ok := args[name]; !ok {
				return fmt.Errorf("缺少必填参数 %s", name)
			}
		}
	}
	for name, p := range props {
		v, ok := args[name]
		if !ok {
			continue
		}
		schema, _ := p.(map[string]any)
		typ, _ := schema["type"].(string)
		if typ != "" && !typeMatches(typ, v) {
			return fmt.Errorf("参数 %s 类型应为 %s，实际 %T", name, typ, v)
		}
		if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
			sv := fmt.Sprintf("%v", v)
			matched := false
			for _, e := range enum {
				if fmt.Sprintf("%v", e) == sv {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("参数 %s 取值 %s 不在允许范围内", name, sv)
			}
		}
	}
	return nil
}

// typeMatches 校验值是否匹配 schema 类型；integer 兼容数字字符串（LLM 常传 "5"）。
func typeMatches(typ string, v any) bool {
	switch typ {
	case "string":
		_, ok := v.(string)
		return ok
	case "integer", "number":
		switch t := v.(type) {
		case float64, int64, int, json.Number:
			return true
		case string:
			_, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
			return err == nil
		}
		return false
	case "boolean":
		_, ok := v.(bool)
		return ok
	default:
		return true // 未知类型不拦截
	}
}

// buildSystemPrompt 构造系统提示词（含当前读者身份）。
func (l *Loop) buildSystemPrompt(patron *store.Patron) string {
	var sb strings.Builder
	sb.WriteString("你是「图书馆智能助手」，运行在一所高校图书馆的流通服务系统上。\n")
	sb.WriteString("你可以通过工具查询书目、馆藏、借阅记录，并执行借书、还书、续借、预约等操作。\n")
	sb.WriteString("规则：\n")
	sb.WriteString("1. 用户提到借书/还书/续借/预约时，先查询确认再操作，操作后明确告知结果。\n")
	sb.WriteString("2. 借书必须到馆办理（自助借还机或服务台），线上不能直接借出。用户表达借书意图时：有可借副本 → 调用 guide_borrow 告知馆藏位置并引导到馆；全部被借出 → 直接调用 place_hold 完成预约排队（无需再征求确认）。\n")
	sb.WriteString("3. 续借每本最多 2 次；逾期或被人预约时不可续借。\n")
	sb.WriteString("4. 逾期罚款按 0.1 元/天计算，还书时自动结算。\n")
	sb.WriteString("5. 用户明确说「预约」某本书时，检索并确认无可用副本后应直接调用 place_hold 完成预约，无需再征求确认。\n")
	sb.WriteString("6. 还书/续借时若用户未指定具体书目，先查询在借清单，再询问用户要处理哪一本；不要擅自选择。\n")
	sb.WriteString("7. 用户问图书馆的藏书量、借出量、读者数等统计问题时，调用 get_library_stats 回答。\n")
	sb.WriteString("8. 用户请求推荐图书（\"推荐几本书\"\"有什么好书\"\"我喜欢科幻/数学\"\"根据我借的书推荐\"）时，调用 recommend_books；可向用户询问兴趣主题以获得更好推荐。\n")
	sb.WriteString("9. 用户问图书馆政策/规则/流程问题（\"能借几本\"\"续借几次\"\"罚款怎么算\"\"如何预约\"\"怎么借书\"）时，先调用 rag_search 获取相关文档片段，再结合用清晰的中文回答。\n")
	sb.WriteString("10. 座位相关：用户问\"有哪些空座位/自习座位/占座\"时先 search_seats 查可用座位；用户明确说\"预约/订座位\"时：先 search_seats 查可用座位，然后直接 reserve_seat 预约第一个可用座位（不要反问用户偏好区域，除非用户主动指定），完成后告知座位号、区域、时段与签到要求；取消座位先 get_my_seat_reservations 找到记录再 cancel_seat_reservation。同一读者一天最多 1 个座位。\n")
	sb.WriteString("11. 门禁相关：用户说\"进馆/入馆\"时调用 gate_scan（direction=in），\"出馆/离开\"时调用 gate_scan（direction=out）；问\"馆里有多少人/在馆人数\"时调用 gate_status。\n")
	sb.WriteString("12. 回答使用简体中文，语气友好简洁，金额用「元」；涉及列表、对比时可用 Markdown 表格。\n")
	if patron != nil {
		fmt.Fprintf(&sb, "当前登录读者：%s（读者ID %d）。涉及\"我\"的借阅、罚款、预约操作都指这位读者，工具参数 patron_id 用 %d。\n",
			patron.Name, patron.ID, patron.ID)
	}
	return sb.String()
}

func jsonMsg(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
