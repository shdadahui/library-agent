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

	// 统一意图预过滤（闲聊/无关主题直接回复，不调工具）
	if reply, ok := l.preFilterReply(msg); ok {
		out = reply
	} else {
		switch {
		case strings.Contains(msg, "统计") || strings.Contains(msg, "多少藏书") || strings.Contains(msg, "藏书量") || strings.Contains(msg, "多少本") || strings.Contains(msg, "多少读者") || strings.Contains(msg, "借出多少"):
			out = l.mockStats(patron, emit)
		case strings.Contains(msg, "推荐") || strings.Contains(msg, "有什么好书") || strings.Contains(msg, "好看的书") || strings.Contains(msg, "喜欢") || strings.Contains(msg, "书单"):
			out = l.mockRecommend(patron, emit)
		case strings.Contains(msg, "续借"):
			out = l.mockRenew(ctx, patron, emit)
		case strings.Contains(msg, "还书") || strings.Contains(msg, "归还") || strings.Contains(msg, "还了") || strings.Contains(msg, "还掉"):
			out = l.mockReturn(ctx, patron, emit)
		case strings.Contains(msg, "罚款") || strings.Contains(msg, "欠费") || strings.Contains(msg, "逾期费") || strings.Contains(msg, "欠图书馆"):
			out = l.mockFines(patron, emit)
		case strings.Contains(msg, "预约") || strings.Contains(msg, "排队"):
			out = l.mockHold(ctx, patron, msg, emit)
		case strings.Contains(msg, "馆藏") || strings.Contains(msg, "可借") || strings.Contains(msg, "能借") || strings.Contains(msg, "有现书"):
			out = l.mockAvailability(ctx, patron, msg, emit)
		case strings.Contains(msg, "我借了") || strings.Contains(msg, "我借的") || strings.Contains(msg, "借阅") || strings.Contains(msg, "我借了什么"):
			out = l.mockLoans(patron, emit)
		case strings.Contains(msg, "借"):
			out = l.mockBorrow(ctx, patron, msg, emit)
		case strings.Contains(msg, "查") || strings.Contains(msg, "找") || strings.Contains(msg, "搜") || strings.Contains(msg, "有没有"):
			out = l.mockSearch(ctx, patron, msg, emit)
		case strings.Contains(msg, "到期") || strings.Contains(msg, "快还"):
			out = l.mockLoans(patron, emit)
		default:
			// 无明确业务动词：形如"有…的书吗/…方面的书"的问句 → 视为查书；否则引导用户
			if strings.Contains(msg, "的书") || strings.Contains(msg, "书吗") || strings.Contains(msg, "书？") || strings.Contains(msg, "有书") {
				out = l.mockSearch(ctx, patron, msg, emit)
			} else {
				out = "请问您想办理什么业务？我可以帮您：查书（如「帮我查一下《三体》」）、借书/预约、还书、续借、查询罚款或藏书统计。"
			}
		}
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
	emitTool(emit, "get_book_availability", map[string]any{"book_id": books[0].ID})
	_, items, err := l.Svc.BookAvailability(books[0].ID)
	if err != nil {
		return "查询馆藏出错：" + err.Error()
	}
	emitResult(emit, "get_book_availability", items)
	var availBarcode, availLoc string
	allBorrowed := true
	for _, it := range items {
		if it.Status == "available" {
			allBorrowed = false
			if availBarcode == "" {
				availBarcode, availLoc = it.Barcode, it.Location
			}
		}
	}
	// 有可借副本 → 引导到馆借阅（本馆规定线上不能直接借出）
	if !allBorrowed {
		emitTool(emit, "guide_borrow", map[string]any{"book_id": books[0].ID})
		guide, err := l.guideBorrow(books[0].ID)
		if err != nil {
			return "查询出错：" + err.Error()
		}
		emitResult(emit, "guide_borrow", guide)
		_ = availBarcode
		return fmt.Sprintf("《%s》目前有可借副本（馆藏位置：%s）。本馆规定借书须到馆办理：请凭读者证在自助借还机或服务台完成借阅手续。", books[0].Title, availLoc)
	}
	// 全部借出 → 预约
	emitTool(emit, "place_hold", map[string]any{"patron_id": patron.ID, "book_id": books[0].ID})
	hold, err := l.Svc.PlaceHold(patron.ID, books[0].ID)
	if err != nil {
		return fmt.Sprintf("《%s》全部借出，预约失败：%s", books[0].Title, err.Error())
	}
	emitResult(emit, "place_hold", hold)
	return fmt.Sprintf("《%s》目前全部借出。我已为您预约排队（第 %d 位），归还后会通知您到馆取书。", books[0].Title, hold.QueuePos)
}

