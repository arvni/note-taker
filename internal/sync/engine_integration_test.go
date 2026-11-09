package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arvinizadi/fathom/internal/calendar"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/google"
)

type fakeTP struct{}

func (fakeTP) AccessToken(context.Context, db.OrgID, int64) (string, error) { return "tok", nil }

type fakePP struct{}

func (fakePP) RecordingSettings(context.Context, db.OrgID, int64) (bool, bool, bool, error) {
	return true, false, false, nil
}

// mutableZoho lets the test change the events returned between sync runs.
type mutableZoho struct {
	mu     sync.Mutex
	events []map[string]any
}

func (z *mutableZoho) set(evs []map[string]any) {
	z.mu.Lock()
	z.events = evs
	z.mu.Unlock()
}

func (z *mutableZoho) server(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/calendars/work/events", func(w http.ResponseWriter, r *http.Request) {
		z.mu.Lock()
		defer z.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"events": z.events})
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// fakeGoogle records create/update/delete calls.
type fakeGoogle struct {
	mu                        sync.Mutex
	creates, updates, deletes int
	nextID                    int
	gone                      map[string]bool // event ids deleted by hand in Google
}

func (g *fakeGoogle) server(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/calendars/dest/events", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.creates++
		g.nextID++
		json.NewEncoder(w).Encode(map[string]any{"id": "g" + itoa(g.nextID)})
	})
	mux.HandleFunc("/calendars/dest/events/", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			if g.gone[strings.TrimPrefix(r.URL.Path, "/calendars/dest/events/")] {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			g.updates++
			json.NewEncoder(w).Encode(map[string]any{"id": "x"})
		case http.MethodDelete:
			g.deletes++
			w.WriteHeader(204)
		}
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func itoa(n int) string { return string(rune('0' + n)) }

// zt formats a time as Zoho does (UTC basic format).
func zt(t time.Time) string { return t.UTC().Format("20060102T150405Z") }

// tomorrow13 is a stable future slot so the event is still "upcoming".
var tomorrow13 = time.Now().UTC().Truncate(24 * time.Hour).Add(24*time.Hour + 13*time.Hour)

func meet(uid, mod string) map[string]any {
	return meetAt(uid, mod, "Client call", tomorrow13, time.Hour)
}

func meetAt(uid, mod, title string, start time.Time, dur time.Duration) map[string]any {
	return map[string]any{"uid": uid, "title": title,
		"location": "https://meet.google.com/abc-defg-hij",
		"start":    zt(start), "end": zt(start.Add(dur)), "lastmodifiedtime": mod}
}

// setupSync creates an org + authorized employee with one enabled calendar and
// returns an engine wired to fake Zoho/Google servers.
func setupSync(t *testing.T, name string) (context.Context, *db.Pool, db.OrgID, int64, *mutableZoho, *fakeGoogle, *Engine) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	pool, err := db.Connect(context.Background(), url)
	if err != nil {
		t.Skipf("db: %v", err)
	}
	t.Cleanup(pool.Close)
	ctx := context.Background()
	var oid, eid int64
	_ = pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ($1) RETURNING id`, name).Scan(&oid)
	_ = pool.QueryRow(ctx, `INSERT INTO employees (organization_id, email, status, onboarding_status)
		VALUES ($1,$2,'active','authorized') RETURNING id`, oid, name+"@company.com").Scan(&eid)
	_, _ = pool.Exec(ctx, `INSERT INTO calendars (employee_id, calendar_uid, name, enabled, is_personal)
		VALUES ($1,'work','Work',true,false)`, eid)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM event_mappings WHERE employee_id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM calendars WHERE employee_id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE employee_id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM employees WHERE id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, oid)
	})
	zoho := &mutableZoho{}
	zsrv := zoho.server(t)
	g := &fakeGoogle{}
	gsrv := g.server(t)
	repo := calendar.NewRepo(pool)
	scanner := calendar.NewService(fakeTP{}, func(context.Context, db.OrgID) (string, error) { return zsrv.URL, nil }, repo, fakePP{}, nil, false)
	dest := google.NewClient(gsrv.URL, "dest", google.StaticToken("gtok"))
	engine := NewEngine(scanner, func(context.Context, db.OrgID) (Destination, error) { return dest, nil }, repo, nil)
	return ctx, pool, db.OrgID(oid), eid, zoho, g, engine
}

// An event that has already ended disappears from the scan (it only looks
// forward) — that must NOT delete it from the Fathom calendar.
func TestSyncEngine_EndedMeetingIsNotCancelled(t *testing.T) {
	ctx, pool, org, eid, zoho, g, engine := setupSync(t, "sync-ended")
	zoho.set([]map[string]any{meet("e1", "20260801T100000Z")})
	if _, err := engine.SyncEmployee(ctx, org, eid); err != nil {
		t.Fatal(err)
	}
	// Pretend the meeting is over: it ended an hour ago and Zoho no longer returns it.
	_, _ = pool.Exec(ctx, `UPDATE event_mappings SET starts_at = now() - interval '2 hours',
		ends_at = now() - interval '1 hour' WHERE employee_id = $1`, eid)
	zoho.set([]map[string]any{})
	rep, err := engine.SyncEmployee(ctx, org, eid)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Cancelled != 0 || g.deletes != 0 {
		t.Fatalf("ended meeting was cancelled: rep=%+v gdeletes=%d", rep, g.deletes)
	}
}

// Each occurrence of a recurring series becomes its own Fathom event, and a
// legacy single-row mapping for the series is removed, never reused.
func TestSyncEngine_RecurringSeries(t *testing.T) {
	ctx, pool, org, eid, zoho, g, engine := setupSync(t, "sync-recurring")
	w1, w2, w3 := tomorrow13, tomorrow13.Add(7*24*time.Hour), tomorrow13.Add(14*24*time.Hour)

	// Legacy row as written by the old code: uid-only key, last occurrence date.
	_, err := pool.Exec(ctx, `INSERT INTO event_mappings (employee_id, calendar_uid, source_event_id, title,
		starts_at, ends_at, destination_event_id, destination_synced_at, source_updated_at)
		VALUES ($1,'work','series1','Weekly sync',$2,$3,'legacy-dest',now(),now())`, eid, w3, w3.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	zoho.set([]map[string]any{
		meetAt("series1", "20260801T100000Z", "Weekly sync", w1, time.Hour),
		meetAt("series1", "20260801T100000Z", "Weekly sync", w2, time.Hour),
		meetAt("series1", "20260801T100000Z", "Weekly sync", w3, time.Hour),
	})
	rep, err := engine.SyncEmployee(ctx, org, eid)
	if err != nil {
		t.Fatal(err)
	}
	// 3 new events (the w3 occurrence must not reuse the stale legacy event),
	// and the legacy event deleted.
	if rep.Created != 3 || g.creates != 3 {
		t.Fatalf("want 3 creates, rep=%+v gcreates=%d", rep, g.creates)
	}
	if rep.Cancelled != 1 || g.deletes != 1 {
		t.Fatalf("legacy row not cleaned up: rep=%+v gdeletes=%d", rep, g.deletes)
	}
	var active int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM event_mappings WHERE employee_id=$1 AND cancelled_at IS NULL
		AND destination_event_id IS NOT NULL AND destination_event_id <> 'legacy-dest'`, eid).Scan(&active)
	if active != 3 {
		t.Fatalf("want 3 active occurrence mappings, got %d", active)
	}

	// Steady state: nothing changes.
	rep, _ = engine.SyncEmployee(ctx, org, eid)
	if rep.Created != 0 || rep.Updated != 0 || rep.Cancelled != 0 {
		t.Fatalf("second run should be a no-op, got %+v", rep)
	}

	// One occurrence is deleted in Zoho → only that one is cancelled.
	zoho.set([]map[string]any{
		meetAt("series1", "20260801T100000Z", "Weekly sync", w1, time.Hour),
		meetAt("series1", "20260801T100000Z", "Weekly sync", w3, time.Hour),
	})
	rep, _ = engine.SyncEmployee(ctx, org, eid)
	if rep.Cancelled != 1 {
		t.Fatalf("want exactly the removed occurrence cancelled, got %+v", rep)
	}
}

