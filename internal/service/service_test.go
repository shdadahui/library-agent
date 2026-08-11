package service_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/shdadahui/library-agent/internal/service"
	"github.com/shdadahui/library-agent/internal/store"
)

// newTest 创建内存数据库 + 种子数据（1 本书 2 副本、2 位读者）。
func newTest(t *testing.T) (*service.Service, *store.Store) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	bid, _ := st.InsertBiblio(&store.Biblio{Title: "三体", Author: "刘慈欣", Lang: "zh"})
	for i := 1; i <= 2; i++ {
		_, _ = st.InsertItem(&store.Item{
			BiblioID: bid, Barcode: fmt.Sprintf("B%d", i), Status: "available", LoanDurationDays: 14,
		})
	}
	pid1, _ := st.InsertPatron(&store.Patron{Name: "张三", Barcode: "P1"})
	_, _ = st.InsertPatron(&store.Patron{Name: "李四", Barcode: "P2"})
	_ = pid1
	return service.New(st), st
}

func itemID(t *testing.T, st *store.Store, n int) int64 {
	t.Helper()
	items, err := st.ListItems(1)
	if err != nil || len(items) < n {
		t.Fatalf("取副本失败: %v", err)
	}
	return items[n-1].ID
}

func patronID(st *store.Store, name string) int64 {
	ps, _ := st.ListPatrons()
	for _, p := range ps {
		if p.Name == name {
			return p.ID
		}
	}
	return 0
}

// TestBorrowReturn 借出 → 副本状态变更；归还 → 恢复可借。
func TestBorrowReturn(t *testing.T) {
	svc, st := newTest(t)
	pid := patronID(st, "张三")
	iid := itemID(t, st, 1)

	loan, err := svc.Borrow(pid, iid)
	if err != nil {
		t.Fatalf("借书失败: %v", err)
	}
	if loan.DueDate == "" {
		t.Fatal("借阅记录缺少应还日期")
	}
	it, _ := st.GetItem(iid)
	if it.Status != "borrowed" {
		t.Fatalf("副本状态应为 borrowed，实际 %s", it.Status)
	}
	// 重复借同一副本应失败
	if _, err := svc.Borrow(pid, iid); !errors.Is(err, service.ErrItemUnavailable) {
		t.Fatalf("应拒绝重复借出，实际: %v", err)
	}
	// 归还
	res, err := svc.Return(loan.ID)
	if err != nil {
		t.Fatalf("还书失败: %v", err)
	}
	if res.FineCents != 0 {
		t.Fatalf("按期归还不应有罚款，实际 %d", res.FineCents)
	}
	it, _ = st.GetItem(iid)
	if it.Status != "available" {
		t.Fatalf("归还后副本应为 available，实际 %s", it.Status)
	}
}

// TestRenewLimit 续借最多 2 次，第 3 次拒绝。
func TestRenewLimit(t *testing.T) {
	svc, st := newTest(t)
	pid := patronID(st, "张三")
	iid := itemID(t, st, 1)
	loan, _ := svc.Borrow(pid, iid)

	for i := 1; i <= 2; i++ {
		next, err := svc.Renew(loan.ID)
		if err != nil {
			t.Fatalf("第 %d 次续借应成功: %v", i, err)
		}
		if next.Renewals != i {
			t.Fatalf("续借次数应为 %d，实际 %d", i, next.Renewals)
		}
		loan = next // 关旧开新后操作新记录
	}
	if _, err := svc.Renew(loan.ID); !errors.Is(err, service.ErrMaxRenewals) {
		t.Fatalf("第 3 次续借应被拒绝，实际: %v", err)
	}
}

// TestRenewOverdue 逾期记录不可续借。
func TestRenewOverdue(t *testing.T) {
	svc, st := newTest(t)
	pid := patronID(st, "张三")
	iid := itemID(t, st, 1)
	// 直接预置一条已逾期 3 天的记录
	due := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
	checkout := time.Now().AddDate(0, 0, -17).Format("2006-01-02")
	loanID, _ := st.Checkout(iid, pid, checkout, due)

	if _, err := svc.Renew(loanID); !errors.Is(err, service.ErrOverdue) {
		t.Fatalf("逾期续借应被拒绝，实际: %v", err)
	}
}

// TestRenewHoldPending 有预约排队时不可续借。
func TestRenewHoldPending(t *testing.T) {
	svc, st := newTest(t)
	pid := patronID(st, "张三")
	// 两个副本都借出，确保预约可成立
	loan1, _ := svc.Borrow(pid, itemID(t, st, 1))
	_, _ = svc.Borrow(pid, itemID(t, st, 2))
	// 李四预约
	if _, err := svc.PlaceHold(patronID(st, "李四"), 1); err != nil {
		t.Fatalf("预约失败: %v", err)
	}
	if _, err := svc.Renew(loan1.ID); !errors.Is(err, service.ErrHoldPending) {
		t.Fatalf("有预约时应拒绝续借，实际: %v", err)
	}
}

