package service_test

import (
	"sync"
	"testing"

	"github.com/shdadahui/library-agent/internal/service"
	"github.com/shdadahui/library-agent/internal/store"
)

func setupLoanForRenew(t *testing.T) (*store.Store, *service.Service, int64) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	bid, _ := st.InsertBiblio(&store.Biblio{Title: "续借并发书", Lang: "zh"})
	itemID, _ := st.InsertItem(&store.Item{BiblioID: bid, Barcode: "RN1", Status: "available", LoanDurationDays: 14})
	pid, _ := st.InsertPatron(&store.Patron{Name: "续借人", Barcode: "RN-P"})
	svc := service.New(st)
	loan, err := svc.Borrow(pid, itemID)
	if err != nil {
		t.Fatalf("借出失败: %v", err)
	}
	return st, svc, loan.ID
}

// TestConcurrentRenew 并发续借同一笔借阅：应只成功一次（CAS 原子），
// 不能出现重复创建多条 active loan（续借语义=旧记录结清+新记录一条）。
func TestConcurrentRenew(t *testing.T) {
	st, svc, loanID := setupLoanForRenew(t)

	const n = 8
	var wg sync.WaitGroup
	okCount := 0
	var mu sync.Mutex
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.Renew(loanID); err == nil {
				mu.Lock()
				okCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if okCount != 1 {
		t.Fatalf("并发续借应仅 1 次成功，实际 %d 次", okCount)
	}
	// 该副本当前应只有 1 条 active loan
	loans, err := st.ActiveLoans(1)
	if err != nil || len(loans) != 1 {
		t.Fatalf("应仅 1 条 active loan, got %d err=%v", len(loans), err)
	}
	// 续借后的 loan renewals=1
	if loans[0].Renewals != 1 {
		t.Fatalf("续借后 renewals 应 1, got %d", loans[0].Renewals)
	}
}

// TestConcurrentReturn 并发归还同一笔：仅一次成功（其余应报"不在借"）。
func TestConcurrentReturn(t *testing.T) {
	st, svc, loanID := setupLoanForRenew(t)
	_ = st
	const n = 6
	var wg sync.WaitGroup
	okCount := 0
	var mu sync.Mutex
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.Return(loanID); err == nil {
				mu.Lock()
				okCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if okCount != 1 {
		t.Fatalf("并发归还应仅 1 次成功，实际 %d 次", okCount)
	}
	it, _ := st.GetItem(1)
	if it.Status != "available" {
		t.Fatalf("归还后副本应 available, got %s", it.Status)
	}
}

// TestConcurrentFavorite 并发收藏同一书：幂等，仅 1 条记录。
func TestConcurrentFavorite(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	bid, _ := st.InsertBiblio(&store.Biblio{Title: "收藏并发书", Lang: "zh"})
	pid, _ := st.InsertPatron(&store.Patron{Name: "收藏人", Barcode: "FA-P"})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = st.AddFavorite(pid, bid)
		}()
	}
	wg.Wait()
	var n int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM favorites WHERE patron_id=? AND biblio_id=?`, pid, bid).Scan(&n)
	if n != 1 {
		t.Fatalf("并发收藏应仅 1 条，实际 %d", n)
	}
}

// TestConcurrentRateBook 并发评分：不崩溃，最终 1 条记录。
func TestConcurrentRateBook(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	bid, _ := st.InsertBiblio(&store.Biblio{Title: "评分并发书", Lang: "zh"})
	pid, _ := st.InsertPatron(&store.Patron{Name: "评分人", Barcode: "RA-P"})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(score int) {
			defer wg.Done()
			_ = st.RateBook(pid, bid, score)
		}(i%5 + 1)
	}
	wg.Wait()
	var n int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM ratings WHERE patron_id=? AND biblio_id=?`, pid, bid).Scan(&n)
	if n != 1 {
		t.Fatalf("并发评分应仅 1 条，实际 %d", n)
	}
}

// TestConcurrentLoanLimit 并发借阅上限：同一读者 30 并发借 30 本不同书，
// 上限 5 必须严格生效（读者级互斥，防超额借出）。
func TestConcurrentLoanLimit(t *testing.T) {
	st, _ := store.Open(":memory:")
	t.Cleanup(func() { st.Close() })
	bid, _ := st.InsertBiblio(&store.Biblio{Title: "上限并发书", Lang: "zh"})
	var itemIDs []int64
	for i := 0; i < 30; i++ {
		id, _ := st.InsertItem(&store.Item{BiblioID: bid, Barcode: "LM-I" + itoa(i), Status: "available"})
		itemIDs = append(itemIDs, id)
	}
	pid, _ := st.InsertPatron(&store.Patron{Name: "上限读者", Barcode: "LM-P"})
	svc := service.New(st)

	var wg sync.WaitGroup
	var mu sync.Mutex
	okCount := 0
	for _, it := range itemIDs {
		wg.Add(1)
		go func(it int64) {
			defer wg.Done()
			if _, err := svc.Borrow(pid, it); err == nil {
				mu.Lock()
				okCount++
				mu.Unlock()
			}
		}(it)
	}
	wg.Wait()
	if okCount != 5 {
		t.Fatalf("30 并发借阅应严格限制 5 本，实际借出 %d 本", okCount)
	}
	loans, _ := st.ActiveLoans(pid)
	if len(loans) != 5 {
		t.Fatalf("库内 active loans 应 5，实际 %d", len(loans))
	}
}
