package service_test

import (
	"testing"
	"time"

	"github.com/shdadahui/library-agent/internal/service"
)

func addDaysForTest(date string, n int) string {
	t, _ := time.Parse("2006-01-02", date)
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

func TestGateScanInOut(t *testing.T) {
	svc, _, p1, _ := seedSeatTest(t)
	res, err := svc.GateScan(p1, "in", "东门")
	if err != nil {
		t.Fatalf("入馆失败: %v", err)
	}
	if res.Direction != "in" || res.InLibrary < 1 {
		t.Fatalf("入馆结果错误: %+v", res)
	}
	// 重复入馆 → 拒绝
	if _, err := svc.GateScan(p1, "in", "东门"); err != service.ErrGateAlreadyIn {
		t.Fatalf("应报已在馆，实际: %v", err)
	}
	// 出馆
	res2, err := svc.GateScan(p1, "out", "东门")
	if err != nil {
		t.Fatalf("出馆失败: %v", err)
	}
	if res2.Direction != "out" {
		t.Fatalf("出馆结果错误: %+v", res2)
	}
}

func TestGateScanOutWithoutIn(t *testing.T) {
	svc, _, _, p2 := seedSeatTest(t)
	if _, err := svc.GateScan(p2, "out", "东门"); err != service.ErrGateNotIn {
		t.Fatalf("未入馆直接出馆应报错，实际: %v", err)
	}
}

func TestGateStatus(t *testing.T) {
	svc, _, p1, p2 := seedSeatTest(t)
	_, _ = svc.GateScan(p1, "in", "东门")
	_, _ = svc.GateScan(p2, "in", "西门")
	st, err := svc.GateStatus()
	if err != nil {
		t.Fatalf("门禁状态失败: %v", err)
	}
	if st.InLibrary < 2 {
		t.Fatalf("在馆人数应为 2，实际 %d", st.InLibrary)
	}
	if st.InToday < 2 {
		t.Fatalf("今日入馆应为 2，实际 %d", st.InToday)
	}
	if len(st.Recent) < 2 {
		t.Fatalf("最近记录应含 2 条，实际 %d", len(st.Recent))
	}
}
