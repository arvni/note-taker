package synccontrol

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func testRedis(t *testing.T) *redis.Client {
	url := os.Getenv("REDIS_URL")
	if url == "" {
		t.Skip("REDIS_URL not set")
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opt)
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("redis: %v", err)
	}
	t.Cleanup(func() {
		rdb.Del(context.Background(), requestKey, lastRunKey, "calendar-sync:dest-error:999")
		rdb.Close()
	})
	return rdb
}

func TestRequestTake_FullNotDowngraded(t *testing.T) {
	ctx, rdb := context.Background(), testRedis(t)
	rdb.Del(ctx, requestKey)
	if ok, _, _ := Take(ctx, rdb); ok {
		t.Fatal("nothing should be queued")
	}
	_ = Request(ctx, rdb, true)
	_ = Request(ctx, rdb, false) // must not downgrade the queued full resync
	if q, f := Pending(ctx, rdb); !q || !f {
		t.Fatalf("pending=%v full=%v, want queued full", q, f)
	}
	ok, full, err := Take(ctx, rdb)
	if err != nil || !ok || !full {
		t.Fatalf("Take = %v %v %v, want queued full", ok, full, err)
	}
	if ok, _, _ := Take(ctx, rdb); ok {
		t.Fatal("request must be consumed exactly once")
	}
}

func TestDestError_NotifiesOncePerOutage(t *testing.T) {
	ctx, rdb := context.Background(), testRedis(t)
	rdb.Del(ctx, "calendar-sync:dest-error:999")
	isNew, err := SetDestError(ctx, rdb, 999, "invalid_grant")
	if err != nil || !isNew {
		t.Fatalf("first failure should be new: %v %v", isNew, err)
	}
	first, _ := LoadDestError(ctx, rdb, 999)
	time.Sleep(10 * time.Millisecond)
	if isNew, _ := SetDestError(ctx, rdb, 999, "invalid_grant again"); isNew {
		t.Fatal("repeat failure must not re-notify")
	}
	again, _ := LoadDestError(ctx, rdb, 999)
	if !again.Since.Equal(first.Since) || again.Message != "invalid_grant again" {
		t.Fatalf("since should be kept, message updated: %+v vs %+v", first, again)
	}
	if was, _ := ClearDestError(ctx, rdb, 999); !was {
		t.Fatal("clear should report it was failing")
	}
	if was, _ := ClearDestError(ctx, rdb, 999); was {
		t.Fatal("second clear should report healthy")
	}
}

func TestLastRunRoundTrip(t *testing.T) {
	ctx, rdb := context.Background(), testRedis(t)
	in := RunReport{Trigger: "manual", Full: true, Employees: 9, Created: 3, Disconnected: []string{"x@y.com"}}
	if err := SaveLast(ctx, rdb, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadLast(ctx, rdb)
	if err != nil || out == nil || out.Created != 3 || !out.Full || out.Disconnected[0] != "x@y.com" {
		t.Fatalf("round trip: %+v %v", out, err)
	}
}
