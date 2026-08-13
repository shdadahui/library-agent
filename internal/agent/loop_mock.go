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
		case strings.Contains(msg, "在馆") || strings.Contains(msg, "馆里多少人") || strings.Contains(msg, "门禁"):
			out = l.mockGateStatus(patron, emit)
		case strings.Contains(msg, "进馆") || strings.Contains(msg, "入馆") || strings.Contains(msg, "扫码进"):
			out = l.mockGateScan(patron, "in", emit)
		case strings.Contains(msg, "出馆") || strings.Contains(msg, "离开图书馆"):
			out = l.mockGateScan(patron, "out", emit)
		// 座位相关（须在"预约/签到"等通用词之前，避免被书预约意图抢占）
		case strings.Contains(msg, "取消座位") || strings.Contains(msg, "退掉座位"):
			out = l.mockCancelSeat(ctx, patron, emit)
		case strings.Contains(msg, "签到") && strings.Contains(msg, "座"):
			out = l.mockCheckinSeat(ctx, patron, emit)
		case strings.Contains(msg, "预约座位") || strings.Contains(msg, "订座位") || strings.Contains(msg, "占个座") || strings.Contains(msg, "订自习") || (strings.Contains(msg, "座位") && strings.Contains(msg, "预约")):
			out = l.mockReserveSeat(ctx, patron, emit)
		case strings.Contains(msg, "座位") || strings.Contains(msg, "自习") || strings.Contains(msg, "空位") || strings.Contains(msg, "空座"):
			out = l.mockSeats(patron, emit)
		case strings.Contains(msg, "推荐") || strings.Contains(msg, "有什么好书") || strings.Contains(msg, "好看的书") || strings.Contains(msg, "喜欢") || strings.Contains(msg, "书单"):
			out = l.mockRecommend(patron, emit)
		case strings.Contains(msg, "续借"):
			out = l.mockRenew(ctx, patron, msg, emit)
		case strings.Contains(msg, "还书") || strings.Contains(msg, "归还") || strings.Contains(msg, "还了") || strings.Contains(msg, "还掉"):
			out = l.mockReturn(ctx, patron, msg, emit)
		case strings.Contains(msg, "罚款") || strings.Contains(msg, "欠费") || strings.Contains(msg, "逾期费") || strings.Contains(msg, "欠图书馆"):
			out = l.mockFines(patron, emit)
		case (strings.Contains(msg, "预约") || strings.Contains(msg, "排队")) && !strings.Contains(msg, "座位"):
			out = l.mockHold(ctx, patron, msg, emit)
		case strings.Contains(msg, "馆藏") || strings.Contains(msg, "可借") || strings.Contains(msg, "能借") || strings.Contains(msg, "有现书"):
			out = l.mockAvailability(ctx, patron, msg, emit)
		case strings.Contains(msg, "哪里") || strings.Contains(msg, "在哪") || strings.Contains(msg, "位置") || strings.Contains(msg, "几层") || strings.Contains(msg, "哪个区") || strings.Contains(msg, "哪个书架"):
			out = l.mockLocation(ctx, patron, msg, emit)
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

func (l *Loop) mockRenew(ctx context.Context, patron *store.Patron, msg string, emit func(Event)) string {
	emitTool(emit, "get_my_loans", map[string]any{"patron_id": patron.ID})
	loans, err := l.Svc.PatronLoans(patron.ID)
	if err != nil {
		return "查询出错：" + err.Error()
	}
	emitResult(emit, "get_my_loans", loans)
	if len(loans) == 0 {
		return "您当前没有在借图书，无需续借。"
	}
	// 用户指定了书名 → 只处理该书（尊重意图；逾期/不可续则明确拒绝）
	if title := extractBookTitle(msg); title != "" {
		for _, v := range loans {
			if strings.Contains(v.Title, title) {
				if v.Renewable {
					emitTool(emit, "renew_loan", map[string]any{"loan_id": v.ID})
					loan, err := l.Svc.Renew(v.ID)
					if err != nil {
						return "续借失败：" + err.Error()
					}
					emitResult(emit, "renew_loan", loan)
					return fmt.Sprintf("已为您续借《%s》，新的应还日期为 %s（本次为第 %d 次续借）。", v.Title, loan.DueDate, loan.Renewals)
				}
				return fmt.Sprintf("《%s》无法续借：%s", v.Title, v.RenewMsg)
			}
		}
		return fmt.Sprintf("您当前没有在借《%s》。", title)
	}
	// 未指定书名：续借第一本可续的
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
	msg2 := "您当前没有可续借的图书：\n"
	for _, v := range loans {
		msg2 += fmt.Sprintf("《%s》— %s\n", v.Title, v.RenewMsg)
	}
	return strings.TrimRight(msg2, "\n")
}

