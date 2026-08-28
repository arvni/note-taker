package tokens

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/arvinizadi/fathom/internal/audit"
	"github.com/arvinizadi/fathom/internal/crypto"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/directory"
	"github.com/arvinizadi/fathom/internal/oauth"
)

// compile-time: *Offboarder must satisfy directory.Offboarder.
var _ directory.Offboarder = (*Offboarder)(nil)

func randKey(t *testing.T) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func TestOffboard_RevokesDeletesDisables(t *testing.T) {
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
	_ = pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ('off-itest') RETURNING id`).Scan(&oid)
	var eid int64
	_ = pool.QueryRow(ctx, `INSERT INTO employees (organization_id, email, status, onboarding_status)
		VALUES ($1,'off@company.com','inactive','authorized') RETURNING id`, oid).Scan(&eid)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM oauth_credentials WHERE employee_id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE employee_id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM employees WHERE id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, oid)
	})

	cipher, _ := crypto.NewFromBase64Key(randKey(t))
	store := NewStore(pool, cipher)
	_ = store.Upsert(ctx, Credential{EmployeeID: eid, ProviderAccountID: "z1",
		AccessToken: "acc", RefreshToken: "ref", AccessExpiresAt: time.Now().Add(time.Hour), Status: StatusActive})

	revoked := false
	z := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/v2/token/revoke" {
			revoked = true
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(z.Close)
	client := oauth.NewClient("c", "s", "cb", z.URL, nil)

	off := NewOffboarder(store, func(context.Context, db.OrgID) (ZohoRevoker, error) { return client, nil }, directory.NewRepo(pool), audit.New(audit.NewPostgresSink(pool)))
	org := db.OrgID(oid)

	if err := off.Offboard(ctx, org, eid); err != nil {
		t.Fatal(err)
	}
	if !revoked {
		t.Error("Zoho revoke not called during offboarding (spec §19)")
	}
	// Credential deleted (tokens removed).
	var cnt int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM oauth_credentials WHERE employee_id=$1`, eid).Scan(&cnt)
	if cnt != 0 {
		t.Fatalf("stored tokens not removed: count=%d (spec §19)", cnt)
	}
	// Employee marked disabled.
	var status string
	_ = pool.QueryRow(ctx, `SELECT onboarding_status FROM employees WHERE id=$1`, eid).Scan(&status)
	if status != "disabled" {
		t.Fatalf("employee status=%q, want disabled", status)
	}
	// Audit preserved.
	var actions int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE employee_id=$1 AND action='employee_disabled'`, eid).Scan(&actions)
	if actions != 1 {
		t.Fatalf("expected 1 employee_disabled audit entry, got %d", actions)
	}

	// Idempotent: offboarding again (no credential) must not error.
	if err := off.Offboard(ctx, org, eid); err != nil {
		t.Fatalf("second offboard should be a no-op, got: %v", err)
	}
}