// TestFineCalculation 逾期罚款 = 天数 × 0.1 元。
func TestFineCalculation(t *testing.T) {
	svc, st := newTest(t)
	pid := patronID(st, "张三")
	iid := itemID(t, st, 1)
	checkout := time.Now().AddDate(0, 0, -20).Format("2006-01-02")
	due := time.Now().AddDate(0, 0, -5).Format("2006-01-02")
	loanID, _ := st.Checkout(iid, pid, checkout, due)

	res, err := svc.Return(loanID)
	if err != nil {
		t.Fatalf("还书失败: %v", err)
	}
	if res.FineCents != 5*service.FinePerDayCents {
		t.Fatalf("罚款应为 %d 分，实际 %d", 5*service.FinePerDayCents, res.FineCents)
	}
	fines, _ := st.Fines(pid, true)
	if len(fines) != 1 || fines[0].AmountCents != 50 {
		t.Fatalf("罚款记录异常: %+v", fines)
	}
}

// TestHoldFIFO 预约队列先进先出，还书唤醒最早预约者。
func TestHoldFIFO(t *testing.T) {
	svc, st := newTest(t)
	pid1 := patronID(st, "张三")
	pid2 := patronID(st, "李四")
	// 两个副本都借出
	loan1, _ := svc.Borrow(pid1, itemID(t, st, 1))
	loan2, _ := svc.Borrow(pid1, itemID(t, st, 2))
	// 张三、李四都预约（张三先）
	if _, err := svc.PlaceHold(pid1, 1); err != nil {
		t.Fatalf("预约失败: %v", err)
	}
	hold2, err := svc.PlaceHold(pid2, 1)
	if err != nil {
		t.Fatalf("预约失败: %v", err)
	}
	if hold2.QueuePos != 2 {
		t.Fatalf("李四应为队列第 2 位，实际 %d", hold2.QueuePos)
	}
	// 归还第一本 → 应唤醒队列第一（张三的预约）
	res, err := svc.Return(loan1.ID)
	if err != nil {
		t.Fatalf("还书失败: %v", err)
	}
	if res.HoldWakeUp == "" {
		t.Fatal("归还后应唤醒预约")
	}
	holds, _ := st.WaitingHolds(1)
	if len(holds) != 1 {
		t.Fatalf("应剩 1 个等待预约，实际 %d", len(holds))
	}
	// 归还第二本 → 唤醒李四
	res2, _ := svc.Return(loan2.ID)
	if res2.HoldWakeUp == "" {
		t.Fatal("第二次归还也应唤醒预约")
	}
}

// TestLoanLimit 同时借阅上限 5 本。
func TestLoanLimit(t *testing.T) {
	svc, st := newTest(t)
	pid := patronID(st, "张三")
	// 借满 5 本：副本 1 + 再插 4 本书各 1 副本
	if _, err := svc.Borrow(pid, itemID(t, st, 1)); err != nil {
		t.Fatalf("借第 1 本失败: %v", err)
	}
	for b := 2; b <= 5; b++ {
		bid, _ := st.InsertBiblio(&store.Biblio{Title: fmt.Sprintf("书%d", b), Author: "作者", Lang: "zh"})
		iid, _ := st.InsertItem(&store.Item{BiblioID: bid, Barcode: fmt.Sprintf("B2-%d", b), Status: "available", LoanDurationDays: 14})
		if _, err := svc.Borrow(pid, iid); err != nil {
			t.Fatalf("借第 %d 本失败: %v", b, err)
		}
	}
	// 第 6 本
	bid, _ := st.InsertBiblio(&store.Biblio{Title: "第六本", Author: "作者", Lang: "zh"})
	iid, _ := st.InsertItem(&store.Item{BiblioID: bid, Barcode: "B6", Status: "available", LoanDurationDays: 14})
	if _, err := svc.Borrow(pid, iid); !errors.Is(err, service.ErrLoanLimitReached) {
		t.Fatalf("借第 6 本应被拒绝，实际: %v", err)
	}
}

// TestPlaceHoldAvailable 有可借副本时预约应提示直接借阅。
func TestPlaceHoldAvailable(t *testing.T) {
	svc, st := newTest(t)
	pid := patronID(st, "张三")
	// 副本 1 可借
	if _, err := svc.PlaceHold(pid, 1); !errors.Is(err, service.ErrNoAvailableItem) {
		t.Fatalf("有可借副本时预约应报错，实际: %v", err)
	}
}
