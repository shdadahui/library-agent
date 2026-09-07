package auth

// 认证安全测试：密码哈希存储 / 登录失败锁定 / 限流 / 会话生命周期。
// 独立内存库，覆盖 auth 包（安全核心，此前覆盖率为 0）。

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shdadahui/library-agent/internal/store"
	"golang.org/x/crypto/bcrypt"
)

func newTestAuth(t *testing.T) *Manager {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return NewManager(st, NewMemorySessionStore(), time.Hour)
}

// TestPasswordHashedNotPlain 密码必须以 bcrypt 哈希存储，绝不明文。
func TestPasswordHashedNotPlain(t *testing.T) {
	m := newTestAuth(t)
	u, err := m.Register("alice", "Alice@123", "张三")
	if err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	if strings.Contains(u.PasswordHash, "Alice@123") {
		t.Fatal("密码不得以明文形式保存")
	}
	if !strings.HasPrefix(u.PasswordHash, "$2") {
		t.Fatalf("应为 bcrypt 哈希（$2a/$2b/$2y 前缀），实际: %s", u.PasswordHash[:6])
	}
	// bcrypt 校验可通过
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("Alice@123")) != nil {
		t.Fatal("bcrypt 校验应通过")
	}
}

// TestRegisterDuplicate 重复用户名应被拒绝。
func TestRegisterDuplicate(t *testing.T) {
	m := newTestAuth(t)
	if _, err := m.Register("bob", "Bob@1234", "李四"); err != nil {
		t.Fatalf("首次注册失败: %v", err)
	}
	if _, err := m.Register("bob", "Other@123", "李四2"); !errors.Is(err, ErrUserExists) {
		t.Fatalf("重复注册应报 ErrUserExists: %v", err)
	}
}

// TestLoginWrongPassword 错误密码登录失败。
func TestLoginWrongPassword(t *testing.T) {
	m := newTestAuth(t)
	if _, err := m.Register("carol", "Carol@123", "王五"); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	if _, _, err := m.Login("carol", "wrongpass"); err == nil {
		t.Fatal("错误密码应登录失败")
	}
}

// TestLoginFailLockout 连续失败达到阈值后锁定（暴力破解防护）。
func TestLoginFailLockout(t *testing.T) {
	m := newTestAuth(t)
	if _, err := m.Register("dave", "Dave@1234", "赵六"); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	locked := false
	for i := 0; i < 8; i++ {
		if _, _, err := m.Login("dave", "badpass"); errors.Is(err, ErrTooManyAttempts) {
			locked = true
			break
		}
	}
	if !locked {
		t.Fatal("连续失败登录应触发锁定（ErrTooManyAttempts）")
	}
	// 锁定期间即使正确密码也拒绝（防定时爆破）
	if _, _, err := m.Login("dave", "Dave@1234"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("锁定期间正确密码也应被拒: %v", err)
	}
}

// TestSessionLifecycle 会话：登录后可用，登出后立即失效。
func TestSessionLifecycle(t *testing.T) {
	m := newTestAuth(t)
	if _, err := m.Register("erin", "Erin@1234", "钱七"); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	token, u, err := m.Login("erin", "Erin@1234")
	if err != nil || token == "" {
		t.Fatalf("登录失败: %v token=%q", err, token)
	}
	got, err := m.Authenticate(token)
	if err != nil || got.ID != u.ID {
		t.Fatalf("会话校验失败: %v user=%+v", err, got)
	}
	m.Logout(token)
	if _, err := m.Authenticate(token); err == nil {
		t.Fatal("登出后会话应立即失效")
	}
}

// TestCheckRateLimit 限流：窗口内超过阈值应被拒绝。
func TestCheckRateLimit(t *testing.T) {
	m := newTestAuth(t)
	key := int64(12345)
	for i := 0; i < 3; i++ {
		if err := m.CheckRate("test_rate:", key, 3, time.Hour); err != nil {
			t.Fatalf("第 %d 次应放行: %v", i+1, err)
		}
	}
	if err := m.CheckRate("test_rate:", key, 3, time.Hour); err == nil {
		t.Fatal("超过阈值应被限流")
	}
	// 不同 key 互不影响
	if err := m.CheckRate("test_rate:", int64(999), 3, time.Hour); err != nil {
		t.Fatalf("不同 key 不应互相影响: %v", err)
	}
}

// TestWeakPasswordRejected 弱密码应被拒绝（密码强度校验）。
func TestWeakPasswordRejected(t *testing.T) {
	m := newTestAuth(t)
	for _, weak := range []string{"1", "abc", "12345", "password"} {
		if _, err := m.Register("weak"+weak, weak, "弱密码用户"); err == nil {
			t.Fatalf("弱密码 %q 应被拒绝", weak)
		}
	}
}
