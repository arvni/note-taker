package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/arvinizadi/fathom/internal/calendar"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/directory"
	"github.com/arvinizadi/fathom/internal/rbac"
)

type nopInviter struct{}

func (nopInviter) Invite(context.Context, db.OrgID, int64) error { return nil }

func TestAPIv1(t *testing.T) {
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
	_ = pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ('apiv1-itest') RETURNING id`).Scan(&oid)
	_, _ = pool.Exec(ctx, `INSERT INTO employees (organization_id, email, status, onboarding_status)
		VALUES ($1,'a@company.com','active','authorized'),($1,'b@company.com','active','invited')`, oid)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM employees WHERE organization_id=$1`, oid)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, oid)
	})

	empRepo := directory.NewRepo(pool)
	sessions := rbac.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour, false)
	auth := NewAuthMiddleware(sessions)
	mux := http.NewServeMux()
	NewAPIv1(empRepo, calendar.NewRepo(pool), directory.NewReconciler(empRepo, nil, nil), &nopRevoker{}, nopInviter{}, auth).Register(mux)

	rec := httptest.NewRecorder()
	_, _ = sessions.Issue(rec, rbac.Principal{OrgID: oid, Role: rbac.OrgAdmin, Email: "admin@company.com"})
	cookie := rec.Result().Cookies()[0]

	do := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(cookie)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, r)
		return rr
	}

	// /me
	if rr := do("/api/v1/me"); rr.Code != 200 {
		t.Fatalf("/me: %d", rr.Code)
	}
	// /stats
	rr := do("/api/v1/stats")
	if rr.Code != 200 {
		t.Fatalf("/stats: %d", rr.Code)
	}
	var stats map[string]int
	_ = json.Unmarshal(rr.Body.Bytes(), &stats)
	if stats["Total"] != 2 || stats["Connected"] != 1 {
		t.Fatalf("stats wrong: %+v", stats)
	}
	// /employees
	rr = do("/api/v1/employees")
	var el struct{ Employees []directory.EmployeeRow }
	_ = json.Unmarshal(rr.Body.Bytes(), &el)
	if len(el.Employees) != 2 {
		t.Fatalf("employees: got %d, want 2", len(el.Employees))
	}
	// unauthenticated -> 401
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, httptest.NewRequest("GET", "/api/v1/stats", nil))
	if rr2.Code != 401 {
		t.Fatalf("unauth /stats: %d, want 401", rr2.Code)
	}
	// employee role -> 403 on org stats
	rec3 := httptest.NewRecorder()
	_, _ = sessions.Issue(rec3, rbac.Principal{OrgID: oid, Role: rbac.Employee, EmployeeID: 1})
	r3 := httptest.NewRequest("GET", "/api/v1/stats", nil)
	r3.AddCookie(rec3.Result().Cookies()[0])
	rr3 := httptest.NewRecorder()
	mux.ServeHTTP(rr3, r3)
	if rr3.Code != 403 {
		t.Fatalf("employee /stats: %d, want 403", rr3.Code)
	}
}

func TestSPAServing(t *testing.T) {
	spaFS := os.DirFS("../../web/spa/dist")
	mux := http.NewServeMux()
	NewSPAHandler(spaFS).Register(mux)

	// index
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/app/", nil))
	if rr.Code != 200 || rr.Body.Len() == 0 {
		t.Fatalf("/app/: %d len=%d", rr.Code, rr.Body.Len())
	}
	// unknown route falls back to index (SPA routing)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/app/some/deep/route", nil))
	if rr.Code != 200 {
		t.Fatalf("spa fallback: %d, want 200", rr.Code)
	}
	// /admin redirects to /app/
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/admin", nil))
	if rr.Code != http.StatusFound {
		t.Fatalf("/admin redirect: %d, want 302", rr.Code)
	}
}
