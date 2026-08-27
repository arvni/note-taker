package tokens

import (
	"context"
	"errors"
	"time"

	"github.com/arvinizadi/fathom/internal/redisx"
)

// RedisLocker adapts *redisx.Client to the Manager's Locker interface (spec §16).
type RedisLocker struct {
	c *redisx.Client
}

func NewRedisLocker(c *redisx.Client) *RedisLocker { return &RedisLocker{c: c} }

// WithLock acquires the named lock, runs fn, and releases it. If the lock is
// already held it returns ErrLockBusy without running fn.
func (l *RedisLocker) WithLock(ctx context.Context, key string, ttl time.Duration, fn func() error) error {
	lock, err := l.c.Acquire(ctx, key, ttl)
	if errors.Is(err, redisx.ErrNotAcquired) {
		return ErrLockBusy
	}
	if err != nil {
		return err
	}
	defer func() {
		// Release with a fresh context so cancellation of ctx still frees the lock.
		rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = lock.Release(rctx)
	}()
	return fn()
}
