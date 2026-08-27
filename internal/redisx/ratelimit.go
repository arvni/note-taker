package redisx

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// rateLimitScript implements an atomic fixed-window counter: INCR the key, and
// on first hit set the window expiry. Returns the current count.
var rateLimitScript = redis.NewScript(`
local current = redis.call("INCR", KEYS[1])
if current == 1 then
	redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
return current`)

// RateLimiter enforces a fixed-window request limit (spec §37). Limits are
// configurable, not hardcoded to production values (spec §37).
type RateLimiter struct {
	client *Client
	limit  int
	window time.Duration
}

// NewRateLimiter builds a limiter allowing `limit` requests per `window`.
func (c *Client) NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{client: c, limit: limit, window: window}
}

// Result reports the outcome of an Allow check.
type Result struct {
	Allowed   bool
	Count     int
	Limit     int
	Remaining int
}

// Allow records a hit against `key` (e.g. "oauth-callback:<ip>") and reports
// whether it is within the limit. Fails open on Redis errors is NOT used here:
// on error we return the error so the caller can decide (spec §37 favors safety).
func (rl *RateLimiter) Allow(ctx context.Context, key string) (Result, error) {
	fullKey := "ratelimit:" + key
	n, err := rateLimitScript.Run(ctx, rl.client, []string{fullKey}, rl.window.Milliseconds()).Int()
	if err != nil {
		return Result{}, err
	}
	remaining := rl.limit - n
	if remaining < 0 {
		remaining = 0
	}
	return Result{
		Allowed:   n <= rl.limit,
		Count:     n,
		Limit:     rl.limit,
		Remaining: remaining,
	}, nil
}
