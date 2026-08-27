// Package security detects and raises alerts for suspicious activity (spec §36):
// repeated failed OAuth attempts, OAuth identity mismatches, repeated token
// refresh failures, suspicious onboarding activity, and unusual request volume.
package security

import (
	"context"
	"log"
	"time"

	"github.com/arvinizadi/fathom/internal/redisx"
)

// EventType enumerates monitored security events (spec §36).
type EventType string

const (
	FailedOAuth       EventType = "failed_oauth"
	IdentityMismatch  EventType = "identity_mismatch"
	RefreshFailure    EventType = "refresh_failure"
	SuspiciousOnboard EventType = "suspicious_onboarding"
	UnusualVolume     EventType = "unusual_volume"
)

// Alert describes a threshold breach.
type Alert struct {
	Type      EventType
	Key       string // the subject that breached (e.g. IP or employee id)
	Count     int
	Threshold int
	Window    time.Duration
}

// Alerter delivers alerts (email, pager, SIEM). LogAlerter is the default.
type Alerter interface {
	Alert(ctx context.Context, a Alert)
}

// LogAlerter logs alerts. Replace with a SIEM/paging integration in production.
type LogAlerter struct{}

func (LogAlerter) Alert(_ context.Context, a Alert) {
	log.Printf("SECURITY ALERT: %s key=%s count=%d threshold=%d window=%s",
		a.Type, a.Key, a.Count, a.Threshold, a.Window)
}

// Threshold configures when an event type triggers an alert.
type Threshold struct {
	Limit  int
	Window time.Duration
}

// Monitor counts security events per subject in Redis and alerts on breach.
type Monitor struct {
	redis      *redisx.Client
	alerter    Alerter
	thresholds map[EventType]Threshold
}

// DefaultThresholds are sensible, configurable starting points (spec §36-37).
func DefaultThresholds() map[EventType]Threshold {
	return map[EventType]Threshold{
		FailedOAuth:       {Limit: 5, Window: 10 * time.Minute},
		IdentityMismatch:  {Limit: 1, Window: time.Hour}, // any mismatch is notable (spec §25)
		RefreshFailure:    {Limit: 5, Window: 30 * time.Minute},
		SuspiciousOnboard: {Limit: 10, Window: 10 * time.Minute},
		UnusualVolume:     {Limit: 1000, Window: time.Minute},
	}
}

func NewMonitor(redis *redisx.Client, alerter Alerter, thresholds map[EventType]Threshold) *Monitor {
	if alerter == nil {
		alerter = LogAlerter{}
	}
	if thresholds == nil {
		thresholds = DefaultThresholds()
	}
	return &Monitor{redis: redis, alerter: alerter, thresholds: thresholds}
}

// Record counts one occurrence of an event for a subject key and alerts if the
// configured threshold is reached within the window (spec §36).
func (m *Monitor) Record(ctx context.Context, t EventType, key string) {
	th, ok := m.thresholds[t]
	if !ok {
		return
	}
	// Reuse the fixed-window limiter as a counter: Allow returns the running
	// count; a breach is count >= limit.
	rl := m.redis.NewRateLimiter(th.Limit, th.Window)
	res, err := rl.Allow(ctx, "sec:"+string(t)+":"+key)
	if err != nil {
		log.Printf("security monitor: %v", err)
		return
	}
	if res.Count >= th.Limit {
		m.alerter.Alert(ctx, Alert{Type: t, Key: key, Count: res.Count, Threshold: th.Limit, Window: th.Window})
	}
}

// RecordEvent is a string-typed wrapper over Record for callers that avoid
// importing the EventType constants (e.g. the httpx layer).
func (m *Monitor) RecordEvent(ctx context.Context, event, key string) {
	m.Record(ctx, EventType(event), key)
}