func TestSyncEngine_CreateUpdateCancel(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	pool, err := db.Connect(context.Background(), url)
	if err != nil {
		t.Skipf("db: %v", err)
	}
	t.Cleanup(pool.Close)
	ctx := context.Background()

	var oid, eid int64
	_ = pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ('sync-itest') RETURNING id`).Scan(&oid)
	_ = pool.QueryRow(ctx, `INSERT INTO employees (organization_id, email, status, onboarding_status)
		VALUES ($1,'sync@company.com','active','authorized') RETURNING id`, oid).Scan(&eid)
	// Enabled work calendar.
	_, _ = pool.Exec(ctx, `INSERT INTO calendars (employee_id, calendar_uid, name, enabled, is_personal)
		VALUES ($1,'work','Work',true,false)`, eid)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM event_mappings WHERE employee_id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM calendars WHERE employee_id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE employee_id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM employees WHERE id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, oid)
	})

	zoho := &mutableZoho{}
	zoho.set([]map[string]any{meet("e1", "20260801T100000Z")})
	zsrv := zoho.server(t)
	g := &fakeGoogle{}
	gsrv := g.server(t)

	repo := calendar.NewRepo(pool)
	scanner := calendar.NewService(fakeTP{}, func(context.Context, db.OrgID) (string, error) { return zsrv.URL, nil }, repo, fakePP{}, nil, false)
	dest := google.NewClient(gsrv.URL, "dest", google.StaticToken("gtok"))
	engine := NewEngine(scanner, func(context.Context, db.OrgID) (Destination, error) { return dest, nil }, repo, nil)
	org := db.OrgID(oid)

	// Run 1: CREATE
	rep, err := engine.SyncEmployee(ctx, org, eid)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Created != 1 || g.creates != 1 {
		t.Fatalf("run1 create: rep=%+v gcreates=%d", rep, g.creates)
	}
	var destID string
	_ = pool.QueryRow(ctx, `SELECT coalesce(destination_event_id,'') FROM event_mappings WHERE employee_id=$1`, eid).Scan(&destID)
	if destID == "" {
		t.Fatal("destination_event_id not recorded")
	}

	// Run 2: no change → nothing
	rep, _ = engine.SyncEmployee(ctx, org, eid)
	if rep.Created != 0 || rep.Updated != 0 || rep.Cancelled != 0 {
		t.Fatalf("run2 should be a no-op, got %+v", rep)
	}

	// Run 3: source changed (newer lastmodifiedtime) → UPDATE
	zoho.set([]map[string]any{meet("e1", "20260802T100000Z")})
	rep, err = engine.SyncEmployee(ctx, org, eid)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Updated != 1 || g.updates != 1 {
		t.Fatalf("run3 update: rep=%+v gupdates=%d", rep, g.updates)
	}

	// Run 4: source event removed → CANCEL
	zoho.set([]map[string]any{})
	rep, err = engine.SyncEmployee(ctx, org, eid)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Cancelled != 1 || g.deletes != 1 {
		t.Fatalf("run4 cancel: rep=%+v gdeletes=%d", rep, g.deletes)
	}
	var cancelled *string
	_ = pool.QueryRow(ctx, `SELECT to_char(cancelled_at,'YYYY') FROM event_mappings WHERE employee_id=$1`, eid).Scan(&cancelled)
	if cancelled == nil {
		t.Fatal("mapping not marked cancelled")
	}
}

// Full resync: every upcoming meeting is rewritten, and one deleted by hand in
// Google is recreated and its mapping repointed.
func TestSyncEngine_FullResyncRecreatesDeleted(t *testing.T) {
	ctx, pool, org, eid, zoho, g, engine := setupSync(t, "sync-full")
	zoho.set([]map[string]any{
		meetAt("a", "20260801T100000Z", "A", tomorrow13, time.Hour),
		meetAt("b", "20260801T100000Z", "B", tomorrow13.Add(2*time.Hour), time.Hour),
	})
	if rep, err := engine.SyncEmployee(ctx, org, eid); err != nil || rep.Created != 2 {
		t.Fatalf("initial create: rep=%+v err=%v", rep, err)
	}
	var goneID string
	_ = pool.QueryRow(ctx, `SELECT destination_event_id FROM event_mappings WHERE employee_id=$1 AND source_event_id='a'`, eid).Scan(&goneID)
	g.mu.Lock()
	g.gone = map[string]bool{goneID: true}
	g.mu.Unlock()

	n, err := calendar.NewRepo(pool).ForceResync(ctx)
	if err != nil || n < 2 {
		t.Fatalf("ForceResync queued %d (err %v), want >= 2", n, err)
	}
	rep, err := engine.SyncEmployee(ctx, org, eid)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Updated != 2 || rep.Created != 1 || g.creates != 3 || g.updates != 1 {
		t.Fatalf("want 1 rewrite + 1 recreate, rep=%+v gcreates=%d gupdates=%d", rep, g.creates, g.updates)
	}
	var newID string
	_ = pool.QueryRow(ctx, `SELECT destination_event_id FROM event_mappings WHERE employee_id=$1 AND source_event_id='a'`, eid).Scan(&newID)
	if newID == "" || newID == goneID {
		t.Fatalf("mapping not repointed to recreated event: old=%s new=%s", goneID, newID)
	}
	// And it settles: nothing more to do.
	if rep, _ := engine.SyncEmployee(ctx, org, eid); rep.Created+rep.Updated+rep.Cancelled != 0 {
		t.Fatalf("should be steady, got %+v", rep)
	}
}
