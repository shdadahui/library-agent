package api

import (
	"net/http"

	"github.com/shdadahui/library-agent/internal/auth"
)

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
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var body RegisterRequest
	if !decodeBody(w, r, &body) {
		return
	}
	if len(body.Username) < 2 || len(body.Password) < 4 {
		writeErr(w, http.StatusBadRequest, "用户名至少 2 个字符，密码至少 4 位")
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
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}
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
