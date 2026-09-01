package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/arvinizadi/fathom/internal/audit"
	"github.com/arvinizadi/fathom/internal/calendar"
	"github.com/arvinizadi/fathom/internal/db"
)

func TestFathomWebhook_ConfirmsRecordingChain(t *testing.T) {
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
	_ = pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ('fathom-itest') RETURNING id`).Scan(&oid)
	_ = pool.QueryRow(ctx, `INSERT INTO employees (organization_id, email, status, onboarding_status)
		VALUES ($1,'f@company.com','active','authorized') RETURNING id`, oid).Scan(&eid)
	meetingURL := "https://meet.google.com/rec-chain-1"
	_, _ = pool.Exec(ctx, `INSERT INTO event_mappings (employee_id, calendar_uid, source_event_id, title, meeting_url)
		VALUES ($1,'work','src-1','Client call',$2)`, eid, meetingURL)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM event_mappings WHERE employee_id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE employee_id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM employees WHERE id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, oid)
	})

	repo := calendar.NewRepo(pool)
	h := NewFathomHandler(repo, audit.New(audit.NewPostgresSink(pool)), "")
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"event":"meeting.processed","meeting_url":"` + meetingURL + `","recording_id":"rec-99","title":"Client call"}`
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/webhooks/fathom", strings.NewReader(body)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("webhook: code=%d, want 204", rec.Code)
	}

	// The recording chain is confirmed via an audit entry tied to the employee.
	var cnt int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_log
		WHERE employee_id=$1 AND action='fathom_meeting_recorded'`, eid).Scan(&cnt)
	if cnt != 1 {
		t.Fatalf("expected 1 fathom_meeting_recorded audit entry, got %d", cnt)
	}

	// A webhook for an unknown meeting_url is accepted but records no match.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/webhooks/fathom",
		strings.NewReader(`{"meeting_url":"https://meet.google.com/unknown","recording_id":"r2"}`)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unknown webhook: code=%d, want 204", rec.Code)
	}
}
