package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type loginLimiter struct {
	redis  *redis.Client
	prefix string
	mu     sync.Mutex
	local  map[string]localAttempt
}

type localAttempt struct {
	count     int
	expiresAt time.Time
}

func newLoginLimiter(client *redis.Client, prefix string) *loginLimiter {
	return &loginLimiter{redis: client, prefix: prefix + "auth:login:", local: make(map[string]localAttempt)}
}

func (l *loginLimiter) allow(ctx context.Context, value string, now time.Time) bool {
	sum := sha256.Sum256([]byte(value))
	key := l.prefix + hex.EncodeToString(sum[:])
	if l.redis != nil {
		pipe := l.redis.TxPipeline()
		increment := pipe.Incr(ctx, key)
		pipe.Expire(ctx, key, 15*time.Minute)
		if _, err := pipe.Exec(ctx); err == nil {
			return increment.Val() <= 5
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.local[key]
	if !entry.expiresAt.After(now) {
		entry = localAttempt{expiresAt: now.Add(15 * time.Minute)}
	}
	entry.count++
	l.local[key] = entry
	return entry.count <= 5
}

func (l *loginLimiter) clear(ctx context.Context, values ...string) {
	keys := make([]string, 0, len(values))
	for _, value := range values {
		sum := sha256.Sum256([]byte(value))
		keys = append(keys, l.prefix+hex.EncodeToString(sum[:]))
	}
	if l.redis != nil && len(keys) > 0 {
		_ = l.redis.Del(ctx, keys...).Err()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, key := range keys {
		delete(l.local, key)
	}
}
