// Package synccontrol is the small Redis-backed channel between the admin API
// (server) and the background calendar sync loop (worker): the server queues a
// manual "sync now" / "full resync" request, the worker picks it up within a
// few seconds, and publishes the outcome of each run plus the health of each
// org's Fathom (Google) destination calendar for the admin UI.
package synccontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	requestKey = "calendar-sync:request"
	lastRunKey = "calendar-sync:last"
	destErrKey = "calendar-sync:dest-error:%d"

	modeQuick = "quick"
	modeFull  = "full"
)

// Request queues a manual sync. A full resync also rewrites every upcoming
// Fathom calendar event; it is never downgraded by a later quick request.
func Request(ctx context.Context, rdb *redis.Client, full bool) error {
	if full {
		return rdb.Set(ctx, requestKey, modeFull, time.Hour).Err()
	}
	return rdb.SetNX(ctx, requestKey, modeQuick, time.Hour).Err()
}

// Pending reports whether a manual sync is queued and not yet picked up.
func Pending(ctx context.Context, rdb *redis.Client) (queued, full bool) {
	v, err := rdb.Get(ctx, requestKey).Result()
	if err != nil {
		return false, false
	}
	return true, v == modeFull
}

// Take atomically consumes a queued request (worker side).
func Take(ctx context.Context, rdb *redis.Client) (requested, full bool, err error) {
	v, err := rdb.GetDel(ctx, requestKey).Result()
	if errors.Is(err, redis.Nil) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return true, v == modeFull, nil
}

// RunReport summarizes one sync run across all connected calendars.
type RunReport struct {
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
	Trigger      string    `json:"trigger"` // "schedule" | "manual"
	Full         bool      `json:"full"`
	Requeued     int64     `json:"requeued"` // meetings forced to rewrite (full resync)
	Employees    int       `json:"employees"`
	Created      int       `json:"created"`
	Updated      int       `json:"updated"`
	Cancelled    int       `json:"cancelled"`
	Failed       int       `json:"failed"`       // employees whose sync errored
	SkippedOrgs  int       `json:"skipped_orgs"` // orgs skipped: destination disconnected
	Disconnected []string  `json:"disconnected"` // employee emails whose Zoho link broke this run
}

func SaveLast(ctx context.Context, rdb *redis.Client, r RunReport) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, lastRunKey, b, 0).Err()
}

func LoadLast(ctx context.Context, rdb *redis.Client) (*RunReport, error) {
	b, err := rdb.Get(ctx, lastRunKey).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r RunReport
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// DestError is the recorded failure of an org's destination calendar.
type DestError struct {
	Message string    `json:"message"`
	Since   time.Time `json:"since"`
}

// SetDestError records that the org's destination is failing. It returns true
// when this is a new failure (it was healthy before), so the caller notifies
// once per outage rather than on every run.
func SetDestError(ctx context.Context, rdb *redis.Client, org int64, msg string) (bool, error) {
	key := fmt.Sprintf(destErrKey, org)
	prev, err := LoadDestError(ctx, rdb, org)
	if err != nil {
		return false, err
	}
	since := time.Now()
	if prev != nil {
		since = prev.Since
	}
	b, _ := json.Marshal(DestError{Message: msg, Since: since})
	if err := rdb.Set(ctx, key, b, 0).Err(); err != nil {
		return false, err
	}
	return prev == nil, nil
}

// ClearDestError marks the destination healthy; returns true if it had been failing.
func ClearDestError(ctx context.Context, rdb *redis.Client, org int64) (bool, error) {
	n, err := rdb.Del(ctx, fmt.Sprintf(destErrKey, org)).Result()
	return n > 0, err
}

func LoadDestError(ctx context.Context, rdb *redis.Client, org int64) (*DestError, error) {
	b, err := rdb.Get(ctx, fmt.Sprintf(destErrKey, org)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var d DestError
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	return &d, nil
}
