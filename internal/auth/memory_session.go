package auth

import (
	"sync"
	"time"
)

// MemorySessionStore 内存会话存储（Redis 不可用时的兜底，进程重启即失效）。
type MemorySessionStore struct {
	mu  sync.Mutex
	m   map[string]memEntry
	ttl time.Duration
}

type memEntry struct {
	userID    int64
	expiresAt time.Time
}

// NewMemorySessionStore 创建内存会话存储。
func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{m: map[string]memEntry{}}
}

func (s *MemorySessionStore) Set(token string, userID int64, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[token] = memEntry{userID: userID, expiresAt: time.Now().Add(ttl)}
	return nil
}

func (s *MemorySessionStore) Get(token string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[token]
	if !ok {
		return 0, errSessionNotFound
	}
	if time.Now().After(e.expiresAt) {
		delete(s.m, token)
		return 0, errSessionNotFound
	}
	return e.userID, nil
}

func (s *MemorySessionStore) Del(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, token)
	return nil
}

var errSessionNotFound = &sessionErr{}

type sessionErr struct{}

func (*sessionErr) Error() string { return "session not found" }
