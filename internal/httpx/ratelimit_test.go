package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/arvinizadi/fathom/internal/redisx"
)

func testRedis(t *testing.T) *redisx.Client {
	t.Helper()
	u := os.Getenv("REDIS_URL")
	if u == "" {
		t.Skip("REDIS_URL not set")
	}
	c, err := redisx.Connect(context.Background(), u)
	if err != nil {
		t.Skipf("redis: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestRateLimitMiddleware(t *testing.T) {
	c := testRedis(t)
	_ = c.Del(context.Background(), "ratelimit:test-class:1.2.3.4").Err()

	rl := NewRateLimit(c, "test-class", 3, time.Minute)
	var served int
	h := rl.Wrap(func(w http.ResponseWriter, r *http.Request) { served++; w.WriteHeader(200) })

	do := func() int {
		req := httptest.NewRequest("GET", "/x", nil)
		req.RemoteAddr = "1.2.3.4:5555"
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec.Code
	}
	for i := 1; i <= 3; i++ {
		if code := do(); code != 200 {
			t.Fatalf("request %d: code=%d, want 200", i, code)
		}
	}
	rec := do()
	if rec != http.StatusTooManyRequests {
		t.Fatalf("4th request: code=%d, want 429", rec)
	}
	if served != 3 {
		t.Fatalf("handler served %d times, want 3", served)
	}
}

func TestClientIP_XForwardedFor(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "9.9.9.9, 10.0.0.1")
	if ip := clientIP(req); ip != "9.9.9.9" {
		t.Fatalf("clientIP = %q, want 9.9.9.9", ip)
	}
}