func (l *Loop) mockReturn(ctx context.Context, patron *store.Patron, msg string, emit func(Event)) string {
	emitTool(emit, "get_my_loans", map[string]any{"patron_id": patron.ID})
	loans, err := l.Svc.PatronLoans(patron.ID)
	if err != nil {
		return "查询出错：" + err.Error()
	}
	emitResult(emit, "get_my_loans", loans)
	if len(loans) == 0 {
		return "您当前没有在借图书，无需归还。"
	}
	// 用户指定了书名 → 归还指定那本（尊重意图）
	v := loans[0]
	if title := extractBookTitle(msg); title != "" {
		found := false
		for _, x := range loans {
			if strings.Contains(x.Title, title) {
				v = x
				found = true
				break
			}
		}
		if !found {
			return fmt.Sprintf("您当前没有在借《%s》。", title)
		}
	}
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

// ---- mock：座位预约 ----

func (l *Loop) mockSeats(patron *store.Patron, emit func(Event)) string {
	emitTool(emit, "search_seats", map[string]any{"slot": "afternoon"})
	seats, err := l.Svc.AvailableSeats(store.Now(), "afternoon")
	if err != nil {
		return "查询座位失败：" + err.Error()
	}
	emitResult(emit, "search_seats", map[string]any{"total": len(seats)})
	if len(seats) == 0 {
		return "今天下午暂时没有可预约的座位。"
	}
	byArea := map[string][]string{}
	for _, se := range seats {
		byArea[se.Area] = append(byArea[se.Area], se.SeatNo)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("今天下午可预约座位共 %d 个：\n", len(seats)))
	for area, nos := range byArea {
		shown := nos
		if len(shown) > 6 {
			shown = append(shown[:6], "…")
		}
		sb.WriteString(fmt.Sprintf("- %s：%s\n", area, strings.Join(shown, "、")))
	}
	sb.WriteString("需要我帮您预约一个吗？（告诉我偏好区域即可）")
	return strings.TrimSpace(sb.String())
}

func (l *Loop) mockReserveSeat(ctx context.Context, patron *store.Patron, emit func(Event)) string {
	emitTool(emit, "search_seats", map[string]any{"slot": "afternoon"})
	seats, err := l.Svc.AvailableSeats(store.Now(), "afternoon")
	if err != nil {
		return "查询座位失败：" + err.Error()
	}
	if len(seats) == 0 {
		return "今天下午暂无可用座位。"
	}
	se := seats[0]
	emitResult(emit, "search_seats", map[string]any{"total": len(seats)})
	emitTool(emit, "reserve_seat", map[string]any{"seat_id": se.ID, "slot": "afternoon"})
	r, err := l.Svc.ReserveSeat(patron.ID, se.ID, store.Now(), "afternoon")
	if err != nil {
		emitResult(emit, "reserve_seat", map[string]any{"error": err.Error()})
		return "预约失败：" + err.Error()
	}
	emitResult(emit, "reserve_seat", map[string]any{"reservation_id": r.ID, "seat_no": se.SeatNo})
	return fmt.Sprintf("已为您预约 %s 的 %s 号座位（下午 13:00-17:00），请按时到馆签到，逾时自动释放。", se.Area, se.SeatNo)
}

func (l *Loop) mockCancelSeat(ctx context.Context, patron *store.Patron, emit func(Event)) string {
	emitTool(emit, "get_my_seat_reservations", map[string]any{"patron_id": patron.ID})
	rs, err := l.Svc.MySeatReservations(patron.ID, true)
	if err != nil {
		return "查询预约失败：" + err.Error()
	}
	if len(rs) == 0 {
		emitResult(emit, "get_my_seat_reservations", map[string]any{"message": "无有效预约"})
		return "您当前没有可取消的座位预约。"
	}
	r := rs[0]
	emitResult(emit, "get_my_seat_reservations", rs)
	emitTool(emit, "cancel_seat_reservation", map[string]any{"reservation_id": r.ID})
	if err := l.Svc.CancelSeatReservation(patron.ID, r.ID); err != nil {
		return "取消失败：" + err.Error()
	}
	emitResult(emit, "cancel_seat_reservation", map[string]any{"ok": true})
	return fmt.Sprintf("已取消 %s（%s）的座位预约。", r.ReserveDate, r.SeatNo)
}

func (l *Loop) mockCheckinSeat(ctx context.Context, patron *store.Patron, emit func(Event)) string {
	emitTool(emit, "get_my_seat_reservations", map[string]any{"patron_id": patron.ID})
	rs, err := l.Svc.MySeatReservations(patron.ID, true)
	if err != nil {
		return "查询预约失败：" + err.Error()
	}
	if len(rs) == 0 {
		emitResult(emit, "get_my_seat_reservations", map[string]any{"message": "无有效预约"})
		return "您当前没有待签到的座位预约。"
	}
	r := rs[0]
	emitResult(emit, "get_my_seat_reservations", rs)
	emitTool(emit, "checkin_seat", map[string]any{"reservation_id": r.ID})
	nr, err := l.Svc.CheckinSeat(patron.ID, r.ID)
	if err != nil {
		return "签到失败：" + err.Error()
	}
	emitResult(emit, "checkin_seat", map[string]any{"ok": true, "status": nr.Status})
	return fmt.Sprintf("已为您在 %s（%s）签到成功，座位 %s 已占用。", r.Area, r.SeatNo, r.SeatNo)
}

// ---- mock：门禁 ----

func (l *Loop) mockGateScan(patron *store.Patron, direction string, emit func(Event)) string {
	emitTool(emit, "gate_scan", map[string]any{"direction": direction})
	res, err := l.Svc.GateScan(patron.ID, direction, "东门")
	if err != nil {
		emitResult(emit, "gate_scan", map[string]any{"error": err.Error()})
		return err.Error()
	}
	emitResult(emit, "gate_scan", res)
	action := "入馆"
	if direction == "out" {
		action = "出馆"
	}
	msg := fmt.Sprintf("✅ %s成功！%s（%s） 当前在馆 %d 人。", action, res.Patron, res.Gate, res.InLibrary)
	if len(res.Warnings) > 0 {
		msg += "\n⚠️ " + strings.Join(res.Warnings, "；") + "。"
	}
	return msg
}

func (l *Loop) mockGateStatus(patron *store.Patron, emit func(Event)) string {
	emitTool(emit, "gate_status", map[string]any{})
	st, err := l.Svc.GateStatus()
	if err != nil {
		return "查询门禁状态失败：" + err.Error()
	}
	emitResult(emit, "gate_status", st)
	return fmt.Sprintf("当前在馆 %d 人；今日入馆 %d 人次、出馆 %d 人次。", st.InLibrary, st.InToday, st.OutToday)
}

// mockLocation 查询图书所在位置（模拟真实 LLM：search → availability → 如实回答位置）。
// 防幻觉：位置仅显示系统记录值；未记录具体位置时如实说明，不编造楼层/书架。
func (l *Loop) mockLocation(ctx context.Context, patron *store.Patron, msg string, emit func(Event)) string {
	title := extractBookTitle(msg)
	if title == "" {
		return "请告诉我具体书名，例如「《百年孤独》在图书馆的哪里？」"
	}
	emitTool(emit, "search_books", map[string]any{"q": title})
	books, err := l.Svc.SearchBooks(title, "", 5)
	if err != nil || len(books) == 0 {
		return "没有找到《" + title + "》这本书。"
	}
	emitResult(emit, "search_books", books)
	b := books[0]
	emitTool(emit, "get_book_availability", map[string]any{"book_id": b.ID})
	_, items, err := l.Svc.BookAvailability(b.ID)
	if err != nil {
		return "查询馆藏出错：" + err.Error()
	}
	emitResult(emit, "get_book_availability", map[string]any{"book": b, "items": items})
	loc := ""
	for _, it := range items {
		if it.Location != "" {
			loc = it.Location
			break
		}
	}
	if loc == "" || loc == "总馆" {
		return fmt.Sprintf("据馆藏系统记录，《%s》位于总馆，系统未记录更具体的楼层或书架位置。", b.Title)
	}
	return fmt.Sprintf("据馆藏系统记录，《%s》位于「%s」。", b.Title, loc)
}
