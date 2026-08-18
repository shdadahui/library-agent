package api

// 集成测试辅助函数（social_integration_test 等使用）。

import (
	"encoding/json"
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

var httpClient = &http.Client{Timeout: 10 * time.Second}

// httpNewRequest 带 Bearer token 的请求。
func httpNewRequest(method, url, body string) (*http.Request, error) {
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// authRegister 注册并返回 token。
func authRegister(t *testing.T, ts *httptest.Server, user, pass, name string) string {
	t.Helper()
	req, _ := httpNewRequest("POST", ts.URL+"/api/auth/register",
		`{"username":"`+user+`","password":"`+pass+`","name":"`+name+`"}`)
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("注册请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("注册 %s 应 201, got %d: %s", user, resp.StatusCode, b)
	}
	var d struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&d)
	return d.Token
}

// authLogin 登录（返回状态码与响应体，供失败场景断言）。
func authLogin(ts *httptest.Server, user, pass string) (int, string) {
	req, _ := httpNewRequest("POST", ts.URL+"/api/auth/login",
		`{"username":"`+user+`","password":"`+pass+`"}`)
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// authDelete 带 token 的 DELETE 请求。
func authDelete(ts *httptest.Server, path, token string) (int, string) {
	req, _ := http.NewRequest("DELETE", ts.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// openMemStore 打开内存 SQLite 库（service 层测试用）。
func openMemStore() (*store.Store, error) {
	return store.Open(":memory:")
}

// newTestAPIServerEx 同 newTestAPIServer，额外返回 store（供提权等测试操作）。
func newTestAPIServerEx(t *testing.T) (*httptest.Server, string, string, *store.Store) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	bid, _ := st.InsertBiblio(&store.Biblio{Title: "越权测试书", Lang: "zh"})
	_, _ = st.InsertItem(&store.Item{BiblioID: bid, Barcode: "OW1", Status: "available"})
	_, _ = st.InsertItem(&store.Item{BiblioID: bid, Barcode: "OW2", Status: "available"})

	sess := auth.NewMemorySessionStore()
	am := auth.NewManager(st, sess, time.Hour)
	cfg := &config.Config{
		Providers:      map[string]config.Provider{"mock": {DefaultModel: "mock"}},
		ActiveProvider: "mock",
		Temperature:    0,
		MaxIterations:  3,
	}
	svc := service.New(st)
	loop := agent.NewLoop(cfg, svc)
	srv := NewServer(cfg, svc, loop, am)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	alice := authRegister(t, ts, "alice", "alice123", "张三")
	bob := authRegister(t, ts, "bob", "bob123", "李四")
	return ts, alice, bob, st
}
