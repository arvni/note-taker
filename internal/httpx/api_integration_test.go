package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/arvinizadi/fathom/internal/audit"
	"github.com/arvinizadi/fathom/internal/crypto"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/directory"
	"github.com/arvinizadi/fathom/internal/oauth"
	"github.com/arvinizadi/fathom/internal/tokens"
)

func TestRevokeEndpoint(t *testing.T) {
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

	// Seed org + authorized employee + credential.
	var oid int64
	_ = pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ('revoke-itest') RETURNING id`).Scan(&oid)
	var eid int64
	_ = pool.QueryRow(ctx, `INSERT INTO employees (organization_id, email, status, onboarding_status)
		VALUES ($1,'rev@company.com','active','authorized') RETURNING id`, oid).Scan(&eid)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM oauth_credentials WHERE employee_id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE employee_id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM employees WHERE id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, oid)
	})

	cipher, _ := crypto.NewFromBase64Key(randKey(t))
	store := tokens.NewStore(pool, cipher)
	_ = store.Upsert(ctx, tokens.Credential{EmployeeID: eid, ProviderAccountID: "z1",
		AccessToken: "acc", RefreshToken: "ref", AccessExpiresAt: time.Now().Add(time.Hour),
		Status: tokens.StatusActive})

	// Fake Zoho revoke endpoint (records the call).
	revoked := false
	z := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/v2/token/revoke" {
			revoked = true
			w.WriteHeader(200)
		}
	}))
	t.Cleanup(z.Close)
	client := oauth.NewClient("cid", "sec", "https://app/cb", z.URL, nil)

	revoker := tokens.NewRevoker(store, client, directory.NewRepo(pool), audit.New(audit.NewPostgresSink(pool)))
	mux := http.NewServeMux()
	NewAPIHandler(revoker).Register(mux)

	// Without org context → 401.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/employees/"+itoa(eid)+"/revoke", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-org revoke: code=%d, want 401", rec.Code)
	}

	// With org context → 204, credential + employee revoked, Zoho revoke called.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/employees/"+itoa(eid)+"/revoke", nil)
	req = req.WithContext(WithOrg(req.Context(), db.OrgID(oid)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: code=%d, want 204", rec.Code)
	}
	if !revoked {
		t.Error("Zoho revoke endpoint was not called (spec §17)")
	}
	var credStatus, onbStatus string
	_ = pool.QueryRow(ctx, `SELECT status FROM oauth_credentials WHERE employee_id=$1`, eid).Scan(&credStatus)
	_ = pool.QueryRow(ctx, `SELECT onboarding_status FROM employees WHERE id=$1`, eid).Scan(&onbStatus)
	if credStatus != tokens.StatusRevoked || onbStatus != "revoked" {
		t.Fatalf("statuses not revoked: cred=%q onb=%q", credStatus, onbStatus)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
