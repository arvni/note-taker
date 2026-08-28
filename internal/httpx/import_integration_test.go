package httpx

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/directory"
	"github.com/arvinizadi/fathom/internal/rbac"
)

func TestCSVImportEndpoint(t *testing.T) {
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
	_ = pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ('import-itest') RETURNING id`).Scan(&oid)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM onboarding_tokens WHERE employee_id IN (SELECT id FROM employees WHERE organization_id=$1)`, oid)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE organization_id=$1`, oid)
		_, _ = pool.Exec(ctx, `DELETE FROM employees WHERE organization_id=$1`, oid)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, oid)
	})

	empRepo := directory.NewRepo(pool)
	// Reconciler with nil inviter/offboarder (no email in test) — creates records.
	reconciler := directory.NewReconciler(empRepo, nil, nil)
	tmpl, _ := webTemplates(t)
	sessions := rbac.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour, false)
	auth := NewAuthMiddleware(sessions)
	mux := http.NewServeMux()
	NewImportHandler(reconciler, auth, tmpl).Register(mux)

	// admin session + CSRF
	rec := httptest.NewRecorder()
	csrf, _ := sessions.Issue(rec, rbac.Principal{OrgID: oid, Role: rbac.OrgAdmin})
	cookie := rec.Result().Cookies()[0]

	// build multipart CSV upload
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("csrf_token", csrf)
	fw, _ := mw.CreateFormFile("file", "employees.csv")
	fw.Write([]byte("email,name,department,status\nalice@company.com,Alice,Sales,active\nbob@company.com,Bob,Eng,active\nbad-row,,,\n"))
	mw.Close()

	req := httptest.NewRequest("POST", "/admin/employees/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("import: code=%d, want 200", rec.Code)
	}
	// Two valid employees created in the org.
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM employees WHERE organization_id=$1`, oid).Scan(&n)
	if n != 2 {
		t.Fatalf("expected 2 employees created, got %d", n)
	}

	// Non-admin (employee) is forbidden.
	rec2 := httptest.NewRecorder()
	empCsrf, _ := sessions.Issue(rec2, rbac.Principal{OrgID: oid, Role: rbac.Employee, EmployeeID: 1})
	empCookie := rec2.Result().Cookies()[0]
	var b2 bytes.Buffer
	mw2 := multipart.NewWriter(&b2)
	mw2.WriteField("csrf_token", empCsrf)
	mw2.Close()
	req2 := httptest.NewRequest("POST", "/admin/employees/import", &b2)
	req2.Header.Set("Content-Type", mw2.FormDataContentType())
	req2.AddCookie(empCookie)
	rec2 = httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("employee import: code=%d, want 403", rec2.Code)
	}
}
