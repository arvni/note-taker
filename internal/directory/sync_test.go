package directory

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunPeriodic_RunsImmediatelyTicksAndStops(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		RunPeriodic(ctx, 20*time.Millisecond, func(context.Context) error {
			calls.Add(1)
			return nil
		})
		close(done)
	}()

	// Should run once immediately plus a few ticks.
	time.Sleep(75 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunPeriodic did not stop after cancel")
	}

	n := calls.Load()
	if n < 2 {
		t.Fatalf("expected immediate run + at least one tick, got %d", n)
	}
	// Confirm it truly stopped: count must not grow after cancel.
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != n {
		t.Fatalf("runner kept firing after cancel: %d -> %d", n, calls.Load())
	}
}
