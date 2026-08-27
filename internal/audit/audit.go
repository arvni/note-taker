// Package audit records security-sensitive actions (spec §35). Token values
// MUST NEVER be written to the audit log; this package actively strips any
// field whose key looks like a secret before persisting.
package audit

import (
	"context"
	"strings"
)

// Action enumerates auditable events (spec §35).
type Action string

const (
	InvitationSent     Action = "employee_invitation_sent"
	InvitationOpened   Action = "employee_invitation_opened"
	OAuthStarted       Action = "oauth_started"
	OAuthCompleted     Action = "oauth_completed"
	OAuthFailed        Action = "oauth_failed"
	TokenRefreshed     Action = "token_refreshed"
	TokenRefreshFailed Action = "token_refresh_failed"
	CalendarConnected  Action = "calendar_connected"
	CalendarDisconnect Action = "calendar_disconnected"
	CalendarEnabled    Action = "calendar_enabled"
	CalendarDisabled   Action = "calendar_disabled"
	MeetingSynced      Action = "meeting_synchronized"
	EmployeeRevoked    Action = "employee_revoked"
	EmployeeDisabled   Action = "employee_disabled"
	AdminReconnected   Action = "admin_reconnected"
)

// Entry is a single audit record.
type Entry struct {
	OrganizationID int64
	EmployeeID     int64
	Actor          string
	Action         Action
	Metadata       map[string]any
}

// Sink persists audit entries (e.g. Postgres). Implementations receive metadata
// already sanitized by Redact.
type Sink interface {
	Write(ctx context.Context, e Entry) error
}

// forbiddenKeys are metadata keys that must never be persisted (spec §35, §57).
var forbiddenSubstrings = []string{
	"token", "secret", "password", "authorization_code", "client_secret",
	"access_token", "refresh_token", "ciphertext", "code", "state",
}

// Redact returns a copy of md with any secret-like key removed. Exported so it
// can be unit-tested directly.
func Redact(md map[string]any) map[string]any {
	if md == nil {
		return nil
	}
	out := make(map[string]any, len(md))
	for k, v := range md {
		if isForbidden(k) {
			out[k] = "[REDACTED]"
			continue
		}
		out[k] = v
	}
	return out
}

func isForbidden(key string) bool {
	lk := strings.ToLower(key)
	for _, sub := range forbiddenSubstrings {
		if strings.Contains(lk, sub) {
			return true
		}
	}
	return false
}

// Logger writes sanitized entries to a Sink.
type Logger struct{ sink Sink }

func New(sink Sink) *Logger { return &Logger{sink: sink} }

// Log sanitizes metadata and writes the entry.
func (l *Logger) Log(ctx context.Context, e Entry) error {
	e.Metadata = Redact(e.Metadata)
	return l.sink.Write(ctx, e)
}
