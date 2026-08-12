package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shdadahui/library-agent/internal/agent"
	"github.com/shdadahui/library-agent/internal/auth"
	"github.com/shdadahui/library-agent/internal/config"
	"github.com/shdadahui/library-agent/internal/service"
	"github.com/shdadahui/library-agent/internal/store"
)

func ioCopy(dst *strings.Builder, src io.Reader) (int64, error) {
	return io.Copy(dst, src)
}

// newTestAPIServer 搭建完整 HTTP 测试服务（内存库 + 内存会话 + mock LLM）。
// 返回 server、alice token、bob token。
func newTestAPIServer(t *testing.T) (*httptest.Server, string, string) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// 张三(读者1)/李四(读者2) + 一本书 2 副本
	bid, _ := st.InsertBiblio(&store.Biblio{Title: "越权测试书", Lang: "zh"})
	item1, _ := st.InsertItem(&store.Item{BiblioID: bid, Barcode: "OW1", Status: "available"})
	_, _ = st.InsertItem(&store.Item{BiblioID: bid, Barcode: "OW2", Status: "available"})

	sess := auth.NewMemorySessionStore()
	am := auth.NewManager(st, sess, time.Hour)
	cfg := &config.Config{
		Providers:    map[string]config.Provider{"mock": {DefaultModel: "mock"}},
		ActiveProvider: "mock",
		Temperature:  0,
		MaxIterations: 3,
	}
	svc := service.New(st)
	loop := agent.NewLoop(cfg, svc)
	srv := NewServer(cfg, svc, loop, am)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// 注册 alice(张三)/bob(李四)
	for _, acc := range []struct{ u, p, name string }{
		{"alice", "alice123", "张三"}, {"bob", "bob123", "李四"},
	} {
		if _, err := am.Register(acc.u, acc.p, acc.name); err != nil {
			t.Fatalf("注册 %s 失败: %v", acc.u, err)
		}
	}
	login := func(u, p string) string {
		resp, err := http.Post(ts.URL+"/api/auth/login", "application/json",
			strings.NewReader(fmt.Sprintf(`{"username":"%s","password":"%s"}`, u, p)))
		if err != nil {
			t.Fatalf("登录失败: %v", err)
		}
		defer resp.Body.Close()
		var d struct {
			Token string `json:"token"`
			User  *store.User `json:"user"`
		}
		decodeJSON(resp, &d)
		return d.Token
	}
	aliceTok := login("alice", "alice123")
	bobTok := login("bob", "bob123")

	// 张三借出 item1（模拟）
	zhang, _ := svc.Patron(1)
	bob, _ := svc.Patron(2)
	_ = zhang
	_ = bob
	_, _ = st.Checkout(item1, 1, store.Now(), "2026-08-30") // 张三借
	return ts, aliceTok, bobTok
}

func decodeJSON(resp *http.Response, v any) {
	_ = json.NewDecoder(resp.Body).Decode(v)
}

func authGet(ts *httptest.Server, path, token string) (int, string) {
	req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	var buf strings.Builder
	_, _ = ioCopy(&buf, resp.Body)
	return resp.StatusCode, buf.String()
}

func authPost(ts *httptest.Server, path, token, body string) (int, string) {
	req, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	var buf strings.Builder
	_, _ = ioCopy(&buf, resp.Body)
	return resp.StatusCode, buf.String()
}

func TestAPIForbiddenAccess(t *testing.T) {
	ts, aliceTok, bobTok := newTestAPIServer(t)

	// 1. bob 还张三的书（loan 1 属于张三）→ 403
	code, _ := authPost(ts, "/api/loans/1/return", bobTok, `{}`)
	if code != http.StatusForbidden {
		t.Errorf("bob 还张三的书应 403，实际 %d", code)
	}
	// 2. bob 续借张三的书 → 403
	code, _ = authPost(ts, "/api/loans/1/renew", bobTok, `{}`)
	if code != http.StatusForbidden {
		t.Errorf("bob 续借张三的书应 403，实际 %d", code)
	}
	// 3. bob 看张三罚款 → 403
	code, _ = authGet(ts, "/api/patrons/1/fines", bobTok)
	if code != http.StatusForbidden {
		t.Errorf("bob 看张三罚款应 403，实际 %d", code)
	}
	// 4. bob 看张三借阅历史 → 403
	code, _ = authGet(ts, "/api/patrons/1/history", bobTok)
	if code != http.StatusForbidden {
		t.Errorf("bob 看张三历史应 403，实际 %d", code)
	}
	// 5. bob 代张三借书（body patron_id=1）→ 403
	code, _ = authPost(ts, "/api/loans", bobTok, `{"patron_id":1,"item_id":2}`)
	if code != http.StatusForbidden {
		t.Errorf("bob 代张三借书应 403，实际 %d", code)
	}
	// 6. bob 代张三预约 → 403
	code, _ = authPost(ts, "/api/holds", bobTok, `{"patron_id":1,"book_id":1}`)
	if code != http.StatusForbidden {
		t.Errorf("bob 代张三预约应 403，实际 %d", code)
	}
	// 7. bob 看读者列表 → 403（仅 admin）
	code, _ = authGet(ts, "/api/patrons", bobTok)
	if code != http.StatusForbidden {
		t.Errorf("bob 看读者列表应 403，实际 %d", code)
	}
	// 8. bob 看自己的数据 → 200（正常路径不受影响）
	code, body := authGet(ts, "/api/patrons/2/fines", bobTok)
	if code != http.StatusOK || !strings.Contains(body, "unpaid_cents") {
		t.Errorf("bob 看自己罚款应 200，实际 %d: %s", code, body)
	}
	// 9. alice 还自己的书 → 200
	code, _ = authPost(ts, "/api/loans/1/return", aliceTok, `{}`)
	if code != http.StatusOK {
		t.Errorf("alice 还自己的书应 200，实际 %d", code)
	}
}
