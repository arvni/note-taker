package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

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
	mu                       sync.Mutex
	creates, updates, deletes int
	nextID                    int
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

func itoa(n int) string { return string(rune('0'+n)) }

func meet(uid, mod string) map[string]any {
	return map[string]any{"uid": uid, "title": "Client call",
		"location": "https://meet.google.com/abc-defg-hij",
		"start": "20260901T130000Z", "end": "20260901T140000Z", "lastmodifiedtime": mod}
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
	scanner := calendar.NewService(fakeTP{}, calendar.NewAPIClient(zsrv.URL), repo, fakePP{}, nil)
	dest := google.NewClient(gsrv.URL, "dest", google.StaticToken("gtok"))
	engine := NewEngine(scanner, dest, repo, nil)
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
