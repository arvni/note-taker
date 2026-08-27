package security

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/arvinizadi/fathom/internal/redisx"
)

type capAlerter struct{ alerts []Alert }

func (c *capAlerter) Alert(_ context.Context, a Alert) { c.alerts = append(c.alerts, a) }

func TestMonitorAlertsOnThreshold(t *testing.T) {
	u := os.Getenv("REDIS_URL")
	if u == "" {
		t.Skip("REDIS_URL not set")
	}
	c, err := redisx.Connect(context.Background(), u)
	if err != nil {
		t.Skipf("redis: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.Del(context.Background(), "ratelimit:sec:failed_oauth:ip1").Err()

	cap := &capAlerter{}
	m := NewMonitor(c, cap, map[EventType]Threshold{
		FailedOAuth: {Limit: 3, Window: time.Minute},
	})
	ctx := context.Background()
	m.Record(ctx, FailedOAuth, "ip1") // 1
	m.Record(ctx, FailedOAuth, "ip1") // 2
	if len(cap.alerts) != 0 {
		t.Fatalf("alerted early: %d", len(cap.alerts))
	}
	m.Record(ctx, FailedOAuth, "ip1") // 3 -> breach
	if len(cap.alerts) != 1 || cap.alerts[0].Type != FailedOAuth {
		t.Fatalf("expected 1 FailedOAuth alert, got %+v", cap.alerts)
	}
}
