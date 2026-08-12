package service_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/shdadahui/library-agent/internal/service"
	"github.com/shdadahui/library-agent/internal/store"
)

// TestConcurrentCheckout 并发借同一副本：仅一个成功（原子 status 条件）。
func TestConcurrentCheckout(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	defer st.Close()
	bid, _ := st.InsertBiblio(&store.Biblio{Title: "并发测试书", Lang: "zh"})
	itemID, _ := st.InsertItem(&store.Item{BiblioID: bid, Barcode: "CC1", Status: "available"})
	p1, _ := st.InsertPatron(&store.Patron{Name: "读者甲", Barcode: "CA1"})
	p2, _ := st.InsertPatron(&store.Patron{Name: "读者乙", Barcode: "CA2"})

	svc := service.New(st)
	var wg sync.WaitGroup
	okCount := 0
	var mu sync.Mutex
	for _, pid := range []int64{p1, p2} {
		wg.Add(1)
		go func(pid int64) {
			defer wg.Done()
			_, err := svc.Borrow(pid, itemID)
			if err == nil {
				mu.Lock()
				okCount++
				mu.Unlock()
			}
		}(pid)
	}
	wg.Wait()
	if okCount != 1 {
		t.Fatalf("并发借同一副本应仅 1 人成功，实际 %d 人", okCount)
	}
}

// TestConcurrentReserveSeat 并发预约同一座位：仅一个成功（INSERT..WHERE NOT EXISTS 原子）。
func TestConcurrentReserveSeat(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	defer st.Close()
	seatID, _ := st.InsertSeat(&store.Seat{SeatNo: "X-101", Area: "测试区", SeatType: "普通", Status: "available"})
	p1, _ := st.InsertPatron(&store.Patron{Name: "读者甲", Barcode: "RA1"})
	p2, _ := st.InsertPatron(&store.Patron{Name: "读者乙", Barcode: "RA2"})

	svc := service.New(st)
	today := store.Now()
	var wg sync.WaitGroup
	okCount := 0
	var mu sync.Mutex
	for _, pid := range []int64{p1, p2} {
		wg.Add(1)
		go func(pid int64) {
			defer wg.Done()
			_, err := svc.ReserveSeat(pid, seatID, today, "afternoon")
			if err == nil {
				mu.Lock()
				okCount++
				mu.Unlock()
			}
		}(pid)
	}
	wg.Wait()
	if okCount != 1 {
		t.Fatalf("并发预约同一座位应仅 1 人成功，实际 %d 人", okCount)
	}
}

// TestReturnTxAtomic 还书事务：罚款与归还同时成功，且唤醒预约者。
func TestReturnTxAtomic(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	defer st.Close()
	bid, _ := st.InsertBiblio(&store.Biblio{Title: "逾期书", Lang: "zh"})
	itemID, _ := st.InsertItem(&store.Item{BiblioID: bid, Barcode: "RT1", Status: "available"})
	p1, _ := st.InsertPatron(&store.Patron{Name: "借书人", Barcode: "RA1"})
	p2, _ := st.InsertPatron(&store.Patron{Name: "预约人", Barcode: "RA2"})

	svc := service.New(st)
	// 借出：应还日 = 今天 - 5 天（逾期 5 天）
	today := store.Now()
	due := addDaysForTest(today, -5)
	loanID, err := st.Checkout(itemID, p1, addDaysForTest(today, -19), due)
	if err != nil {
		t.Fatalf("借出失败: %v", err)
	}
	// 预约人排队
	_, err = st.CreateHold(&store.Hold{BiblioID: bid, PatronID: p2, CreatedAt: store.Now()})
	if err != nil {
		t.Fatalf("创建预约失败: %v", err)
	}
	res, err := svc.Return(loanID)
	if err != nil {
		t.Fatalf("还书失败: %v", err)
	}
	if res.FineCents != 5*10 {
		t.Fatalf("逾期 5 天罚款应为 50 分，实际 %d", res.FineCents)
	}
	if res.HoldWakeUp == "" || !containsStr(res.HoldWakeUp, "预约人") {
		t.Fatalf("应唤醒预约人，实际: %q", res.HoldWakeUp)
	}
	// 副本状态
	it, _ := st.GetItem(itemID)
	if it.Status != "available" {
		t.Fatalf("还书后副本应 available，实际 %s", it.Status)
	}
	// 罚款落库
	fines, _ := st.Fines(p1, true)
	if len(fines) != 1 || fines[0].AmountCents != 50 {
		t.Fatalf("罚款记录异常: %+v", fines)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

var _ = fmt.Sprintf
