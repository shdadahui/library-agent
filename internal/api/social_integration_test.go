package api

// 集成测试：P1/P2 新增功能 + 管理端 + 权限边界（内存 SQLite，可重复执行）。
// 复用 newTestAPIServer / authGet / authPost / authRegister（api_test.go）。

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/shdadahui/library-agent/internal/service"
	"github.com/shdadahui/library-agent/internal/store"
)

// ---- 收藏 ----

func TestFavoriteToggle(t *testing.T) {
	ts, alice, _ := newTestAPIServer(t)
	var bid int64
	if err := json.Unmarshal([]byte(`{"id":1}`), &bid); err == nil { /* noop */ }

	// 未登录 → 401
	if code, _ := authPost(ts, "/api/me/favorites/1", "", ""); code != 401 {
		t.Fatalf("未登录收藏应 401，得到 %d", code)
	}
	// 收藏 → true
	code, body := authPost(ts, "/api/me/favorites/1", alice, "")
	if code != 200 || !strings.Contains(body, `"favorited":true`) {
		t.Fatalf("收藏应成功: code=%d body=%s", code, body)
	}
	// 再点 → false（取消）
	code, body = authPost(ts, "/api/me/favorites/1", alice, "")
	if code != 200 || !strings.Contains(body, `"favorited":false`) {
		t.Fatalf("取消收藏应成功: code=%d body=%s", code, body)
	}
	// 不存在书目 → 409
	if code, _ := authPost(ts, "/api/me/favorites/99999", alice, ""); code != 409 {
		t.Fatalf("收藏不存在书目应 409，得到 %d", code)
	}
	// 收藏后列表可见
	_, _ = authPost(ts, "/api/me/favorites/1", alice, "")
	code, body = authGet(ts, "/api/me/favorites", alice)
	if code != 200 || !strings.Contains(body, "越权测试书") {
		t.Fatalf("收藏列表应含该书: code=%d body=%s", code, body)
	}
}

// ---- 评分 ----

func TestRateBook(t *testing.T) {
	ts, alice, bob := newTestAPIServer(t)

	// 非法分值 0 / 6 → 400
	for _, sc := range []int{0, 6} {
		if code, _ := authPost(ts, "/api/biblios/1/rating", alice, fmt.Sprintf(`{"score":%d}`, sc)); code != 400 {
			t.Fatalf("评分 %d 应 400，得到 %d", sc, code)
		}
	}
	// alice 评 5 → avg=5
	if code, body := authPost(ts, "/api/biblios/1/rating", alice, `{"score":5}`); code != 200 || !strings.Contains(body, `"avg":5`) {
		t.Fatalf("评分5应成功: code=%d body=%s", code, body)
	}
	// alice 改评 4 → upsert 后 avg=4 count=1
	if code, body := authPost(ts, "/api/biblios/1/rating", alice, `{"score":4}`); code != 200 || !strings.Contains(body, `"avg":4`) || !strings.Contains(body, `"count":1`) {
		t.Fatalf("改评应 upsert: code=%d body=%s", code, body)
	}
	// bob 评 2 → avg=3 count=2（(4+2)/2）
	if code, body := authPost(ts, "/api/biblios/1/rating", bob, `{"score":2}`); code != 200 || !strings.Contains(body, `"avg":3`) || !strings.Contains(body, `"count":2`) {
		t.Fatalf("两人评分均值应 3: code=%d body=%s", code, body)
	}
}

// ---- VIP ----

