package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/arvinizadi/fathom/internal/calendar"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/directory"
	"github.com/arvinizadi/fathom/internal/rbac"
)

type nopRevoker struct{ called bool }

func (n *nopRevoker) Revoke(context.Context, db.OrgID, int64) error { n.called = true; return nil }

func TestDashboardRBAC(t *testing.T) {
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
	_ = pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ('dash-itest') RETURNING id`).Scan(&oid)
	_ = pool.QueryRow(ctx, `INSERT INTO employees (organization_id, email, status, onboarding_status)
		VALUES ($1,'emp@company.com','active','authorized') RETURNING id`, oid).Scan(&eid)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM calendars WHERE employee_id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM employees WHERE id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, oid)
	})

	tmpl, _ := webTemplates(t)
	sessions := rbac.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour, false)
	auth := NewAuthMiddleware(sessions)
	rev := &nopRevoker{}
	h := NewDashboardHandler(DashboardDeps{
		Employees: directory.NewRepo(pool), Calendars: calendar.NewRepo(pool),
		Revoker: rev, Auth: auth, Templates: tmpl, CompanyName: "Acme",
	})
	mux := http.NewServeMux()
	h.Register(mux)

	issue := func(p rbac.Principal) (*http.Cookie, string) {
		rec := httptest.NewRecorder()
		csrf, _ := sessions.Issue(rec, p)
		return rec.Result().Cookies()[0], csrf
	}

	// Unauthenticated → 401.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/admin", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth /admin: %d", rec.Code)
	}

	// Employee cannot access /admin → 403.
	empCookie, _ := issue(rbac.Principal{OrgID: oid, Role: rbac.Employee, EmployeeID: eid})
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin", nil)
	req.AddCookie(empCookie)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("employee /admin: %d, want 403", rec.Code)
	}

	// Admin can access /admin → 200.
	adminCookie, _ := issue(rbac.Principal{OrgID: oid, Role: rbac.OrgAdmin})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/admin", nil)
	req.AddCookie(adminCookie)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Acme") {
		t.Fatalf("admin /admin: %d", rec.Code)
	}

	// Employee /me → 200.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/me", nil)
	req.AddCookie(empCookie)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("employee /me: %d", rec.Code)
	}

	// POST /me/disconnect WITHOUT CSRF → 403.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/me/disconnect", nil)
	req.AddCookie(empCookie)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disconnect without CSRF: %d, want 403", rec.Code)
	}
	if rev.called {
		t.Fatal("revoke happened despite missing CSRF")
	}

	// POST /me/disconnect WITH CSRF → 303 and revoke called.
	empCookie2, csrf2 := issue(rbac.Principal{OrgID: oid, Role: rbac.Employee, EmployeeID: eid})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/me/disconnect", strings.NewReader("csrf_token="+csrf2))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(empCookie2)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("disconnect with CSRF: %d, want 303", rec.Code)
	}
	if !rev.called {
		t.Fatal("revoke not called on valid self-disconnect")
	}
}
