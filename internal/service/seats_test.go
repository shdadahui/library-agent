package service_test

import (
	"testing"

	"github.com/shdadahui/library-agent/internal/service"
	"github.com/shdadahui/library-agent/internal/store"
)

// seedSeatTest 造 2 个座位 + 2 位读者。
func seedSeatTest(t *testing.T) (*service.Service, *store.Store, int64, int64) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	_, _ = st.InsertSeat(&store.Seat{SeatNo: "3-101", Area: "3F 阅览区", SeatType: "带插座", Status: "available", RowPos: 1, ColPos: 1})
	_, _ = st.InsertSeat(&store.Seat{SeatNo: "2-101", Area: "2F 自习区", SeatType: "普通", Status: "available", RowPos: 1, ColPos: 1})
	p1, _ := st.InsertPatron(&store.Patron{Name: "张三", Barcode: "S1"})
	p2, _ := st.InsertPatron(&store.Patron{Name: "李四", Barcode: "S2"})
	return service.New(st), st, p1, p2
}

func TestReserveSeatBasic(t *testing.T) {
	svc, _, p1, _ := seedSeatTest(t)
	r, err := svc.ReserveSeat(p1, 1, store.Now(), "afternoon")
	if err != nil {
		t.Fatalf("预约失败: %v", err)
	}
	if r.Status != "active" || r.SeatID != 1 {
		t.Fatalf("预约状态错误: %+v", r)
	}
}

func TestReserveSeatQuota(t *testing.T) {
	svc, _, p1, _ := seedSeatTest(t)
	if _, err := svc.ReserveSeat(p1, 1, store.Now(), "afternoon"); err != nil {
		t.Fatalf("首次预约失败: %v", err)
	}
	// 同日再约第二个座位 → quota
	if _, err := svc.ReserveSeat(p1, 2, store.Now(), "evening"); err != service.ErrSeatQuotaReached {
		t.Fatalf("应报 quota 错误，实际: %v", err)
	}
}

func TestReserveSeatConflict(t *testing.T) {
	svc, _, p1, p2 := seedSeatTest(t)
	if _, err := svc.ReserveSeat(p1, 1, store.Now(), "afternoon"); err != nil {
		t.Fatalf("首次预约失败: %v", err)
	}
	// 另一读者约同一座位同时段 → 冲突
	if _, err := svc.ReserveSeat(p2, 1, store.Now(), "afternoon"); err != service.ErrSeatAlreadyReserved {
		t.Fatalf("应报冲突错误，实际: %v", err)
	}
	// 但不同时段可约
	if _, err := svc.ReserveSeat(p2, 1, store.Now(), "evening"); err != nil {
		t.Fatalf("不同时段应可约: %v", err)
	}
}

func TestCancelSeatReservation(t *testing.T) {
	svc, _, p1, _ := seedSeatTest(t)
	r, _ := svc.ReserveSeat(p1, 1, store.Now(), "afternoon")
	if err := svc.CancelSeatReservation(p1, r.ID); err != nil {
		t.Fatalf("取消失败: %v", err)
	}
	// 重复取消 → 已关闭
	if err := svc.CancelSeatReservation(p1, r.ID); err != service.ErrSeatReservationClosed {
		t.Fatalf("应报已关闭，实际: %v", err)
	}
	// 非本人 → 拒绝
	r2, _ := svc.ReserveSeat(p1, 2, store.Now(), "evening")
	if err := svc.CancelSeatReservation(999, r2.ID); err != service.ErrSeatReservationNotMine {
		t.Fatalf("应报非本人，实际: %v", err)
	}
}

func TestCheckinSeatTime(t *testing.T) {
	svc, _, p1, _ := seedSeatTest(t)
	// 预约明天 → 今天签到失败
	tomorrow := addDaysForTest(store.Now(), 1)
	r, err := svc.ReserveSeat(p1, 1, tomorrow, "afternoon")
	if err != nil {
		t.Fatalf("预约失败: %v", err)
	}
	if _, err := svc.CheckinSeat(p1, r.ID); err != service.ErrSeatCheckinDate {
		t.Fatalf("跨日签到应报日期错误，实际: %v", err)
	}
}

func TestAvailableSeatsExcludesReserved(t *testing.T) {
	svc, _, p1, _ := seedSeatTest(t)
	if _, err := svc.ReserveSeat(p1, 1, store.Now(), "afternoon"); err != nil {
		t.Fatalf("预约失败: %v", err)
	}
	avail, err := svc.AvailableSeats(store.Now(), "afternoon")
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	for _, se := range avail {
		if se.ID == 1 {
			t.Fatalf("已预约座位不应出现在可预约列表")
		}
	}
}

func TestExpireStaleSeats(t *testing.T) {
	svc, st, p1, _ := seedSeatTest(t)
	// 昨天预约（active）→ 应过期
	tomorrow := addDaysForTest(store.Now(), 1)
	past := addDaysForTest(store.Now(), -1)
	if _, err := svc.ReserveSeat(p1, 1, tomorrow, "afternoon"); err != nil {
		t.Fatalf("预约失败: %v", err)
	}
	// 直接插入一条昨日预约（绕过服务层日期校验）
	sid := int64(1)
	if _, err := st.CreateSeatReservation(&store.SeatReservation{
		SeatID: sid, PatronID: p1, ReserveDate: past, Slot: "afternoon",
		Status: "active", CreatedAt: store.NowDateTime(),
	}); err != nil {
		t.Fatalf("插入昨日预约失败: %v", err)
	}
	n, err := st.ExpireStaleSeatReservations()
	if err != nil {
		t.Fatalf("过期清理失败: %v", err)
	}
	if n < 1 {
		t.Fatalf("应至少清理 1 条昨日预约，实际 %d", n)
	}
	// 昨日 active 应已过期
	var cnt int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM seat_reservations WHERE reserve_date=? AND status='active'`, past).Scan(&cnt)
	if cnt != 0 {
		t.Fatalf("昨日预约应已过期，仍 active %d 条", cnt)
	}
	// 明日预约不受影响
	rs, _ := svc.MySeatReservations(p1, true)
	found := false
	for _, r := range rs {
		if r.ReserveDate == tomorrow && r.Status == "active" {
			found = true
		}
	}
	if !found {
		t.Fatalf("明日预约不应被误清理")
	}
}
