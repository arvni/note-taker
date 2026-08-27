package redisx

import (
	"context"
	"os"
	"testing"
	"time"
)

// testClient connects to REDIS_URL or skips the test when Redis is unavailable.
func testClient(t *testing.T) *Client {
	t.Helper()
	url := os.Getenv("REDIS_URL")
	if url == "" {
		t.Skip("REDIS_URL not set; skipping Redis integration test")
	}
	c, err := Connect(context.Background(), url)
	if err != nil {
		t.Skipf("cannot connect to Redis: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestLockMutualExclusion(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	key := TokenRefreshKey(42)
	_ = c.Del(ctx, key).Err()

	l1, err := c.Acquire(ctx, key, 5*time.Second)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if _, err := c.Acquire(ctx, key, 5*time.Second); err != ErrNotAcquired {
		t.Fatalf("second acquire: got %v, want ErrNotAcquired", err)
	}
	if err := l1.Release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
	l2, err := c.Acquire(ctx, key, 5*time.Second)
	if err != nil {
		t.Fatalf("re-acquire after release failed: %v", err)
	}
	_ = l2.Release(ctx)
}

func TestLockReleaseIsOwnershipChecked(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	key := "test-ownership-lock"
	_ = c.Del(ctx, key).Err()

	l1, err := c.Acquire(ctx, key, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	// Let l1 expire, then a second holder takes the lock.
	time.Sleep(200 * time.Millisecond)
	l2, err := c.Acquire(ctx, key, 5*time.Second)
	if err != nil {
		t.Fatalf("l2 acquire after expiry: %v", err)
	}
	// l1's stale Release must NOT delete l2's lock.
	if err := l1.Release(ctx); err != nil {
		t.Fatalf("l1 release: %v", err)
	}
	if _, err := c.Acquire(ctx, key, time.Second); err != ErrNotAcquired {
		t.Fatal("l1 wrongly released l2's lock")
	}
	_ = l2.Release(ctx)
}

func TestRateLimiterFixedWindow(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	key := "test-rl"
	_ = c.Del(ctx, "ratelimit:"+key).Err()

	rl := c.NewRateLimiter(3, 500*time.Millisecond)
	for i := 1; i <= 3; i++ {
		r, err := rl.Allow(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if !r.Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}
	r, err := rl.Allow(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if r.Allowed {
		t.Fatal("4th request should be blocked")
	}
	if r.Remaining != 0 {
		t.Fatalf("remaining = %d, want 0", r.Remaining)
	}
	// After the window expires, requests are allowed again.
	time.Sleep(600 * time.Millisecond)
	r, err = rl.Allow(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Allowed {
		t.Fatal("request after window reset should be allowed")
	}
}
