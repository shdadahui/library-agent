package service_test

import (
	"fmt"
	"testing"

	"github.com/shdadahui/library-agent/internal/service"
	"github.com/shdadahui/library-agent/internal/store"
)

// seedRecommend 插入推荐测试数据：3 本不同主题的书 + 1 位读者。
func seedRecommend(t *testing.T) (*service.Service, *store.Store) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	books := []store.Biblio{
		{Title: "三体", Author: "刘慈欣", Subjects: "科幻,小说", Lang: "zh"},
		{Title: "数学：确定性的丧失", Author: "莫里斯·克莱因", Subjects: "数学,科普", Lang: "zh"},
		{Title: "活着", Author: "余华", Subjects: "小说,文学", Lang: "zh"},
		{Title: "银河帝国", Author: "阿西莫夫", Subjects: "科幻,小说", Lang: "zh"},
	}
	for _, b := range books {
		id, err := st.InsertBiblio(&b)
		if err != nil {
			t.Fatalf("插书失败: %v", err)
		}
		_, _ = st.InsertItem(&store.Item{BiblioID: id, Barcode: fmt.Sprintf("R%d", id), Status: "available", LoanDurationDays: 14})
	}
	pid, _ := st.InsertPatron(&store.Patron{Name: "测试读者", Barcode: "RP1"})
	_ = pid
	return service.New(st), st
}

// TestRecommendByTaste 主题推荐：taste=科幻 应命中科幻书。
func TestRecommendByTaste(t *testing.T) {
	svc, st := seedRecommend(t)
	recs, err := svc.RecommendForPatron(0, "科幻", 5)
	if err != nil {
		t.Fatalf("推荐失败: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("应返回推荐结果")
	}
	found := false
	for _, r := range recs {
		if r.Title == "三体" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("主题推荐应包含《三体》，实际 %+v", recs)
	}
	_ = st
}

// TestRecommendPersonalized 个性化推荐：借过科幻书 → 推荐含其他科幻书。
func TestRecommendPersonalized(t *testing.T) {
	svc, st := seedRecommend(t)
	// 测试读者借了《三体》
	items, _ := st.ListItems(1) // 三体
	if len(items) == 0 {
		t.Fatal("无副本")
	}
	pid, _ := st.InsertPatron(&store.Patron{Name: "读者A", Barcode: "RP2"})
	_, _ = st.Checkout(items[0].ID, pid, "2026-07-01", "2026-07-15")
	_ = st.Checkin(1, "2026-07-14") // 归还，形成历史
	// 再用另一个读者测试（避免排除已借）
	pid2, _ := st.InsertPatron(&store.Patron{Name: "读者B", Barcode: "RP3"})
	items1, _ := st.ListItems(1)
	_, _ = st.Checkout(items1[0].ID, pid2, "2026-07-01", "2026-07-15")
	_ = st.Checkin(2, "2026-07-14") // 读者B 也借过三体（历史）

	recs, err := svc.RecommendForPatron(pid2, "", 5)
	if err != nil {
		t.Fatalf("推荐失败: %v", err)
	}
	foundSciFi := false
	for _, r := range recs {
		if r.Title == "银河帝国" { // 同为科幻
			foundSciFi = true
		}
		if r.ID == 1 {
			t.Fatalf("不应推荐读者已借过的书")
		}
	}
	if !foundSciFi {
		t.Fatalf("个性化推荐应包含同主题《银河帝国》，实际 %+v", recs)
	}
}

// TestRecommendHotFallback 无历史无 taste → 热门兜底（应仍返回结果）。
func TestRecommendHotFallback(t *testing.T) {
	svc, st := seedRecommend(t)
	recs, err := svc.RecommendForPatron(0, "", 3)
	if err != nil {
		t.Fatalf("推荐失败: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("热门兜底应返回结果")
	}
	_ = st
}
