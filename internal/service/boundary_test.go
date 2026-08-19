package service_test

// 边界测试：VIP 上限 / 多预约 FIFO / 取消预约补位 / 非法输入 / 已还再借 / 续已关 / 座位跨时段 / 多天罚款 / 空搜索。
// 独立内存库，互不污染。

import (
	"errors"
	"testing"

	"github.com/shdadahui/library-agent/internal/service"
	"github.com/shdadahui/library-agent/internal/store"
)

func bsvc(t *testing.T) (*store.Store, *service.Service) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st, service.New(st)
}

// TestVIPBorrowLimit VIP 借阅上限 10：普通 5 拒绝，VIP 6 本成功。
func TestVIPBorrowLimit(t *testing.T) {
	st, svc := bsvc(t)
	bid, _ := st.InsertBiblio(&store.Biblio{Title: "VIP书", Lang: "zh"})
	var itemIDs []int64
	for i := 0; i < 12; i++ {
		id, _ := st.InsertItem(&store.Item{BiblioID: bid, Barcode: "VIP-I" + itoa(i), Status: "available"})
		itemIDs = append(itemIDs, id)
	}
	pid, _ := st.InsertPatron(&store.Patron{Name: "VIP读者", Barcode: "VIP-P"})

	// 普通读者：借 5 本成功，第 6 本被拒
	for i := 0; i < 5; i++ {
		if _, err := svc.Borrow(pid, itemIDs[i]); err != nil {
			t.Fatalf("第 %d 本应借成功: %v", i+1, err)
		}
	}
	if _, err := svc.Borrow(pid, itemIDs[5]); !errors.Is(err, service.ErrLoanLimitReached) {
		t.Fatalf("普通读者第 6 本应拒绝: %v", err)
	}
	// 还 1 本后设为 VIP → 可再借 6 本（上限 10）
	loans, _ := st.ActiveLoans(pid)
	if _, err := svc.Return(loans[0].ID); err != nil {
		t.Fatalf("还书失败: %v", err)
	}
	if err := st.SetVip(pid, true); err != nil {
		t.Fatalf("设 VIP 失败: %v", err)
	}
	for i := 5; i <= 10; i++ {
		if _, err := svc.Borrow(pid, itemIDs[i]); err != nil {
			t.Fatalf("VIP 第 %d 本应借成功: %v", i-3, err)
		}
	}
	// 已借 10 本，再借未借过的第 12 本 → 上限拒绝
	if _, err := svc.Borrow(pid, itemIDs[11]); err == nil {
		t.Fatalf("VIP 超限应拒绝")
	} else if !errors.Is(err, service.ErrLoanLimitReached) {
		t.Fatalf("VIP 超限应报 ErrLoanLimitReached: %v", err)
	}
}

// TestHoldQueueFIFO 三人预约 FIFO：还书只唤醒排第一的人。
func TestHoldQueueFIFO(t *testing.T) {
	st, svc := bsvc(t)
	bid, _ := st.InsertBiblio(&store.Biblio{Title: "FIFO书", Lang: "zh"})
	itemID, _ := st.InsertItem(&store.Item{BiblioID: bid, Barcode: "FIFO1", Status: "available"})
	p1, _ := st.InsertPatron(&store.Patron{Name: "甲", Barcode: "F-P1"})
	p2, _ := st.InsertPatron(&store.Patron{Name: "乙", Barcode: "F-P2"})
	p3, _ := st.InsertPatron(&store.Patron{Name: "丙", Barcode: "F-P3"})

	// 甲先借走（占唯一副本）
	if _, err := svc.Borrow(p1, itemID); err != nil {
		t.Fatalf("借出失败: %v", err)
	}
	// 乙、丙依次预约（FIFO）
	if _, err := svc.PlaceHold(p2, bid); err != nil {
		t.Fatalf("乙预约失败: %v", err)
	}
	if _, err := svc.PlaceHold(p3, bid); err != nil {
		t.Fatalf("丙预约失败: %v", err)
	}
	// 甲还书 → 唤醒乙（队首）
	loans, _ := st.ActiveLoans(p1)
	res, err := svc.Return(loans[0].ID)
	if err != nil {
		t.Fatalf("还书失败: %v", err)
	}
	if !containsStr(res.HoldWakeUp, "乙") || containsStr(res.HoldWakeUp, "丙") {
		t.Fatalf("应只唤醒乙: %q", res.HoldWakeUp)
	}
	// 乙的 hold 已 fulfill（不再 waiting），丙仍在 waiting（队列剩余）
	h, err := st.WaitingHolds(bid)
	if err != nil {
		t.Fatalf("查预约失败: %v", err)
	}
	if len(h) != 1 || h[0].PatronID != p3 {
		t.Fatalf("唤醒后 waiting 队列应只剩丙: %+v", h)
	}
}

