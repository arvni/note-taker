package alerts

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/email"
	"github.com/arvinizadi/fathom/internal/onboarding"
)

type capture struct{ msgs []email.Message }

func (c *capture) Send(_ context.Context, m email.Message) error { c.msgs = append(c.msgs, m); return nil }

type fakeInviter struct{ err error }

func (f fakeInviter) Reconnect(context.Context, db.OrgID, int64) error { return f.err }

func newNotifier(c *capture) *Notifier {
	return New(func(context.Context, db.OrgID) (email.Sender, onboarding.Brand) {
		return c, onboarding.Brand{AppName: "Bridge", FromAddr: "noreply@x.com"}
	}, []string{"a@x.com", "b@x.com"}, "https://app.example.com/")
}

func TestReconnector_ReinvitesAndAlertsAdmins(t *testing.T) {
	c := &capture{}
	var got []string
	r := &Reconnector{
		Invite:   fakeInviter{},
		Notifier: newNotifier(c),
		Lookup: func(context.Context, db.OrgID, int64) (string, string, error) {
			return "Jane Doe", "jane@x.com", nil
		},
		OnDisconnect: func(e string) { got = append(got, e) },
	}
	if err := r.Reconnect(context.Background(), 1, 42); err != nil {
		t.Fatal(err)
	}
	if len(c.msgs) != 2 {
		t.Fatalf("want one alert per admin (2), got %d", len(c.msgs))
	}
	m := c.msgs[0]
	if !strings.Contains(m.Subject, "Jane Doe (jane@x.com)") || !strings.Contains(m.Subject, "[Bridge]") {
		t.Errorf("subject = %q", m.Subject)
	}
	if !strings.Contains(m.HTMLBody, "emailed a new connect link") || !strings.Contains(m.HTMLBody, "https://app.example.com/app/") {
		t.Errorf("body missing re-invite note or link: %s", m.HTMLBody)
	}
	if len(got) != 1 || got[0] != "jane@x.com" {
		t.Errorf("OnDisconnect got %v", got)
	}
}

func TestReconnector_InviteFailureStillAlerts(t *testing.T) {
	c := &capture{}
	r := &Reconnector{Invite: fakeInviter{err: errors.New("smtp down")}, Notifier: newNotifier(c)}
	if err := r.Reconnect(context.Background(), 1, 7); err == nil {
		t.Fatal("want the invite error returned")
	}
	if len(c.msgs) != 2 || !strings.Contains(c.msgs[0].HTMLBody, "not</b> re-invited") {
		t.Fatalf("admins should still be alerted with manual instructions: %+v", c.msgs)
	}
}

func TestDestinationDisconnected_EscapesReason(t *testing.T) {
	c := &capture{}
	newNotifier(c).DestinationDisconnected(context.Background(), 1, "cal@gmail.com", `invalid_grant <script>`)
	if len(c.msgs) != 2 || strings.Contains(c.msgs[0].HTMLBody, "<script>") {
		t.Fatalf("reason must be escaped: %+v", c.msgs)
	}
}
