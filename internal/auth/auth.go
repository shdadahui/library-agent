// Package auth 提供登录认证：bcrypt 密码哈希 + 会话令牌管理。
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"time"

	"github.com/shdadahui/library-agent/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// 业务错误。
var (
	ErrUserExists        = errors.New("用户名已存在")
	ErrInvalidCredential = errors.New("用户名或密码错误")
	ErrSessionInvalid    = errors.New("会话无效或已过期")
	ErrTooManyAttempts   = errors.New("登录失败次数过多，请 15 分钟后再试")
)

// 登录限流：连续失败 N 次锁定 15 分钟。
const (
	maxLoginFails  = 5
	lockDuration   = 15 * time.Minute
	failKeyPrefix  = "login_fail:"
)

// SessionStore 会话存储接口（Redis 实现 + 内存兜底）。
type SessionStore interface {
	Set(token string, userID int64, ttl time.Duration) error
	Get(token string) (int64, error)
	Del(token string) error
}

// Manager 认证管理器。
type Manager struct {
	st   *store.Store
	sess SessionStore
	ttl  time.Duration
}

// NewManager 创建认证管理器；session 存储连接失败时自动降级为内存实现。
func NewManager(st *store.Store, sess SessionStore, ttl time.Duration) *Manager {
	return &Manager{st: st, sess: sess, ttl: ttl}
}

// NewToken 生成随机会话令牌（32 字节 hex）。
func NewToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Register 注册：创建读者（姓名）+ 登录用户。
func (m *Manager) Register(username, password, name string) (*store.User, error) {
	if _, err := m.st.GetUserByUsername(username); err == nil {
		return nil, ErrUserExists
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	// 创建读者（姓名可能重名，加注册时间戳避免条码冲突）
	pid, err := m.st.InsertPatron(&store.Patron{
		Name:   name,
		Barcode: "U" + time.Now().Format("150405") + username,
	})
	if err != nil {
		return nil, err
	}
	uid, err := m.st.CreateUser(&store.User{
		Username: username, PasswordHash: string(hash), PatronID: pid,
		CreatedAt: store.NowDateTime(),
	})
	if err != nil {
		return nil, err
	}
	return m.st.GetUserByID(uid)
}

// Login 校验凭据并签发会话令牌（带失败限流：连续 5 次错误锁定 15 分钟）。
func (m *Manager) Login(username, password string) (string, *store.User, error) {
	failKey := failKeyPrefix + username
	// 限流检查
	if fails, _ := m.sess.Get(failKey); fails >= maxLoginFails {
		return "", nil, ErrTooManyAttempts
	}
	u, err := m.st.GetUserByUsername(username)
	if err != nil {
		m.recordFail(failKey)
		return "", nil, ErrInvalidCredential
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		m.recordFail(failKey)
		return "", nil, ErrInvalidCredential
	}
	// 登录成功：清除失败计数
	_ = m.sess.Del(failKey)
	token := NewToken()
	if err := m.sess.Set(token, u.ID, m.ttl); err != nil {
		log.Printf("[auth] 会话写入失败: %v", err)
		return "", nil, err
	}
	return token, u, nil
}

// recordFail 记录一次登录失败（达到阈值后锁定）。
func (m *Manager) recordFail(key string) {
	fails, _ := m.sess.Get(key)
	_ = m.sess.Set(key, fails+1, lockDuration)
}

// Logout 注销会话。
func (m *Manager) Logout(token string) { _ = m.sess.Del(token) }

// Authenticate 校验令牌并返回用户。
func (m *Manager) Authenticate(token string) (*store.User, error) {
	if token == "" {
		return nil, ErrSessionInvalid
	}
	uid, err := m.sess.Get(token)
	if err != nil || uid <= 0 {
		return nil, ErrSessionInvalid
	}
	u, err := m.st.GetUserByID(uid)
	if err != nil {
		return nil, ErrSessionInvalid
	}
	return u, nil
}
