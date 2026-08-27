package httpx

import (
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/arvinizadi/fathom/internal/redisx"
)

// RateLimitMiddleware enforces a per-client-IP request limit on a route class
// using the Redis fixed-window limiter (spec §37). Limits are configurable.
type RateLimitMiddleware struct {
	limiter *redisx.RateLimiter
	class   string
	window  time.Duration
	// failOpen: on a Redis error, allow the request (availability) rather than
	// blocking legitimate traffic. Security-sensitive deployments may prefer
	// fail-closed; this is a deliberate, documented choice (spec §37).
	failOpen bool
}

// NewRateLimit builds a limiter middleware for a route class (e.g. "oauth-callback").
func NewRateLimit(client *redisx.Client, class string, limit int, window time.Duration) *RateLimitMiddleware {
	return &RateLimitMiddleware{
		limiter: client.NewRateLimiter(limit, window), class: class, window: window, failOpen: true,
	}
}

// Wrap applies the rate limit to a handler, keyed by class + client IP (spec §37).
func (m *RateLimitMiddleware) Wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := m.class + ":" + clientIP(r)
		res, err := m.limiter.Allow(r.Context(), key)
		if err != nil {
			if m.failOpen {
				log.Printf("ratelimit: redis error (failing open): %v", err)
				next(w, r)
				return
			}
			http.Error(w, "rate limiter unavailable", http.StatusServiceUnavailable)
			return
		}
		if !res.Allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(m.window.Seconds())))
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

// clientIP extracts the client IP, honoring X-Forwarded-For's first hop when
// present (deployments behind a trusted proxy).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := indexByte(xff, ','); i >= 0 {
			return trim(xff[:i])
		}
		return trim(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
