// Package api 提供 HTTP 接口：REST 业务端点 + SSE 聊天端点。
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shdadahui/library-agent/internal/agent"
	"github.com/shdadahui/library-agent/internal/auth"
	"github.com/shdadahui/library-agent/internal/config"
	"github.com/shdadahui/library-agent/internal/service"
	"github.com/shdadahui/library-agent/internal/store"
)

// Server HTTP 服务。
type Server struct {
	Svc     *service.Service
	Loop    *agent.Loop
	Cfg     *config.Config
	Auth    *auth.Manager
	metrics *Metrics
	mux     *http.ServeMux
}

// NewServer 创建服务并注册路由。
func NewServer(cfg *config.Config, svc *service.Service, loop *agent.Loop, am *auth.Manager) *Server {
	s := &Server{Svc: svc, Loop: loop, Cfg: cfg, Auth: am, metrics: NewMetrics(), mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler 返回根处理器（含请求日志与认证中间件）。
func (s *Server) Handler() http.Handler {
	return s.withLogging(s.withAuth(s.mux))
}

// withAuth 认证中间件：受保护 API 需携带 Bearer 令牌。
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if !strings.HasPrefix(path, "/api/") || isPublicPath(path) {
			next.ServeHTTP(w, r)
			return
		}
		token := bearerToken(r)
		user, err := s.Auth.Authenticate(token)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "请先登录（或会话已过期）")
			return
		}
		// 将用户注入上下文，供各 handler 使用
		ctx := context.WithValue(r.Context(), ctxUserKey{}, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type ctxUserKey struct{}

// currentUser 从上下文取当前登录用户。
func currentUser(r *http.Request) *store.User {
	if u, ok := r.Context().Value(ctxUserKey{}).(*store.User); ok {
		return u
	}
	return nil
}

// IsAdmin 判断当前用户是否为管理员。
func IsAdmin(r *http.Request) bool {
	u := currentUser(r)
	return u != nil && u.Role == "admin"
}

// requireAdmin 管理员鉴权中间件。
func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsAdmin(r) {
			writeErr(w, http.StatusForbidden, "需要管理员权限")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearerToken 提取 Authorization: Bearer xxx。
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// isPublicPath 无需登录的公开路径。
func isPublicPath(path string) bool {
	switch {
	case path == "/api/health", path == "/api/metrics":
		return true
	case path == "/api/auth/register", path == "/api/auth/login":
		return true
	case strings.HasPrefix(path, "/api/books"):
		return true
	default:
		return false
	}
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /api/health", s.handleHealth)
	m.HandleFunc("GET /api/metrics", s.handleMetrics)
	m.HandleFunc("GET /api/books", s.handleSearchBooks)
	m.HandleFunc("GET /api/books/hot", s.handleHotBooks)
	m.HandleFunc("GET /api/books/new", s.handleNewBooks)
	m.HandleFunc("GET /api/books/{id}", s.handleBookDetail)
	m.HandleFunc("GET /api/patrons", s.handlePatrons)
	m.HandleFunc("GET /api/recommend", s.handleRecommend)
	m.HandleFunc("GET /api/me/report", s.handleMyReport)
	m.HandleFunc("GET /api/me/seat-reservations", s.handleMySeatReservations)
	// 座位预约
	m.HandleFunc("GET /api/seats", s.handleSeats)
	m.HandleFunc("GET /api/seats/areas", s.handleSeatMeta)
	m.HandleFunc("GET /api/seats/available", s.handleAvailableSeats)
	m.HandleFunc("POST /api/seats/reserve", s.handleReserveSeat)
	m.HandleFunc("POST /api/seat-reservations/{id}/cancel", s.handleCancelSeatReservation)
	m.HandleFunc("POST /api/seat-reservations/{id}/checkin", s.handleCheckinSeat)
	// 门禁
	m.HandleFunc("POST /api/gate/scan", s.handleGateScan)
	m.HandleFunc("GET /api/gate/status", s.handleGateStatus)
	// 管理员端点（路由分组：包在 requireAdmin 里）
	m.Handle("GET /api/admin/users", requireAdmin(http.HandlerFunc(s.handleAdminUsers)))
	m.Handle("GET /api/admin/books", requireAdmin(http.HandlerFunc(s.handleAdminBooks)))
	m.Handle("GET /api/admin/stats", requireAdmin(http.HandlerFunc(s.handleAdminStats)))
	m.HandleFunc("GET /api/patrons/{id}/loans", s.handlePatronLoans)
	m.HandleFunc("GET /api/patrons/{id}/history", s.handlePatronHistory)
	m.HandleFunc("GET /api/patrons/{id}/fines", s.handlePatronFines)
	m.HandleFunc("GET /api/patrons/{id}/holds", s.handlePatronHolds)
	m.HandleFunc("POST /api/loans", s.handleBorrow)
	m.HandleFunc("POST /api/loans/{id}/return", s.handleReturn)
	m.HandleFunc("POST /api/loans/{id}/renew", s.handleRenew)
	m.HandleFunc("POST /api/holds", s.handlePlaceHold)
	m.HandleFunc("POST /api/chat", s.handleChat)
	m.HandleFunc("POST /api/auth/register", s.handleRegister)
	m.HandleFunc("POST /api/auth/login", s.handleLogin)
	m.HandleFunc("POST /api/auth/logout", s.handleLogout)
	m.HandleFunc("GET /api/auth/me", s.handleMe)
	m.HandleFunc("GET /api/conversations", s.handleListConversations)
	m.HandleFunc("POST /api/conversations", s.handleCreateConversation)
	m.HandleFunc("GET /api/conversations/{id}/messages", s.handleConversationMessages)
	m.HandleFunc("DELETE /api/conversations/{id}", s.handleDeleteConversation)
	m.HandleFunc("/", s.handleStatic)
}

// ---- REST 处理器 ----

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	n, err := s.Svc.CountBooks()
	if err != nil {
		n = 0
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "provider": s.Cfg.ActiveProvider, "books": n,
	})
}

