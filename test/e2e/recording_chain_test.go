// Package e2e holds end-to-end tests that require live external services and are
// skipped unless the necessary credentials are provided.
package e2e

import (
	"os"
	"testing"
)

// TestRecordingChain is the spec §42 integration test: a destination Google
// Calendar event we create must be DETECTED and RECORDED by Fathom. This cannot
// be verified with fakes — it requires a real Fathom account, a real Fathom-
// watched Google Calendar, and time for Fathom to process a live meeting. It is
// therefore gated on real credentials and skipped otherwise.
//
// Required env: FATHOM_API_KEY, GOOGLE_ACCESS_TOKEN, GOOGLE_CALENDAR_ID, and a
// live meeting to join. See docs/security/oauth-security.md and the POC (spec
// §58) for the manual verification procedure.
func TestRecordingChain(t *testing.T) {
	if os.Getenv("FATHOM_E2E") == "" {
		t.Skip("FATHOM_E2E not set: spec §42 recording-chain test requires live Fathom + Google Calendar")
	}
	t.Fatal("live recording-chain verification not yet automated; run the manual §42 procedure")
}
