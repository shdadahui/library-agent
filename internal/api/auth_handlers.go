package api

import (
	"hash/fnv"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/shdadahui/library-agent/internal/auth"
)

// hashIP 将 IP 转为稳定正整数（限流 key 用，避免原始 IP 字符串入 key）。
func hashIP(ip string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(ip))
	return int64(h.Sum64() % 1_000_000_007)
}

// clientIP 提取客户端 IP：优先 X-Forwarded-For（nginx 反代），
// 否则从 RemoteAddr 剥离端口（RemoteAddr 形如 "127.0.0.1:53211"，
// 若直接入 key 会因端口变化导致限流永不触发）。
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// checkPasswordStrength 密码强度校验：至少 6 位且包含字母与数字。
func checkPasswordStrength(pw string) string {
	if len(pw) < 6 {
		return "密码至少 6 位"
	}
	hasLetter, hasDigit := false, false
	for _, c := range pw {
		if unicode.IsLetter(c) {
			hasLetter = true
		}
		if unicode.IsDigit(c) {
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return "密码需同时包含字母和数字"
	}
	if strings.ContainsAny(pw, " \t") {
		return "密码不能包含空格"
	}
	return ""
}

// RegisterRequest 注册请求。
type RegisterRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Name     string `json:"name"` // 真实姓名（自动创建读者账号）
}

// LoginRequest 登录请求。
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleRegister 注册：创建读者 + 登录用户，返回会话令牌。
// 防滥用：同一 IP 每小时最多注册 5 次（Redis 计数）。
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if err := s.Auth.CheckRate("reg_rate:", hashIP(clientIP(r)), 5, time.Hour); err != nil {
		s.metrics.IncRateLimited()
		writeErr(w, http.StatusTooManyRequests, "注册过于频繁，请稍后再试")
		return
	}
	var body RegisterRequest
	if !decodeBody(w, r, &body) {
		return
	}
	if len(body.Username) < 2 {
		writeErr(w, http.StatusBadRequest, "用户名至少 2 个字符")
		return
	}
	if msg := checkPasswordStrength(body.Password); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if body.Name == "" {
		body.Name = body.Username
	}
	user, err := s.Auth.Register(body.Username, body.Password, body.Name)
	if err != nil {
		code := http.StatusConflict
		if err == auth.ErrUserExists {
			code = http.StatusConflict
		} else {
			code = http.StatusInternalServerError
		}
		writeErr(w, code, err.Error())
		return
	}
	token, _, err := s.Auth.Login(body.Username, body.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "user": user})
}

// handleLogin 登录。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body LoginRequest
	if !decodeBody(w, r, &body) {
		return
	}
	token, user, err := s.Auth.Login(body.Username, body.Password)
	if err != nil {
		s.Svc.RecordLoginLog(0, body.Username, clientIP(r), false)
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	s.Svc.RecordLoginLog(user.ID, user.Username, clientIP(r), true)
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": user})
}

// handleLogout 注销。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	if token != "" {
		s.Auth.Logout(token)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMe 当前用户信息（含绑定读者）。
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	patron, err := s.Svc.Patron(user.PatronID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读者不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user, "patron": patron})
}
