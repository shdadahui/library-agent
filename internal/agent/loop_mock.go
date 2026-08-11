package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/shdadahui/library-agent/internal/service"
	"github.com/shdadahui/library-agent/internal/store"
)

// runMock 本地 mock 模式：按关键词匹配意图直接调用业务层，
// 模拟 LLM 的工具调用过程，保证无网络/无 key 时也能完整演示。
func (l *Loop) runMock(ctx context.Context, patron *store.Patron, userMsg string, emit func(Event)) string {
	if patron == nil {
		emit(Event{Type: "error", Data: map[string]string{"message": "请先选择读者"}})
		return "请先在上方选择读者身份。"
	}
	msg := strings.TrimSpace(userMsg)
	var out string

	switch {
	case strings.Contains(msg, "续借"):
		out = l.mockRenew(ctx, patron, emit)
	case strings.Contains(msg, "还书") || strings.Contains(msg, "归还"):
		out = l.mockReturn(ctx, patron, emit)
	case strings.Contains(msg, "罚款") || strings.Contains(msg, "欠费") || strings.Contains(msg, "逾期费"):
		out = l.mockFines(patron, emit)
	case strings.Contains(msg, "预约") || strings.Contains(msg, "排队"):
		out = l.mockHold(ctx, patron, msg, emit)
	case strings.Contains(msg, "借") && (strings.Contains(msg, "借了") || strings.Contains(msg, "借阅") || strings.Contains(msg, "我借")):
		out = l.mockLoans(patron, emit)
	case strings.Contains(msg, "借"):
		out = l.mockBorrow(ctx, patron, msg, emit)
	case strings.Contains(msg, "查") || strings.Contains(msg, "找") || strings.Contains(msg, "搜") || strings.Contains(msg, "有没有"):
		out = l.mockSearch(ctx, patron, msg, emit)
	case strings.Contains(msg, "到期") || strings.Contains(msg, "快还") || strings.Contains(msg, "我借了什么") || strings.Contains(msg, "借的书"):
		out = l.mockLoans(patron, emit)
	default:
		out = l.mockSearch(ctx, patron, msg, emit)
	}
	// 模拟打字机：按句号分段推送
	parts := splitSentences(out)
	for i, p := range parts {
		if i > 0 {
			emit(Event{Type: "message", Data: map[string]string{"delta": "\n"}})
		}
		emit(Event{Type: "message", Data: map[string]string{"delta": p}})
	}
	emit(Event{Type: "done", Data: map[string]any{}})
	return out
}

// ---- mock 各意图实现 ----

