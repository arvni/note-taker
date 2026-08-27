// Package redisx provides the Redis client plus two primitives built on it: a
// distributed lock for serializing token refreshes (spec §16) and a rate
// limiter for OAuth/API endpoints (spec §37).
package redisx

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client wraps a go-redis client.
type Client struct {
	*redis.Client
}

// Connect parses a redis:// URL, opens the client, and verifies it with a ping.
func Connect(ctx context.Context, redisURL string) (*Client, error) {
	if redisURL == "" {
		return nil, fmt.Errorf("redisx: REDIS_URL is empty")
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("redisx: parse url: %w", err)
	}
	rdb := redis.NewClient(opt)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redisx: ping: %w", err)
	}
	return &Client{rdb}, nil
}
