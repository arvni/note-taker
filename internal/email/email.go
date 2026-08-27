// Package email sends transactional onboarding messages from a dedicated sender
// address (spec §21). It never uses employee credentials and never places
// tokens/codes in a way that violates §22 — the caller supplies a safe consent
// link built from the onboarding token path.
package email

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"log"
)

// Message is a rendered email ready to send.
type Message struct {
	To       string
	From     string
	Subject  string
	HTMLBody string
}

// Sender delivers a Message via an approved transactional provider (spec §21).
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// LogSender is a development Sender that logs metadata only — never the body,
// which could contain the consent link (spec §22 keeps sensitive items out of
// logs by convention).
type LogSender struct{}

func (LogSender) Send(_ context.Context, m Message) error {
	log.Printf("email: would send to=%s from=%s subject=%q (%d bytes body)",
		m.To, m.From, m.Subject, len(m.HTMLBody))
	return nil
}

// InviteData is the data for the onboarding invitation template (spec §22-23).
type InviteData struct {
	CompanyName    string
	AppName        string
	EmployeeEmail  string
	Permissions    []string
	ConsentURL     string // https://<verified-domain>/connect/<token> (spec §23)
	ExpiresAt      string
	SupportContact string
	PrivacyURL     string
}

// RenderInvite renders the onboarding invitation email from a parsed template.
func RenderInvite(tmpl *template.Template, from string, d InviteData) (Message, error) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "invite.html", d); err != nil {
		return Message{}, fmt.Errorf("email: render invite: %w", err)
	}
	return Message{
		To:       d.EmployeeEmail,
		From:     from,
		Subject:  fmt.Sprintf("Action required: connect your %s Calendar", d.CompanyName),
		HTMLBody: buf.String(),
	}, nil
}
