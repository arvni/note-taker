package rbac

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mgr(ttl time.Duration) *SessionManager {
	return NewSessionManager([]byte("test-key-32-bytes-000000000000000"), ttl, true)
}

func TestSessionRoundTrip(t *testing.T) {
	m := mgr(time.Hour)
	rec := httptest.NewRecorder()
	p := Principal{OrgID: 7, Role: OrgAdmin, Email: "admin@company.com"}
	csrf, err := m.Issue(rec, p)
	if err != nil {
		t.Fatal(err)
	}
	// Cookie flags (spec §38).
	sc := rec.Result().Cookies()[0]
	if !sc.HttpOnly || !sc.Secure || sc.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie flags wrong: httponly=%v secure=%v samesite=%v", sc.HttpOnly, sc.Secure, sc.SameSite)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(sc)
	got, gotCSRF, err := m.Verify(req)
	if err != nil {
		t.Fatal(err)
	}
	if got.OrgID != 7 || got.Role != OrgAdmin {
		t.Fatalf("principal mismatch: %+v", got)
	}
	if gotCSRF != csrf {
		t.Fatal("csrf mismatch")
	}
	if err := VerifyCSRF(gotCSRF, csrf); err != nil {
		t.Fatal("valid CSRF rejected")
	}
	if VerifyCSRF(gotCSRF, "wrong") == nil {
		t.Fatal("bad CSRF accepted")
	}
}

func TestSessionTamperRejected(t *testing.T) {
	m := mgr(time.Hour)
	rec := httptest.NewRecorder()
	_, _ = m.Issue(rec, Principal{OrgID: 1, Role: Employee})
	sc := rec.Result().Cookies()[0]
	// Tamper: flip role in the payload by mangling the cookie value.
	sc.Value = strings.Replace(sc.Value, sc.Value[:5], "AAAAA", 1)
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(sc)
	if _, _, err := m.Verify(req); err == nil {
		t.Fatal("tampered session accepted")
	}
}

func TestSessionExpiry(t *testing.T) {
	m := mgr(-time.Minute) // already expired... but ttl<=0 defaults to 30m; force via short
	m2 := NewSessionManager([]byte("k"), time.Millisecond, false)
	rec := httptest.NewRecorder()
	_, _ = m2.Issue(rec, Principal{Role: OrgAdmin})
	time.Sleep(5 * time.Millisecond)
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(rec.Result().Cookies()[0])
	if _, _, err := m2.Verify(req); err == nil {
		t.Fatal("expired session accepted")
	}
	_ = m
}

func TestNoCookie(t *testing.T) {
	m := mgr(time.Hour)
	if _, _, err := m.Verify(httptest.NewRequest("GET", "/", nil)); err != ErrInvalidSession {
		t.Fatal("expected ErrInvalidSession with no cookie")
	}
}