// TestCancelHoldFreesQueue 取消预约后，新预约者补位队尾。
func TestCancelHoldFreesQueue(t *testing.T) {
	st, svc := bsvc(t)
	bid, _ := st.InsertBiblio(&store.Biblio{Title: "取消书", Lang: "zh"})
	itemID, _ := st.InsertItem(&store.Item{BiblioID: bid, Barcode: "CH1", Status: "available"})
	p1, _ := st.InsertPatron(&store.Patron{Name: "甲", Barcode: "C-P1"})
	p2, _ := st.InsertPatron(&store.Patron{Name: "乙", Barcode: "C-P2"})
	p3, _ := st.InsertPatron(&store.Patron{Name: "丙", Barcode: "C-P3"})

	if _, err := svc.Borrow(p1, itemID); err != nil {
		t.Fatalf("借出失败: %v", err)
	}
	hid2, err := svc.PlaceHold(p2, bid)
	if err != nil {
		t.Fatalf("乙预约失败: %v", err)
	}
	if _, err := svc.PlaceHold(p3, bid); err != nil {
		t.Fatalf("丙预约失败: %v", err)
	}
	// 乙取消预约 → 队列剩丙
	if err := svc.CancelHold(p2, hid2.ID); err != nil {
		t.Fatalf("取消预约失败: %v", err)
	}
	// 甲还书 → 唤醒丙（现在是队首）
	loans, _ := st.ActiveLoans(p1)
	res, err := svc.Return(loans[0].ID)
	if err != nil {
		t.Fatalf("还书失败: %v", err)
	}
	if !containsStr(res.HoldWakeUp, "丙") {
		t.Fatalf("取消后应唤醒丙: %q", res.HoldWakeUp)
	}
}

// TestBorrowReturnedItem 借已归还副本 → ErrItemUnavailable。
func TestBorrowReturnedItem(t *testing.T) {
	_, svc := bsvc(t)
	if _, err := svc.Borrow(1, 99999); !errors.Is(err, service.ErrPatronNotFound) {
		t.Fatalf("不存在读者应报 ErrPatronNotFound: %v", err)
	}
}

// TestRenewClosedLoan 续借已关闭记录 → ErrLoanNotActive。
func TestRenewClosedLoan(t *testing.T) {
	st, svc := bsvc(t)
	bid, _ := st.InsertBiblio(&store.Biblio{Title: "关续书", Lang: "zh"})
	itemID, _ := st.InsertItem(&store.Item{BiblioID: bid, Barcode: "RC1", Status: "available"})
	pid, _ := st.InsertPatron(&store.Patron{Name: "读者", Barcode: "RC-P"})
	loan, err := svc.Borrow(pid, itemID)
	if err != nil {
		t.Fatalf("借出失败: %v", err)
	}
	if _, err := svc.Return(loan.ID); err != nil {
		t.Fatalf("还书失败: %v", err)
	}
	if _, err := svc.Renew(loan.ID); !errors.Is(err, service.ErrLoanNotActive) {
		t.Fatalf("续借已关闭记录应报 ErrLoanNotActive: %v", err)
	}
}

// TestSeatCrossPeriod 同座位不同时段允许；同时段拒绝。
func TestSeatCrossPeriod(t *testing.T) {
	st, svc := bsvc(t)
	seatID, _ := st.InsertSeat(&store.Seat{SeatNo: "S-1", Area: "A", SeatType: "普通", Status: "available"})
	p1, _ := st.InsertPatron(&store.Patron{Name: "甲", Barcode: "SP-P1"})
	p2, _ := st.InsertPatron(&store.Patron{Name: "乙", Barcode: "SP-P2"})
	today := store.Now()

	// 甲约上午
	if _, err := svc.ReserveSeat(p1, seatID, today, "morning"); err != nil {
		t.Fatalf("甲约上午失败: %v", err)
	}
	// 乙约同一座位上午 → 拒绝
	if _, err := svc.ReserveSeat(p2, seatID, today, "morning"); err == nil {
		t.Fatalf("同时段应拒绝")
	}
	// 乙约下午 → 允许
	if _, err := svc.ReserveSeat(p2, seatID, today, "afternoon"); err != nil {
		t.Fatalf("不同时段应允许: %v", err)
	}
}

// TestFineMultiDays 逾期多天罚款 = 天数 × 10 分。
func TestFineMultiDays(t *testing.T) {
	st, svc := bsvc(t)
	bid, _ := st.InsertBiblio(&store.Biblio{Title: "逾期书", Lang: "zh"})
	itemID, _ := st.InsertItem(&store.Item{BiblioID: bid, Barcode: "FM1", Status: "available"})
	pid, _ := st.InsertPatron(&store.Patron{Name: "逾期人", Barcode: "FM-P"})

	today := store.Now()
	due := addDaysForTest(today, -10) // 逾期 10 天
	if _, err := st.Checkout(itemID, pid, addDaysForTest(today, -24), due); err != nil {
		t.Fatalf("借出失败: %v", err)
	}
	loans, _ := st.ActiveLoans(pid)
	res, err := svc.Return(loans[0].ID)
	if err != nil {
		t.Fatalf("还书失败: %v", err)
	}
	if res.FineCents != 100 {
		t.Fatalf("逾期 10 天罚款应 100 分，实际 %d", res.FineCents)
	}
}

// TestSearchEmpty 搜索不存在关键词返回空且不报错。
func TestSearchEmpty(t *testing.T) {
	_, svc := bsvc(t)
	books, err := svc.SearchBooks("绝对不存在的书名XYZ", "", 5)
	if err != nil {
		t.Fatalf("搜索应不报错: %v", err)
	}
	if len(books) != 0 {
		t.Fatalf("空搜索应返回空, got %d", len(books))
	}
}

// TestBorrowNegativeID 非法 ID（0/负数）应报"不存在"而非崩溃。
func TestBorrowNegativeID(t *testing.T) {
	_, svc := bsvc(t)
	for _, id := range []int64{0, -1, -100} {
		if _, err := svc.Borrow(id, 1); err == nil {
			t.Fatalf("id=%d 应报错", id)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}