func TestVIPPrivilege(t *testing.T) {
	ts, alice, bob, st := newTestAPIServerEx(t)
	admin := authRegister(t, ts, "admin", "admin123", "馆员")
	if _, err := st.DB.Exec(`UPDATE users SET role='admin' WHERE username='admin'`); err != nil {
		t.Fatalf("提权失败: %v", err)
	}

	// 非 admin 设置 VIP → 403
	if code, _ := authPost(ts, "/api/admin/patrons/1/vip", alice, `{"vip":true}`); code != 403 {
		t.Fatalf("非 admin 设 VIP 应 403，得到 %d", code)
	}
	// admin 设置 alice VIP
	if code, _ := authPost(ts, "/api/admin/patrons/1/vip", admin, `{"vip":true}`); code != 200 {
		t.Fatalf("admin 设 VIP 失败: %d", code)
	}
	// admin users 返回 vip=1
	if code, body := authGet(ts, "/api/admin/users", admin); code != 200 || !strings.Contains(body, `"vip":1`) {
		t.Fatalf("users 应含 vip: code=%d body=%s", code, body)
	}

	// 普通读者 bob 上限 5：借 6 本 → 第 6 本被拒
	// （newTestAPIServer 只有 1 本书 2 副本，借 6 本需要先造多副本）
	// 单独用 service 测试覆盖上限差异（见 service 层）。
	_ = bob
	// 复测：VIP 读者上限翻倍在 service 层验证
}

// ---- 管理端书目 CRUD ----

func TestAdminBookCRUD(t *testing.T) {
	ts, alice, _, st := newTestAPIServerEx(t)
	admin := authRegister(t, ts, "admin2", "admin2x", "馆员B")
	if _, err := st.DB.Exec(`UPDATE users SET role='admin' WHERE username='admin2'`); err != nil {
		t.Fatalf("提权失败: %v", err)
	}

	// 非 admin 编辑 → 403（PUT）
	req0, _ := httpNewRequest("PUT", ts.URL+"/api/admin/books/1", `{"title":"x"}`)
	req0.Header.Set("Authorization", "Bearer "+alice)
	resp0, _ := httpClient.Do(req0)
	if resp0.StatusCode != 403 {
		t.Fatalf("非 admin 编辑应 403，得到 %d", resp0.StatusCode)
	}
	// 编辑（PUT）
	req, _ := httpNewRequest("PUT", ts.URL+"/api/admin/books/1", `{"title":"改名书","author":"甲","subjects":"小说"}`)
	req.Header.Set("Authorization", "Bearer "+admin)
	resp, _ := httpClient.Do(req)
	if resp.StatusCode != 200 {
		t.Fatalf("编辑书目失败: %d", resp.StatusCode)
	}
	// 分页列表含改名书
	if code, body := authGet(ts, "/api/admin/books?q=改名书", admin); code != 200 || !strings.Contains(body, "改名书") {
		t.Fatalf("编辑后列表应含新书名: code=%d body=%s", code, body)
	}
	// 加副本
	code, body := authPost(ts, "/api/admin/books/1/copies", admin, `{"copies":2}`)
	if code != 201 || !strings.Contains(body, "LIB-") {
		t.Fatalf("加副本失败: code=%d body=%s", code, body)
	}
	// 副本数 = 4（原 2 + 新 2）
	if _, body := authGet(ts, "/api/admin/books?page=1&size=5", admin); !strings.Contains(body, `"total_copies":4`) {
		t.Fatalf("副本数应 4: %s", body)
	}
	// 删除保护：借出一本后再删 → 409
	// 先借出（alice 借副本1）
	if code, _ := authPost(ts, "/api/loans", alice, `{"item_id":1}`); code != 201 {
		t.Fatalf("借出失败: %d", code)
	}
	if code, body := authDelete(ts, "/api/admin/books/1", admin); code != 409 || !strings.Contains(body, "在借") {
		t.Fatalf("有在借应拒绝删除: code=%d body=%s", code, body)
	}
	// 还书后可删除
	if _, body := authGet(ts, "/api/patrons/1/loans", alice); !strings.Contains(body, `"id":1`) {
		t.Fatalf("alice 应在借列表: %s", body)
	}
	if code, _ := authPost(ts, "/api/loans/1/return", alice, ""); code != 200 {
		t.Fatalf("还书失败: %d", code)
	}
	if code, _ := authDelete(ts, "/api/admin/books/1", admin); code != 200 {
		t.Fatalf("还书后删除应成功: %d", code)
	}
}

