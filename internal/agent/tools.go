package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/shdadahui/library-agent/internal/service"
)

// ToolDef 工具定义：Schema + Handler 注册表，与业务层解耦。
type ToolDef struct {
	Name        string
	Description string
	Parameters  map[string]any
	Handler     func(ctx context.Context, s *service.Service, args map[string]any) (any, error)
}

// AllTools 全部工具定义。
func AllTools() []*ToolDef {
	return []*ToolDef{
		{
			Name:        "search_books",
			Description: "按书名/作者/主题检索图书馆书目，返回书目列表及可借副本数。用户想找书、查某本书是否存在时使用。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"q":    map[string]any{"type": "string", "description": "检索关键词（书名/作者/主题）"},
					"lang": map[string]any{"type": "string", "enum": []string{"zh", "en"}, "description": "语种过滤，可选"},
				},
				"required": []string{"q"},
			},
			Handler: func(_ context.Context, s *service.Service, args map[string]any) (any, error) {
				q, _ := args["q"].(string)
				lang, _ := args["lang"].(string)
				books, err := s.SearchBooks(q, lang, 10)
				if err != nil {
					return nil, err
				}
				if len(books) == 0 {
					return map[string]any{"message": "没有找到相关图书"}, nil
				}
				return books, nil
			},
		},
		{
			Name:        "get_book_availability",
			Description: "查询某本书的馆藏状态：各副本是否可借（available）、借出时的借阅人与应还日期、预约排队人数（queue_count）、全书是否有可借副本（has_available）。参数 book_id 与 title 二选一。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"book_id": map[string]any{"type": "integer", "description": "书目 ID（search_books 返回的 id）"},
					"title":   map[string]any{"type": "string", "description": "书名，优先用 book_id"},
				},
			},
			Handler: func(_ context.Context, s *service.Service, args map[string]any) (any, error) {
				id, err := resolveBookID(s, args)
				if err != nil {
					return nil, err
				}
				b, items, err := s.BookAvailability(id)
				if err != nil {
					return nil, err
				}
				return map[string]any{"book": b, "items": items}, nil
			},
		},
		{
			Name:        "get_my_loans",
			Description: "查询当前读者的在借图书清单：书名、应还日期、是否可续借及原因。用户问\"我借了什么\"\"快到期了\"\"我要续借\"时使用。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"patron_id": map[string]any{"type": "integer", "description": "读者 ID"},
				},
				"required": []string{"patron_id"},
			},
			Handler: func(_ context.Context, s *service.Service, args map[string]any) (any, error) {
				pid, err := intArg(args, "patron_id")
				if err != nil {
					return nil, err
				}
				loans, err := s.PatronLoans(pid)
				if err != nil {
					return nil, err
				}
				if len(loans) == 0 {
					return map[string]any{"message": "当前没有在借图书"}, nil
				}
				return loans, nil
			},
		},
		{
			Name:        "borrow_book",
			Description: "为读者借出一本书（馆藏副本）。用户说\"我要借《XX》\"\"帮我借这本书\"时，先 search_books 找到 book_id，再 get_book_availability 找到可借的 item_id，再调用本工具。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"patron_id": map[string]any{"type": "integer", "description": "读者 ID"},
					"item_id":   map[string]any{"type": "integer", "description": "馆藏副本 ID（可借状态的）"},
				},
				"required": []string{"patron_id", "item_id"},
			},
			Handler: func(_ context.Context, s *service.Service, args map[string]any) (any, error) {
				pid, err := intArg(args, "patron_id")
				if err != nil {
					return nil, err
				}
				iid, err := intArg(args, "item_id")
				if err != nil {
					return nil, err
				}
				loan, err := s.Borrow(pid, iid)
				if err != nil {
					return nil, err
				}
				return map[string]any{"loan": loan, "message": "借阅成功"}, nil
			},
		},
		{
			Name:        "return_book",
			Description: "归还图书。用户说\"我还书\"\"这本书还了\"时使用，先 get_my_loans 找到 loan_id。归还时自动计算逾期罚款并通知预约者。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"loan_id": map[string]any{"type": "integer", "description": "借阅记录 ID"},
				},
				"required": []string{"loan_id"},
			},
			Handler: func(_ context.Context, s *service.Service, args map[string]any) (any, error) {
				lid, err := intArg(args, "loan_id")
				if err != nil {
					return nil, err
				}
				res, err := s.Return(lid)
				if err != nil {
					return nil, err
				}
				out := map[string]any{"message": "归还成功", "fine_yuan": float64(res.FineCents) / 100}
				if res.HoldWakeUp != "" {
					out["hold_notice"] = res.HoldWakeUp
				}
				return out, nil
			},
		},
		{
			Name:        "renew_loan",
			Description: "续借图书，延长借期（每本最多续借 2 次，逾期或被预约时不可续借）。用户说\"续借\"\"再借一段时间\"时，先 get_my_loans 找到 loan_id 和可续借状态。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"loan_id": map[string]any{"type": "integer", "description": "借阅记录 ID"},
				},
				"required": []string{"loan_id"},
			},
			Handler: func(_ context.Context, s *service.Service, args map[string]any) (any, error) {
				lid, err := intArg(args, "loan_id")
				if err != nil {
					return nil, err
				}
				loan, err := s.Renew(lid)
				if err != nil {
					return nil, err
				}
				return map[string]any{"loan": loan, "message": "续借成功"}, nil
			},
		},
		{
			Name:        "get_my_fines",
			Description: "查询当前读者的未缴逾期罚款。用户问\"我有多少罚款\"\"欠费\"\"逾期\"时使用。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"patron_id": map[string]any{"type": "integer", "description": "读者 ID"},
				},
				"required": []string{"patron_id"},
			},
			Handler: func(_ context.Context, s *service.Service, args map[string]any) (any, error) {
				pid, err := intArg(args, "patron_id")
				if err != nil {
					return nil, err
				}
				fines, err := s.Fines(pid)
				if err != nil {
					return nil, err
				}
				if fines.UnpaidCents == 0 {
					return map[string]any{"message": "没有未缴罚款", "unpaid_yuan": 0}, nil
				}
				return map[string]any{"unpaid_yuan": float64(fines.UnpaidCents) / 100, "items": fines.Items}, nil
			},
		},
		{
			Name:        "place_hold",
			Description: "预约一本书。无论当前排队人数多少，只要该书全部副本被借出（has_available 为 false）且读者尚未预约，就调用本工具为读者排队，归还后自动通知。用户说\"预约\"\"帮我排队\"时使用。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"patron_id": map[string]any{"type": "integer", "description": "读者 ID"},
					"book_id":   map[string]any{"type": "integer", "description": "书目 ID"},
				},
				"required": []string{"patron_id", "book_id"},
			},
			Handler: func(_ context.Context, s *service.Service, args map[string]any) (any, error) {
				pid, err := intArg(args, "patron_id")
				if err != nil {
					return nil, err
				}
				bid, err := intArg(args, "book_id")
				if err != nil {
					return nil, err
				}
				hold, err := s.PlaceHold(pid, bid)
				if err != nil {
					return nil, err
				}
				return map[string]any{"hold": hold, "message": "预约成功，排队中"}, nil
			},
		},
	}
}

