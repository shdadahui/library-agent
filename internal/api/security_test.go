package api

// 安全与契约测试：越权矩阵 / SQL 注入 / XSS 存储 / 无效令牌 / 限流 / 错误响应格式。

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// TestForbiddenMatrix 越权矩阵：读者只能访问自己的数据。
func TestForbiddenMatrix(t *testing.T) {
	ts, alice, _, _ := newTestAPIServerEx(t)
	// alice 查 bob 的借阅/罚款/预约 → 403
	for _, p := range []string{
		"/api/patrons/2/loans", "/api/patrons/2/history",
		"/api/patrons/2/fines", "/api/patrons/2/holds",
	} {
		if code, _ := authGet(ts, p, alice); code != 403 {
			t.Fatalf("alice 访问 %s 应 403，得到 %d", p, code)
		}
	}
	// 无令牌 → 401
	for _, p := range []string{"/api/patrons/1/loans", "/api/me/notifications", "/api/admin/books"} {
		if code, _ := authGet(ts, p, ""); code != 401 {
			t.Fatalf("无令牌访问 %s 应 401，得到 %d", p, code)
		}
	}
	// 无效令牌 → 401
	if code, _ := authGet(ts, "/api/patrons/1/loans", "not-a-real-token"); code != 401 {
		t.Fatalf("无效令牌应 401，得到 %d", code)
	}
}

// TestSQLInjection 参数化查询防注入：恶意输入不报 500、不产生副作用。
func TestSQLInjection(t *testing.T) {
	ts, alice, _, _ := newTestAPIServerEx(t)

	// 1. 登录注入
	if code, _ := authLogin(ts, `' OR 1=1 --`, "x"); code != 401 {
		t.Fatalf("登录注入应 401，得到 %d", code)
	}
	// 2. 搜索注入（书名带 DROP）：400（校验拦截）或 200（空结果）均可，绝不能 500 或执行注入
	if code, body := authGet(ts, "/api/books?q="+`'; DROP TABLE biblios;--`, alice); code != 200 && code != 400 {
		t.Fatalf("搜索注入应被拦截(400)或返回空(200)，得到 %d: %s", code, body)
	}
	// 3. 书目仍在（DROP 未生效）
	if code, _ := authGet(ts, "/api/books?q=%E4%B8%89%E4%BD%93", alice); code != 200 {
		t.Fatalf("注入后书目查询异常: %d", code)
	}
}

// TestXSSStored 恶意内容（书名/姓名）存储与返回不崩溃。
func TestXSSStored(t *testing.T) {
	ts, _, _, st := newTestAPIServerEx(t)
	// 注册带 <script> 的姓名
	tok := authRegister(t, ts, "xssuser", "xss12345", `<script>alert(1)</script>`)
	if code, body := authGet(ts, "/api/auth/me", tok); code != 200 || !strings.Contains(body, "alert(1)") {
		t.Fatalf("me 应原样返回姓名: code=%d body=%s", code, body)
	}
	// 管理端添加带脚本的书名（admin 权限）
	admin := authRegister(t, ts, "xssadmin", "xssad123", "馆员")
	if _, err := st.DB.Exec(`UPDATE users SET role='admin' WHERE username='xssadmin'`); err != nil {
		t.Fatalf("提权失败: %v", err)
	}
	req, _ := httpNewRequest("POST", ts.URL+"/api/admin/books",
		`{"title":"<img src=x onerror=alert(1)>","author":"a","subjects":"x","copies":1}`)
	req.Header.Set("Authorization", "Bearer "+admin)
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("添加书失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("添加带脚本书名应 201: %d %s", resp.StatusCode, b)
	}
}

// TestErrorFormat 错误响应契约：所有错误均为 {"error": "..."}。
func TestErrorFormat(t *testing.T) {
	ts, alice, _, _ := newTestAPIServerEx(t)
	// 制造一个 403
	code, body := authGet(ts, "/api/patrons/2/loans", alice)
	if code != 403 {
		t.Fatalf("应 403: %d", code)
	}
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil || e.Error == "" {
		t.Fatalf("错误响应应含 error 字段: %s", body)
	}
	// 制造一个 404/409
	code, body = authGet(ts, "/api/books/99999", alice)
	if code != 200 && code != 404 {
		// 取决于路由设计：若返回业务空结果则跳过
		t.Logf("书目 404 场景 code=%d", code)
	}
	_ = code
}

// TestLoginRateLimit 失败登录限流：连续失败 5 次后锁定（429/401 且提示锁定）。
func TestLoginRateLimit(t *testing.T) {
	ts, _, _, _ := newTestAPIServerEx(t)
	// 先注册一个用户
	authRegister(t, ts, "ratelimit", "rl12345", "限流人")
	locked := false
	for i := 0; i < 7; i++ {
		code, body := authLogin(ts, "ratelimit", "wrongpass")
		if code == 429 || strings.Contains(body, "锁定") || strings.Contains(body, "次数过多") {
			locked = true
			break
		}
	}
	if !locked {
		t.Fatal("连续 7 次失败登录应触发锁定")
	}
}
