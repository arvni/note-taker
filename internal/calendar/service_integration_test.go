package calendar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/arvinizadi/fathom/internal/db"
)

type fakeTP struct{}

func (fakeTP) AccessToken(context.Context, db.OrgID, int64) (string, error) { return "tok", nil }

type fakePP struct{ empEnabled bool }

func (p fakePP) RecordingSettings(context.Context, db.OrgID, int64) (bool, bool, bool, error) {
	return p.empEnabled, false, false, nil
}

func fakeCalendarAPI(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/calendars", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"calendars": []map[string]any{
			{"uid": "work-uid", "name": "Work Calendar", "type": "user", "timezone": "UTC"},
			{"uid": "pers-uid", "name": "My Personal", "type": "personal", "timezone": "UTC"},
		}})
	})
	mux.HandleFunc("/calendars/work-uid/events", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"events": []map[string]any{
			{"uid": "e1", "title": "Client call", "location": "https://meet.google.com/abc-defg-hij", "start": "20260901T130000Z"},
			{"uid": "e2", "title": "Secret 1:1", "location": "https://meet.google.com/xyz-1234-abc", "isprivate": true},
			{"uid": "e3", "title": "Desk work", "location": "Room 2"},
		}})
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func TestDiscoverAndScan(t *testing.T) {
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

	var oid int64
	_ = pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ('cal-itest') RETURNING id`).Scan(&oid)
	var eid int64
	_ = pool.QueryRow(ctx, `INSERT INTO employees (organization_id, email, status, onboarding_status)
		VALUES ($1,'cal@company.com','active','authorized') RETURNING id`, oid).Scan(&eid)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM event_mappings WHERE employee_id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM calendars WHERE employee_id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM employees WHERE id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, oid)
	})

	zoho := fakeCalendarAPI(t)
	repo := NewRepo(pool)
	svc := NewService(fakeTP{}, func(context.Context, db.OrgID) (string, error) { return zoho.URL, nil }, repo, fakePP{empEnabled: true}, nil, false)
	org := db.OrgID(oid)

	// Discover: 2 calendars, work enabled, personal disabled.
	n, err := svc.Discover(ctx, org, eid)
	if err != nil || n != 2 {
		t.Fatalf("discover: n=%d err=%v", n, err)
	}
	enabled, err := repo.ListEnabledCalendars(ctx, eid)
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 1 || enabled[0].UID != "work-uid" {
		t.Fatalf("expected only work calendar enabled, got %+v", enabled)
	}

	// Scan: only e1 qualifies (e2 private, e3 no meeting; personal calendar skipped).
	stored, err := svc.Scan(ctx, org, eid)
	if err != nil {
		t.Fatal(err)
	}
	if stored != 1 {
		t.Fatalf("stored=%d, want 1 qualifying meeting", stored)
	}

	var srcID, provider, mtgURL, title string
	if err := pool.QueryRow(ctx, `SELECT source_event_id, meeting_provider, meeting_url, title
		FROM event_mappings WHERE employee_id=$1`, eid).Scan(&srcID, &provider, &mtgURL, &title); err != nil {
		t.Fatal(err)
	}
	if srcID != "e1" || provider != "google_meet" || title != "Client call" {
		t.Fatalf("wrong stored event: id=%s provider=%s title=%s", srcID, provider, title)
	}

	// Data minimization: no description/attendee columns exist to leak (spec §32).
	// Re-scan is idempotent (spec §48): still exactly 1 row.
	if _, err := svc.Scan(ctx, org, eid); err != nil {
		t.Fatal(err)
	}
	var cnt int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM event_mappings WHERE employee_id=$1`, eid).Scan(&cnt)
	if cnt != 1 {
		t.Fatalf("re-scan created duplicates: count=%d", cnt)
	}
}