func (l *Loop) mockSearch(ctx context.Context, patron *store.Patron, msg string, emit func(Event)) string {
	q := extractBookTitle(msg)
	if q == "" {
		q = strings.Trim(strings.TrimSpace(msg), "？?。，,！!")
		if len([]rune(q)) > 20 {
			q = string([]rune(q)[:20])
		}
	}
	emitTool(emit, "search_books", map[string]any{"q": q})
	books, err := l.Svc.SearchBooks(q, "", 5)
	if err != nil {
		return "查询出错：" + err.Error()
	}
	emitResult(emit, "search_books", books)
	if len(books) == 0 {
		return "很抱歉，没有找到与「" + q + "」相关的图书。"
	}
	var sb strings.Builder
	sb.WriteString("为您找到以下图书：\n")
	for _, b := range books {
		sb.WriteString(fmt.Sprintf("《%s》 %s（%d 年）可借 %d/%d 本，书目ID %d\n", b.Title, b.Author, b.PublishYear, b.Available, b.Total, b.ID))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (l *Loop) mockLoans(patron *store.Patron, emit func(Event)) string {
	emitTool(emit, "get_my_loans", map[string]any{"patron_id": patron.ID})
	loans, err := l.Svc.PatronLoans(patron.ID)
	if err != nil {
		return "查询出错：" + err.Error()
	}
	emitResult(emit, "get_my_loans", loans)
	if len(loans) == 0 {
		return "您当前没有在借图书。"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s，您当前在借 %d 本：\n", patron.Name, len(loans)))
	for _, v := range loans {
		flag := "✓可续借"
		if !v.Renewable {
			flag = "✗" + v.RenewMsg
		}
		sb.WriteString(fmt.Sprintf("《%s》 应还 %s（已续 %d 次）%s（借阅ID %d）\n", v.Title, v.DueDate, v.Renewals, flag, v.ID))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (l *Loop) mockBorrow(ctx context.Context, patron *store.Patron, msg string, emit func(Event)) string {
	q := extractBookTitle(msg)
	if q == "" {
		return "请问您想借哪本书？可以告诉我书名，例如「帮我借《三体》」。"
	}
	emitTool(emit, "search_books", map[string]any{"q": q})
	books, err := l.Svc.SearchBooks(q, "", 1)
	if err != nil {
		return "查询出错：" + err.Error()
	}
	if len(books) == 0 {
		return "没有找到《" + q + "》这本书。"
	}
	emitResult(emit, "search_books", books)
	_, items, err := l.Svc.BookAvailability(books[0].ID)
	if err != nil {
		return "查询馆藏出错：" + err.Error()
	}
	var itemID int64
	for _, it := range items {
		if it.Status == "available" {
			itemID = it.ID
			break
		}
	}
	if itemID == 0 {
		emitTool(emit, "place_hold", map[string]any{"patron_id": patron.ID, "book_id": books[0].ID})
		hold, err := l.Svc.PlaceHold(patron.ID, books[0].ID)
		if err != nil {
			return fmt.Sprintf("《%s》全部借出，预约失败：%s", books[0].Title, err.Error())
		}
		emitResult(emit, "place_hold", hold)
		return fmt.Sprintf("《%s》目前全部借出。我已为您预约排队（第 %d 位），归还后会通知您。", books[0].Title, hold.QueuePos)
	}
	emitTool(emit, "borrow_book", map[string]any{"patron_id": patron.ID, "item_id": itemID})
	loan, err := l.Svc.Borrow(patron.ID, itemID)
	if err != nil {
		return "借阅失败：" + err.Error()
	}
	emitResult(emit, "borrow_book", loan)
	return fmt.Sprintf("借阅成功！《%s》应还日期为 %s，请按时归还。", books[0].Title, loan.DueDate)
}

func (l *Loop) mockRenew(ctx context.Context, patron *store.Patron, emit func(Event)) string {
	emitTool(emit, "get_my_loans", map[string]any{"patron_id": patron.ID})
	loans, err := l.Svc.PatronLoans(patron.ID)
	if err != nil {
		return "查询出错：" + err.Error()
	}
	emitResult(emit, "get_my_loans", loans)
	if len(loans) == 0 {
		return "您当前没有在借图书，无需续借。"
	}
	for _, v := range loans {
		if v.Renewable {
			emitTool(emit, "renew_loan", map[string]any{"loan_id": v.ID})
			loan, err := l.Svc.Renew(v.ID)
			if err != nil {
				return "续借失败：" + err.Error()
			}
			emitResult(emit, "renew_loan", loan)
			return fmt.Sprintf("已为您续借《%s》，新的应还日期为 %s（本次为第 %d 次续借）。", v.Title, loan.DueDate, loan.Renewals)
		}
	}
	// 都没有可续借的
	msg := "您当前没有可续借的图书：\n"
	for _, v := range loans {
		msg += fmt.Sprintf("《%s》— %s\n", v.Title, v.RenewMsg)
	}
	return strings.TrimRight(msg, "\n")
}

func (l *Loop) mockReturn(ctx context.Context, patron *store.Patron, emit func(Event)) string {
	emitTool(emit, "get_my_loans", map[string]any{"patron_id": patron.ID})
	loans, err := l.Svc.PatronLoans(patron.ID)
	if err != nil {
		return "查询出错：" + err.Error()
	}
	emitResult(emit, "get_my_loans", loans)
	if len(loans) == 0 {
		return "您当前没有在借图书，无需归还。"
	}
	v := loans[0]
	emitTool(emit, "return_book", map[string]any{"loan_id": v.ID})
	res, err := l.Svc.Return(v.ID)
	if err != nil {
		return "归还失败：" + err.Error()
	}
	emitResult(emit, "return_book", res)
	out := fmt.Sprintf("《%s》已归还。", v.Title)
	if res.FineCents > 0 {
		out += fmt.Sprintf(" 因逾期，产生罚款 %.1f 元。", float64(res.FineCents)/100)
	}
	if res.HoldWakeUp != "" {
		out += " " + res.HoldWakeUp
	}
	return out
}

func (l *Loop) mockFines(patron *store.Patron, emit func(Event)) string {
	emitTool(emit, "get_my_fines", map[string]any{"patron_id": patron.ID})
	fines, err := l.Svc.Fines(patron.ID)
	if err != nil {
		return "查询出错：" + err.Error()
	}
	emitResult(emit, "get_my_fines", fines)
	if fines.UnpaidCents == 0 {
		return "您没有未缴罚款，保持良好的借阅记录！"
	}
	return fmt.Sprintf("您当前有未缴罚款 %.1f 元（共 %d 笔）。请尽快处理，以免影响借阅。", float64(fines.UnpaidCents)/100, len(fines.Items))
}

func (l *Loop) mockHold(ctx context.Context, patron *store.Patron, msg string, emit func(Event)) string {
	q := extractBookTitle(msg)
	if q == "" {
		return "请问您想预约哪本书？可以告诉我书名，例如「帮我预约《三体》」。"
	}
	emitTool(emit, "search_books", map[string]any{"q": q})
	books, err := l.Svc.SearchBooks(q, "", 1)
	if err != nil {
		return "查询出错：" + err.Error()
	}
	if len(books) == 0 {
		return "没有找到《" + q + "》这本书。"
	}
	emitResult(emit, "search_books", books)
	emitTool(emit, "place_hold", map[string]any{"patron_id": patron.ID, "book_id": books[0].ID})
	hold, err := l.Svc.PlaceHold(patron.ID, books[0].ID)
	if err != nil {
		return "预约失败：" + err.Error()
	}
	emitResult(emit, "place_hold", hold)
	return fmt.Sprintf("预约成功！《%s》当前排队第 %d 位，归还后会通知您到馆取书。", books[0].Title, hold.QueuePos)
}

// ---- 工具事件辅助 ----

func emitTool(emit func(Event), name string, args any) {
	argsJSON, _ := json.Marshal(args)
	emit(Event{Type: "tool_call", Data: map[string]any{
		"id": "mock_" + name, "name": name, "arguments": string(argsJSON), "mock": true,
	}})
}

func emitResult(emit func(Event), name string, result any) {
	emit(Event{Type: "tool_result", Data: map[string]any{
		"id": "mock_" + name, "name": name, "result": result, "mock": true,
	}})
}

var bookTitleRe = regexp.MustCompile(`《([^》]+)》`)

// extractBookTitle 提取用户消息中的书名（书名号内内容）。
func extractBookTitle(msg string) string {
	m := bookTitleRe.FindStringSubmatch(msg)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

// splitSentences 按标点分段，模拟打字机输出。
func splitSentences(s string) []string {
	seps := []string{"。", "！", "！", "\n", "；"}
	var parts []string
	cur := ""
	for _, r := range s {
		cur += string(r)
		for _, sep := range seps {
			if string(r) == sep {
				parts = append(parts, cur)
				cur = ""
				break
			}
		}
	}
	if cur != "" {
		parts = append(parts, cur)
	}
	return parts
}

var _ = service.MaxActiveLoans
