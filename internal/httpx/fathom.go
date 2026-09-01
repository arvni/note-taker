package httpx

import (
	"context"
	"io"
	"log"
	"net/http"

	"github.com/arvinizadi/fathom/internal/audit"
	"github.com/arvinizadi/fathom/internal/calendar"
	"github.com/arvinizadi/fathom/internal/fathom"
)

// RecordingMatcher correlates a Fathom meeting_url to a synced meeting and stores
// the recording link back onto it (spec §42-43).
type RecordingMatcher interface {
	FindByMeetingURL(ctx context.Context, meetingURL string) (*calendar.RecordedMatch, bool, error)
	SaveRecording(ctx context.Context, meetingURL, recordingID, recordingURL string, hasTranscript, hasSummary bool) (int64, error)
}

// FathomHandler receives Fathom webhooks and confirms the recording chain: a
// destination event we created was detected and recorded by Fathom (spec §42).
// It does not persist transcript/summary bodies (spec §32 data minimization);
// storage, if required, is governed by the company retention policy (spec §50).
type FathomHandler struct {
	matcher RecordingMatcher
	audit   *audit.Logger
	secret  string // Fathom webhook signing secret (whsec_...); verifies authenticity
}

func NewFathomHandler(matcher RecordingMatcher, auditLog *audit.Logger, secret string) *FathomHandler {
	return &FathomHandler{matcher: matcher, audit: auditLog, secret: secret}
}

func (h *FathomHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /webhooks/fathom", h.receive)
}

func (h *FathomHandler) receive(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	// Verify the signature when a signing secret is configured (spec §38): reject
	// forged webhooks. Unset secret keeps the endpoint open (dev/back-compat).
	if h.secret != "" {
		if err := fathom.VerifyWebhook(h.secret, r.Header, body); err != nil {
			log.Printf("fathom webhook: signature rejected: %v", err)
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}
	payload, err := fathom.ParseWebhook(body)
	if err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	// Correlate the recorded meeting back to a synced event, store the recording
	// link on it, and confirm the chain (spec §42-43).
	if payload.MeetingURL != "" && h.matcher != nil {
		match, found, err := h.matcher.FindByMeetingURL(r.Context(), payload.MeetingURL)
		if err != nil {
			log.Printf("fathom webhook: match: %v", err)
		} else if found {
			if _, err := h.matcher.SaveRecording(r.Context(), payload.MeetingURL, payload.RecordingID,
				payload.RecordingURL, payload.Transcript != "", payload.Summary != ""); err != nil {
				log.Printf("fathom webhook: save recording: %v", err)
			}
			if h.audit != nil {
				_ = h.audit.Log(r.Context(), audit.Entry{
					EmployeeID: match.EmployeeID,
					Action:     "fathom_meeting_recorded",
					Metadata:   map[string]any{"source_event_id": match.SourceEventID, "recording_id": payload.RecordingID},
				})
			}
		}
	}

	w.WriteHeader(http.StatusNoContent)
}