// guideBorrow 返回到馆借阅引导信息（不执行借出）。
func (l *Loop) guideBorrow(bookID int64) (map[string]any, error) {
	b, items, err := l.Svc.BookAvailability(bookID)
	if err != nil {
		return nil, err
	}
	avail := []map[string]any{}
	for _, it := range items {
		if it.Status == "available" {
			avail = append(avail, map[string]any{"barcode": it.Barcode, "location": it.Location})
		}
	}
	return map[string]any{"book": b, "available_items": avail, "guide": "请凭读者证到馆，在自助借还机或服务台办理借阅手续。"}, nil
}

// mockStats 全馆统计。
func (l *Loop) mockStats(patron *store.Patron, emit func(Event)) string {
	emitTool(emit, "get_library_stats", map[string]any{})
	st, err := l.Svc.LibraryStats()
	if err != nil {
		return "查询出错：" + err.Error()
	}
	emitResult(emit, "get_library_stats", st)
	return fmt.Sprintf("本馆现有藏书 %d 种、馆藏副本 %d 本（可借 %d 本、在借 %d 本），等待预约 %d 个，注册读者 %d 位，全馆未缴罚款合计 %.1f 元。",
		st.Books, st.Copies, st.Available, st.Borrowed, st.HoldsWaiting, st.Patrons, float64(st.UnpaidFinesCents)/100)
}

// mockRecommend 智能推荐（基于借阅历史，无历史时热门兜底）。
func (l *Loop) mockRecommend(patron *store.Patron, emit func(Event)) string {
	emitTool(emit, "recommend_books", map[string]any{"patron_id": patron.ID})
	recs, err := l.Svc.RecommendForPatron(patron.ID, "", 5)
	if err != nil {
		return "查询出错：" + err.Error()
	}
	emitResult(emit, "recommend_books", recs)
	if len(recs) == 0 {
		return "暂无可推荐的图书，建议先多借阅几本，我会根据您的阅读偏好推荐。"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s，为您推荐以下图书：\n", patron.Name))
	for i, r := range recs {
		avail := "有可借副本"
		if r.Available == 0 {
			avail = "暂无可借副本"
		}
		why := ""
		if len(r.Reasons) > 0 {
			n := 2
			if len(r.Reasons) < n {
				n = len(r.Reasons)
			}
			why = "（" + strings.Join(r.Reasons[:n], "、") + "）"
		}
		sb.WriteString(fmt.Sprintf("%d. 《%s》 %s（%d 年）%s%s\n", i+1, r.Title, r.Author, r.PublishYear, avail, why))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// mockAvailability 查询馆藏：先检索书目，再查各副本状态。
func (l *Loop) mockAvailability(ctx context.Context, patron *store.Patron, msg string, emit func(Event)) string {
	q := extractBookTitle(msg)
	if q == "" {
		q = strings.Trim(strings.TrimSpace(msg), "？?。，,！!")
	}
	if q == "" {
		return "请问您想查询哪本书的馆藏？可以告诉我书名，例如「《三体》有可借的吗」。"
	}
	emitTool(emit, "search_books", map[string]any{"q": q})
	books, err := l.Svc.SearchBooks(q, "", 5)
	if err != nil {
		return "查询出错：" + err.Error()
	}
	emitResult(emit, "search_books", books)
	if len(books) == 0 {
		return "没有找到《" + q + "》这本书。"
	}
	var sb strings.Builder
	for _, b := range books {
		sb.WriteString(fmt.Sprintf("《%s》 %s（书目ID %d）\n", b.Title, b.Author, b.ID))
		emitTool(emit, "get_book_availability", map[string]any{"book_id": b.ID})
		bk, items, err := l.Svc.BookAvailability(b.ID)
		if err != nil {
			sb.WriteString("  查询失败\n")
			continue
		}
		emitResult(emit, "get_book_availability", items)
		_ = bk
		for _, it := range items {
			switch it.Status {
			case "available":
				sb.WriteString(fmt.Sprintf("  · 副本 %s：可借（%s）\n", it.Barcode, it.Location))
			case "borrowed":
				sb.WriteString(fmt.Sprintf("  · 副本 %s：借出中（应还 %s）\n", it.Barcode, it.DueDate))
			default:
				sb.WriteString(fmt.Sprintf("  · 副本 %s：%s\n", it.Barcode, it.Status))
			}
		}
		if len(items) == 0 {
			sb.WriteString("  · 暂无馆藏副本\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
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
