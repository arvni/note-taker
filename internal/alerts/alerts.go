// Package alerts emails the app's admins when a calendar connection breaks:
// the org's Fathom (Google) destination calendar, or an employee's Zoho
// calendar. It reuses the org's configured SMTP sender (Settings → Email).
package alerts

import (
	"context"
	"fmt"
	"html"
	"log"
	"strings"
	"time"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/email"
	"github.com/arvinizadi/fathom/internal/onboarding"
)

// Notifier sends connection alerts to the admin addresses.
type Notifier struct {
	resolve onboarding.Resolver
	admins  []string
	baseURL string
}

func New(resolve onboarding.Resolver, admins []string, baseURL string) *Notifier {
	return &Notifier{resolve: resolve, admins: admins, baseURL: strings.TrimRight(baseURL, "/")}
}

// DestinationDisconnected tells admins the Fathom calendar can no longer be
// written to, so no meetings will reach Fathom until it is reconnected.
func (n *Notifier) DestinationDisconnected(ctx context.Context, org db.OrgID, account, reason string) {
	who := "the Fathom calendar"
	if account != "" {
		who = "the Fathom calendar (" + account + ")"
	}
	n.send(ctx, org, "Action needed: Fathom calendar disconnected",
		fmt.Sprintf(`<p>The app can no longer write to %s. New and changed meetings are <b>not</b> reaching Fathom until it is reconnected.</p>
<p><b>Reason:</b> %s</p>
<p><a href="%s/app/">Open the admin app</a> → <b>Fathom Calendar</b> → <b>Reconnect</b>, and sign in with the same Google account.</p>
<p style="color:#666">Detected %s. You will get this email once per outage.</p>`,
			html.EscapeString(who), html.EscapeString(reason), n.baseURL, time.Now().UTC().Format("2006-01-02 15:04 UTC")))
}

// DestinationRestored tells admins the Fathom calendar works again.
func (n *Notifier) DestinationRestored(ctx context.Context, org db.OrgID) {
	n.send(ctx, org, "Resolved: Fathom calendar reconnected",
		`<p>The Fathom calendar is connected again. Meetings are syncing normally; anything missed during the outage is caught up automatically.</p>`)
}

// EmployeeDisconnected tells admins an employee's Zoho calendar link broke and
// that the employee was emailed a fresh connect link.
func (n *Notifier) EmployeeDisconnected(ctx context.Context, org db.OrgID, name, addr string, reinvited bool) {
	who := addr
	if name != "" {
		who = name + " (" + addr + ")"
	}
	next := "They were <b>not</b> re-invited automatically — open the admin app and click <b>Invite</b> on their row."
	if reinvited {
		next = "They were emailed a new connect link automatically; their meetings sync again as soon as they reconnect. You can resend it from the admin app."
	}
	n.send(ctx, org, "Calendar disconnected: "+who,
		fmt.Sprintf(`<p>The Zoho calendar connection for <b>%s</b> stopped working (Zoho rejected the saved login — it was revoked, or the app was removed from their Zoho account). Their meetings are no longer synced to Fathom.</p>
<p>%s</p>
<p><a href="%s/app/">Open the admin app</a></p>`,
			html.EscapeString(who), next, n.baseURL))
}

func (n *Notifier) send(ctx context.Context, org db.OrgID, subject, body string) {
	if len(n.admins) == 0 {
		log.Printf("alerts: no ADMIN_EMAILS configured; dropping alert %q", subject)
		return
	}
	sender, brand := n.resolve(ctx, org)
	app := brand.AppName
	if app == "" {
		app = "Calendar Bridge"
	}
	for _, to := range n.admins {
		msg := email.Message{
			To: to, From: brand.FromAddr, Subject: "[" + app + "] " + subject,
			HTMLBody: `<div style="font-family:system-ui,sans-serif;font-size:14px;line-height:1.5">` + body + `</div>`,
		}
		if err := sender.Send(ctx, msg); err != nil {
			log.Printf("alerts: send %q to %s: %v", subject, to, err)
		}
	}
	log.Printf("alerts: sent %q to %d admin(s)", subject, len(n.admins))
}

// Inviter re-sends an employee's connect link (onboarding.Service).
type Inviter interface {
	Reconnect(ctx context.Context, org db.OrgID, employeeID int64) error
}

// EmployeeLookup returns an employee's display name and email.
type EmployeeLookup func(ctx context.Context, org db.OrgID, employeeID int64) (name, email string, err error)

// Reconnector satisfies tokens.Reconnector: when an employee's Zoho token
// becomes permanently invalid it re-invites them and alerts the admins.
type Reconnector struct {
	Invite   Inviter
	Notifier *Notifier
	Lookup   EmployeeLookup
	// OnDisconnect, if set, is called with the employee's email (e.g. so the
	// worker can list disconnections in its run report).
	OnDisconnect func(email string)
}

func (r *Reconnector) Reconnect(ctx context.Context, org db.OrgID, employeeID int64) error {
	var inviteErr error
	if r.Invite != nil {
		inviteErr = r.Invite.Reconnect(ctx, org, employeeID)
		if inviteErr != nil {
			log.Printf("alerts: re-invite employee=%d: %v", employeeID, inviteErr)
		}
	}
	name, addr := "", fmt.Sprintf("employee #%d", employeeID)
	if r.Lookup != nil {
		if n, e, err := r.Lookup(ctx, org, employeeID); err == nil {
			name, addr = n, e
		}
	}
	if r.Notifier != nil {
		r.Notifier.EmployeeDisconnected(ctx, org, name, addr, r.Invite != nil && inviteErr == nil)
	}
	if r.OnDisconnect != nil {
		r.OnDisconnect(addr)
	}
	return inviteErr
}
