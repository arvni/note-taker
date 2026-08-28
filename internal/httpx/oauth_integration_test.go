package httpx

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/arvinizadi/fathom/internal/audit"
	"github.com/arvinizadi/fathom/internal/crypto"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/directory"
	"github.com/arvinizadi/fathom/internal/oauth"
	"github.com/arvinizadi/fathom/internal/onboarding"
	"github.com/arvinizadi/fathom/internal/tokens"
	"github.com/arvinizadi/fathom/web"
)

func testPool(t *testing.T) *db.Pool {
	t.Helper()
	u := os.Getenv("DATABASE_URL")
	if u == "" {
		t.Skip("DATABASE_URL not set; skipping OAuth flow integration test")
	}
	pool, err := db.Connect(context.Background(), u)
	if err != nil {
		t.Skipf("cannot connect to DB: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func randKey(t *testing.T) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// fakeZoho serves the token and userinfo endpoints. identity controls the
// returned ZUID/Email so we can test both match and mismatch.
func fakeZoho(t *testing.T, zuid, email string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/v2/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "acc-" + zuid,
			"refresh_token": "ref-" + zuid,
			"api_domain":    "https://www.zohoapis.com",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	})
	mux.HandleFunc("/oauth/user/info", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"ZUID": zuid, "Email": email, "Display_Name": "Test User",
		})
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func seed(t *testing.T, pool *db.Pool, email string) (empID int64, org db.OrgID) {
	ctx := context.Background()
	var oid int64
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ('oauth-itest') RETURNING id`).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	var eid int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO employees (organization_id, email, status, onboarding_status)
		VALUES ($1, $2, 'active', 'invited') RETURNING id`, oid, email).Scan(&eid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM oauth_credentials WHERE employee_id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM oauth_states WHERE employee_id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM onboarding_tokens WHERE employee_id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE employee_id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM employees WHERE id=$1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, oid)
	})
	return eid, db.OrgID(oid)
}

func buildApp(t *testing.T, pool *db.Pool, zohoBase string) (*http.ServeMux, *onboarding.Repo, *tokens.Store) {
	cipher, err := crypto.NewFromBase64Key(randKey(t))
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := web.Templates()
	if err != nil {
		t.Fatal(err)
	}
	onb := onboarding.NewRepo(pool)
	creds := tokens.NewStore(pool, cipher)
	oc := oauth.OrgConfig{ClientID: "cid", ClientSecret: "csecret",
		RedirectURI: "https://app.example/oauth/zoho/callback", AccountsBase: zohoBase, CalendarBase: zohoBase,
		Scopes: []string{"ZohoCalendar.calendar.READ", "ZohoCalendar.event.READ", "aaaserver.profile.READ"}}
	h := NewOAuthHandler(OAuthDeps{
		Onboarding: onb, States: oauth.NewStateRepo(pool), Employees: directory.NewRepo(pool),
		Creds: creds, Zoho: func(context.Context, db.OrgID) (oauth.OrgConfig, error) { return oc, nil },
		Audit: audit.New(audit.NewPostgresSink(pool)), Templates: tmpl,
		CompanyName: "Acme", AppName: "Bridge", Permissions: []string{"Read calendars"},
	})
	mux := http.NewServeMux()
	h.Register(mux)
	return mux, onb, creds
}

func TestOAuthFlow_HappyPath(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	email := "flow@company.com"
	empID, _ := seed(t, pool, email)

	z := fakeZoho(t, "100200300", email)
	mux, onb, creds := buildApp(t, pool, z.URL)

	// Mint an onboarding token for this employee.
	raw, hash, _ := onboarding.GenerateToken(48)
	if err := onb.Create(ctx, empID, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	// 1) consent page
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/connect/"+raw, nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), email) {
		t.Fatalf("consent page: code=%d body?=%v", rec.Code, strings.Contains(rec.Body.String(), email))
	}

	// 2) start → 302 to Zoho authorize with a state param
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/oauth/zoho/start?t="+url.QueryEscape(raw), nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("start: code=%d, want 302", rec.Code)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	state := loc.Query().Get("state")
	if state == "" {
		t.Fatal("no state in authorize redirect")
	}

	// onboarding token is now single-used: re-start must fail
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/oauth/zoho/start?t="+url.QueryEscape(raw), nil))
	if rec2.Code == http.StatusFound {
		t.Fatal("onboarding token was reusable at /start (spec §7 violation)")
	}

	// 3) callback → 200 connected
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/oauth/zoho/callback?state="+url.QueryEscape(state)+"&code=authcode", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "successful") {
		t.Fatalf("callback: code=%d body=%q", rec.Code, rec.Body.String())
	}

	// Assert DB state: employee authorized + zoho id bound
	var status, zohoID string
	if err := pool.QueryRow(ctx, `SELECT onboarding_status, coalesce(zoho_user_id,'') FROM employees WHERE id=$1`, empID).
		Scan(&status, &zohoID); err != nil {
		t.Fatal(err)
	}
	if status != "authorized" {
		t.Fatalf("employee status = %q, want authorized", status)
	}
	if zohoID != "100200300" {
		t.Fatalf("zoho_user_id not bound: %q", zohoID)
	}

	// Credential stored & decrypts to the fake tokens
	cred, err := creds.Get(ctx, empID)
	if err != nil {
		t.Fatal(err)
	}
	if cred.AccessToken != "acc-100200300" || cred.RefreshToken != "ref-100200300" {
		t.Fatalf("credential decrypt mismatch: %+v", cred)
	}
	// Ciphertext at rest is NOT the plaintext (spec §13)
	var accCT string
	_ = pool.QueryRow(ctx, `SELECT access_token_ciphertext FROM oauth_credentials WHERE employee_id=$1`, empID).Scan(&accCT)
	if strings.Contains(accCT, "acc-100200300") {
		t.Fatal("access token stored in plaintext (spec §13 violation)")
	}

	// Replayed state must fail (single-use, spec §9)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/oauth/zoho/callback?state="+url.QueryEscape(state)+"&code=authcode", nil))
	if rec.Code == 200 {
		t.Fatal("replayed state was accepted (spec §9 violation)")
	}
}

func TestOAuthFlow_IdentityMismatch(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	empID, _ := seed(t, pool, "real@company.com")

	// Zoho returns a DIFFERENT email/zuid than the employee record.
	z := fakeZoho(t, "999", "attacker@company.com")
	mux, onb, _ := buildApp(t, pool, z.URL)

	raw, hash, _ := onboarding.GenerateToken(48)
	_ = onb.Create(ctx, empID, hash, time.Now().Add(time.Hour))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/oauth/zoho/start?t="+url.QueryEscape(raw), nil))
	state, _ := url.Parse(rec.Header().Get("Location"))
	st := state.Query().Get("state")

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/oauth/zoho/callback?state="+url.QueryEscape(st)+"&code=authcode", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("identity mismatch: code=%d, want 403", rec.Code)
	}
	var status string
	_ = pool.QueryRow(ctx, `SELECT onboarding_status FROM employees WHERE id=$1`, empID).Scan(&status)
	if status != "authorization_failed" {
		t.Fatalf("status = %q, want authorization_failed", status)
	}
	// No credential should have been stored.
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM oauth_credentials WHERE employee_id=$1`, empID).Scan(&n)
	if n != 0 {
		t.Fatal("credential stored despite identity mismatch (spec §25 violation)")
	}
}
