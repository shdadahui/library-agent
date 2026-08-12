package auth

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisSessionStore 基于 Redis 的会话存储（key: session:{token} → userID）。
type RedisSessionStore struct {
	client *redis.Client
}

// NewRedisSessionStore 连接 Redis；addr 为空或连接失败时返回 nil（由调用方降级）。
func NewRedisSessionStore(addr, password string, db int) (*RedisSessionStore, error) {
	if addr == "" {
		return nil, fmt.Errorf("redis 地址为空")
	}
	client := redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("连接 Redis %s 失败: %w", addr, err)
	}
	return &RedisSessionStore{client: client}, nil
}

func (r *RedisSessionStore) Set(token string, userID int64, ttl time.Duration) error {
	return r.client.Set(context.Background(), "session:"+token, userID, ttl).Err()
}

func (r *RedisSessionStore) Get(token string) (int64, error) {
	v, err := r.client.Get(context.Background(), "session:"+token).Result()
	if err != nil {
		return 0, err
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (r *RedisSessionStore) Del(token string) error {
	return r.client.Del(context.Background(), "session:"+token).Err()
}