func (s *Server) handleSearchBooks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	lang := r.URL.Query().Get("lang")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	books, err := s.Svc.SearchBooks(q, lang, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, books)
}

func (s *Server) handleBookDetail(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	b, items, err := s.Svc.BookAvailability(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"book": b, "items": items})
}

func (s *Server) handlePatrons(w http.ResponseWriter, _ *http.Request) {
	patrons, err := s.Svc.Patrons()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, patrons)
}

func (s *Server) handlePatronLoans(w http.ResponseWriter, r *http.Request) {
	pid, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	loans, err := s.Svc.PatronLoans(pid)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, loans)
}

func (s *Server) handlePatronHistory(w http.ResponseWriter, r *http.Request) {
	pid, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	loans, err := s.Svc.LoanHistory(pid)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, loans)
}

func (s *Server) handlePatronFines(w http.ResponseWriter, r *http.Request) {
	pid, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	fines, err := s.Svc.Fines(pid)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, fines)
}

func (s *Server) handlePatronHolds(w http.ResponseWriter, r *http.Request) {
	pid, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	holds, err := s.Svc.PatronHolds(pid)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, holds)
}

func (s *Server) handleBorrow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PatronID int64 `json:"patron_id"`
		ItemID   int64 `json:"item_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	loan, err := s.Svc.Borrow(body.PatronID, body.ItemID)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, loan)
}

func (s *Server) handleReturn(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.Svc.Return(id)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleRenew(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	loan, err := s.Svc.Renew(id)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, loan)
}

func (s *Server) handlePlaceHold(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PatronID int64 `json:"patron_id"`
		BookID   int64 `json:"book_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	hold, err := s.Svc.PlaceHold(body.PatronID, body.BookID)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, hold)
}

// ---- 静态资源 ----

// handleStatic 从磁盘 web/ 目录提供前端静态资源。
// 查找顺序：当前工作目录 web/ → 可执行文件所在目录 web/。
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if data, err := readWebFile(path); err == nil {
		serveBytes(w, r, path, data)
		return
	}
	http.NotFound(w, r)
}

func readWebFile(rel string) ([]byte, error) {
	candidates := []string{filepath.Join("web", rel)}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "web", rel))
	}
	for _, p := range candidates {
		if data, err := os.ReadFile(p); err == nil {
			return data, nil
		}
	}
	return nil, os.ErrNotExist
}

func serveBytes(w http.ResponseWriter, r *http.Request, path string, data []byte) {
	ct := "text/plain"
	switch {
	case strings.HasSuffix(path, ".html"):
		ct = "text/html; charset=utf-8"
	case strings.HasSuffix(path, ".js"):
		ct = "application/javascript; charset=utf-8"
	case strings.HasSuffix(path, ".css"):
		ct = "text/css; charset=utf-8"
	case strings.HasSuffix(path, ".svg"):
		ct = "image/svg+xml"
	case strings.HasSuffix(path, ".png"):
		ct = "image/png"
	}
	w.Header().Set("Content-Type", ct)
	w.Write(data)
}

// ---- 工具函数 ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return false
	}
	return true
}

func pathID(r *http.Request, name string) (int64, error) {
	v := r.PathValue(name)
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("无效的 %s: %s", name, v)
	}
	return id, nil
}