// ToOpenAI 将工具定义转为 OpenAI tools 格式。
func ToOpenAI(defs []*ToolDef) []Tool {
	out := make([]Tool, 0, len(defs))
	for _, d := range defs {
		out = append(out, Tool{Type: "function", Function: ToolFunction{
			Name: d.Name, Description: d.Description, Parameters: d.Parameters,
		}})
	}
	return out
}

// FindTool 按名称查工具。
func FindTool(defs []*ToolDef, name string) *ToolDef {
	for _, d := range defs {
		if d.Name == name {
			return d
		}
	}
	return nil
}

// ---- 参数解析辅助 ----

func intArg(args map[string]any, key string) (int64, error) {
	switch v := args[key].(type) {
	case float64:
		return int64(v), nil
	case json.Number:
		return v.Int64()
	case int64:
		return v, nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	default:
		return 0, fmt.Errorf("参数 %s 缺失或类型错误", key)
	}
}

// resolveBookID 从 args 中解析书目 ID（book_id 优先，其次用 title 检索第一个结果）。
func resolveBookID(s *service.Service, args map[string]any) (int64, error) {
	if v, ok := args["book_id"]; ok && v != nil {
		return intArg(args, "book_id")
	}
	title, ok := args["title"].(string)
	if !ok || strings.TrimSpace(title) == "" {
		return 0, errors.New("请提供 book_id 或 title")
	}
	books, err := s.SearchBooks(strings.TrimSpace(title), "", 1)
	if err != nil {
		return 0, err
	}
	if len(books) == 0 {
		return 0, fmt.Errorf("未找到书名《%s》", title)
	}
	return books[0].ID, nil
}
