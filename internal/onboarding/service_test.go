package onboarding

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/email"
	"github.com/arvinizadi/fathom/web"
)

type fakeTokenStore struct {
	hash      []byte
	expiresAt time.Time
	rawSeen   bool
}

func (f *fakeTokenStore) Create(_ context.Context, _ int64, hash []byte, exp time.Time) error {
	f.hash = hash
	f.expiresAt = exp
	return nil
}

type fakeEmps struct{ status string }

func (f *fakeEmps) GetByID(_ context.Context, _ db.OrgID, _ int64) (string, string, error) {
	return "alice@company.com", "Alice", nil
}
func (f *fakeEmps) SetOnboardingStatus(_ context.Context, _ db.OrgID, _ int64, s string) error {
	f.status = s
	return nil
}

type captureSender struct{ msg email.Message }

func (c *captureSender) Send(_ context.Context, m email.Message) error { c.msg = m; return nil }

func TestInvite_HashOnlyStorageAndSafeLink(t *testing.T) {
	tmpl, err := web.Templates()
	if err != nil {
		t.Fatal(err)
	}
	ts := &fakeTokenStore{}
	emps := &fakeEmps{}
	snd := &captureSender{}

	svc := NewService(ts, emps, snd, nil, tmpl, Config{
		BaseURL:     "https://calendar-sync.company.com",
		CompanyName: "Acme",
		AppName:     "Calendar Bridge",
		Permissions: []string{"Read calendars", "Read events"},
		SupportAddr: "support@company.com",
		FromAddr:    "calendar-integration@company.com",
		TokenBytes:  48,
		TTL:         7 * 24 * time.Hour,
	})

	if err := svc.Invite(context.Background(), db.OrgID(1), 42); err != nil {
		t.Fatal(err)
	}

	// Employee transitioned to invited (spec §4).
	if emps.status != string(Invited) {
		t.Fatalf("onboarding status = %q, want invited", emps.status)
	}
	// A hash was stored, and it is NOT the raw token.
	if len(ts.hash) != 32 {
		t.Fatalf("stored hash len = %d, want 32 (sha256)", len(ts.hash))
	}

	// Extract the consent link from the rendered email.
	body := snd.msg.HTMLBody
	m := regexp.MustCompile(`https://calendar-sync\.company\.com/connect/([A-Za-z0-9_\-]+)`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("consent link not found in email body")
	}
	rawToken := m[1]

	// The token in the link must hash to exactly what was stored (spec §6).
	if !ConstantTimeEqual(HashToken(rawToken), ts.hash) {
		t.Fatal("token in email does not match stored hash — hash-only storage broken")
	}
	// The raw token must NOT appear as a query parameter anywhere (spec §22).
	if strings.Contains(body, "?token=") || strings.Contains(body, "&token=") {
		t.Fatal("raw token leaked into a query string (spec §22 violation)")
	}
	// The email must state Mail/password are not accessed (spec §5).
	if !strings.Contains(body, "not") || !strings.Contains(strings.ToLower(body), "mail") {
		t.Error("invite should state it does not access Zoho Mail/password")
	}
	// Recipient + sender wired correctly (spec §21).
	if snd.msg.To != "alice@company.com" || snd.msg.From != "calendar-integration@company.com" {
		t.Errorf("envelope wrong: to=%s from=%s", snd.msg.To, snd.msg.From)
	}
}