// ---- 借阅记录分页 + 登录日志 ----

func TestAdminLoansAndLoginLogs(t *testing.T) {
	ts, alice, _, st := newTestAPIServerEx(t)
	admin := authRegister(t, ts, "admin3", "admin3x", "馆员C")
	if _, err := st.DB.Exec(`UPDATE users SET role='admin' WHERE username='admin3'`); err != nil {
		t.Fatalf("提权失败: %v", err)
	}

	// 借一笔制造记录
	if code, body := authPost(ts, "/api/loans", alice, `{"item_id":1}`); code != 201 {
		t.Fatalf("借出失败: %d %s", code, body)
	}
	// 借阅记录含该书
	if code, body := authGet(ts, "/api/admin/loans?status=active", admin); code != 200 || !strings.Contains(body, "越权测试书") {
		t.Fatalf("借阅记录应含该书: code=%d body=%s", code, body)
	}
	// 登录日志：注册与登录已产生成功记录
	if code, body := authGet(ts, "/api/admin/login-logs?page=1&size=10", admin); code != 200 || !strings.Contains(body, "alice") {
		t.Fatalf("登录日志应含 alice: code=%d body=%s", code, body)
	}
	// 失败登录 → 日志含失败记录
	authLogin(ts, "alice", "wrongpass")
	if code, body := authGet(ts, "/api/admin/login-logs?page=1&size=10", admin); code != 200 || !strings.Contains(body, `"success":false`) {
		t.Fatalf("登录日志应含失败记录: code=%d body=%s", code, body)
	}
}

// ---- 到期提醒（去重）----

func TestDueLoansReminder(t *testing.T) {
	st, err := openMemStore()
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	defer st.Close()
	bid, _ := st.InsertBiblio(&store.Biblio{Title: "到期测试书", Lang: "zh"})
	itemID, _ := st.InsertItem(&store.Item{BiblioID: bid, Barcode: "DUE1", Status: "available"})
	pid, _ := st.InsertPatron(&store.Patron{Name: "到期读者", Barcode: "DUE-P", Phone: ""})

	svc := service.New(st)
	// 借出并改到期日为今天
	_, err = svc.Borrow(pid, itemID)
	if err != nil {
		t.Fatalf("借出失败: %v", err)
	}
	if _, err := st.DB.Exec(`UPDATE loans SET due_date=? WHERE item_id=?`, store.Now(), itemID); err != nil {
		t.Fatalf("改到期日失败: %v", err)
	}

	// 第一次扫描 → 生成 1 条 due 提醒
	svc.CheckDueLoansNow()
	notifs, err := st.ListNotifications(pid, 10)
	if err != nil || len(notifs) != 1 {
		t.Fatalf("应生成 1 条通知, got %d err=%v", len(notifs), err)
	}
	if notifs[0].Type != "due" || !strings.Contains(notifs[0].Body, "到期测试书") {
		t.Fatalf("通知内容不符: %+v", notifs[0])
	}
	// 第二次扫描 → 去重（ref_id）不再生成
	svc.CheckDueLoansNow()
	notifs, _ = st.ListNotifications(pid, 10)
	if len(notifs) != 1 {
		t.Fatalf("去重失败: 应仍 1 条, got %d", len(notifs))
	}
	// 改为逾期（昨天到期）再扫描 → 生成 1 条 overdue（不同 type，不冲突）
	today := store.Now()
	tt, _ := time.Parse("2006-01-02", today)
	prev := tt.AddDate(0, 0, -1).Format("2006-01-02")
	if _, err := st.DB.Exec(`UPDATE loans SET due_date=? WHERE item_id=?`, prev, itemID); err != nil {
		t.Fatalf("改逾期日失败: %v", err)
	}
	svc.CheckDueLoansNow()
	notifs, _ = st.ListNotifications(pid, 10)
	if len(notifs) != 2 || notifs[0].Type != "overdue" {
		t.Fatalf("逾期应另生成 overdue 通知: %+v", notifs)
	}
}
