package audit

import (
	"context"
	"testing"
)

type memSink struct{ last Entry }

func (m *memSink) Write(_ context.Context, e Entry) error { m.last = e; return nil }

func TestRedactStripsSecrets(t *testing.T) {
	md := map[string]any{
		"employee_id":   int64(5),
		"access_token":  "1000.secret.value",
		"refresh_token": "1000.refresh",
		"client_secret": "shh",
		"state":         "csrf-value",
		"calendar_name": "Work",
	}
	got := Redact(md)
	for _, k := range []string{"access_token", "refresh_token", "client_secret", "state"} {
		if got[k] != "[REDACTED]" {
			t.Errorf("key %q not redacted: %v", k, got[k])
		}
	}
	if got["calendar_name"] != "Work" {
		t.Errorf("non-secret field wrongly altered: %v", got["calendar_name"])
	}
}

func TestLoggerRedactsBeforeSink(t *testing.T) {
	s := &memSink{}
	l := New(s)
	_ = l.Log(context.Background(), Entry{
		Action:   OAuthCompleted,
		Metadata: map[string]any{"refresh_token": "leak", "ok": true},
	})
	if s.last.Metadata["refresh_token"] != "[REDACTED]" {
		t.Fatal("token reached sink unredacted")
	}
}
