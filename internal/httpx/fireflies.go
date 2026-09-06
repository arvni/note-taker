package httpx

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/arvinizadi/fathom/internal/calendar"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/email"
	"github.com/arvinizadi/fathom/internal/fireflies"
	"github.com/arvinizadi/fathom/internal/onboarding"
)

// matchWindow is how far a synced calendar event's start may sit from the
// recorded meeting's start and still be considered the same meeting.
const matchWindow = 30 * time.Minute

// FirefliesWebhookHandler receives Fireflies "Transcription completed" webhooks,
// fetches the transcript + summary from the Fireflies API, downloads them to a
// per-meeting folder, and (when enabled) emails the transcript + summary to the
// employees who had that meeting on their synced calendar.
type FirefliesWebhookHandler struct {
	keyFor      func(ctx context.Context, org db.OrgID) string
	downloadDir string
	secret      string // optional webhook signing secret (x-hub-signature)
	adminOrg    db.OrgID

	// notify dependencies (nil-safe: when unset or notify=false, no email is sent).
	matchAttendees func(ctx context.Context, org db.OrgID, start time.Time, window time.Duration, title string) ([]calendar.Attendee, error)
	senderFor      onboarding.Resolver
	notify         bool
}

func NewFirefliesWebhookHandler(keyFor func(ctx context.Context, org db.OrgID) string, downloadDir, secret string, adminOrg db.OrgID) *FirefliesWebhookHandler {
	return &FirefliesWebhookHandler{keyFor: keyFor, downloadDir: downloadDir, secret: secret, adminOrg: adminOrg}
}

// WithAttendeeNotify enables emailing the transcript/summary to the employees
// who had the meeting on their synced calendar.
func (h *FirefliesWebhookHandler) WithAttendeeNotify(
	match func(ctx context.Context, org db.OrgID, start time.Time, window time.Duration, title string) ([]calendar.Attendee, error),
	senderFor onboarding.Resolver, enabled bool,
) *FirefliesWebhookHandler {
	h.matchAttendees, h.senderFor, h.notify = match, senderFor, enabled
	return h
}

func (h *FirefliesWebhookHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /webhooks/fireflies", h.receive)
}

func (h *FirefliesWebhookHandler) receive(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	// Optional signature verification (x-hub-signature: sha256=<hmac-of-body>).
	if h.secret != "" {
		if !validHubSignature(h.secret, r.Header.Get("x-hub-signature"), body) {
			log.Print("fireflies webhook: signature rejected")
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}
	var p struct {
		MeetingID    string `json:"meetingId"`
		TranscriptID string `json:"transcriptId"`
		EventType    string `json:"eventType"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	id := p.MeetingID
	if id == "" {
		id = p.TranscriptID
	}
	if id == "" {
		// Ack unknown/ping payloads so Fireflies doesn't retry.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	log.Printf("fireflies webhook: event=%q id=%s", p.EventType, id)

	key := h.keyFor(r.Context(), h.adminOrg)
	if key == "" {
		log.Print("fireflies webhook: no API key configured — skipping fetch")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Fetch + download in the background so the webhook returns immediately.
	go h.fetchAndDownload(key, id)
	w.WriteHeader(http.StatusNoContent)
}

func (h *FirefliesWebhookHandler) fetchAndDownload(key, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := fireflies.NewClient(key)
	t, err := c.GetTranscript(ctx, id)
	if err != nil {
		log.Printf("fireflies fetch %s: %v", id, err)
		return
	}
	// Best-effort media URLs (paid plans only).
	t.AudioURL, t.VideoURL = c.GetMedia(ctx, id)
	dir, err := c.Download(ctx, h.downloadDir, t)
	if err != nil {
		log.Printf("fireflies download %s: %v", id, err)
		return
	}
	log.Printf("fireflies: downloaded %q -> %s", t.Title, dir)

	if h.notify && h.matchAttendees != nil && h.senderFor != nil {
		h.notifyAttendees(ctx, t, dir)
	}
}

// notifyAttendees emails the transcript + summary to the employees who had this
// meeting on their synced calendar. A `.notified` marker in the meeting folder
// makes it idempotent across Fireflies webhook retries.
func (h *FirefliesWebhookHandler) notifyAttendees(ctx context.Context, t *fireflies.Transcript, dir string) {
	marker := filepath.Join(dir, ".notified")
	if _, err := os.Stat(marker); err == nil {
		return // already emailed for this meeting
	}

	start, ok := parseMeetingStart(t.DateString)
	if !ok {
		log.Printf("fireflies notify: unparseable start %q for %q — skipping", t.DateString, t.Title)
		return
	}
	attendees, err := h.matchAttendees(ctx, h.adminOrg, start, matchWindow, t.Title)
	if err != nil {
		log.Printf("fireflies notify: match attendees for %q: %v", t.Title, err)
		return
	}
	if len(attendees) == 0 {
		log.Printf("fireflies notify: no calendar match for %q at %s — no recipients", t.Title, start.Format(time.RFC3339))
		return
	}

	sender, brand := h.senderFor(ctx, h.adminOrg)
	atts := readAttachments(dir)
	subject := "Meeting notes: " + orDefault(t.Title, "recording")
	body := recordingEmailBody(t.Title, t.DateString, t.TranscriptURL, brand, dir)

	var sent, failed int
	for _, a := range attendees {
		msg := email.Message{
			To:          a.Email,
			From:        brand.FromAddr,
			Subject:     subject,
			HTMLBody:    body,
			Attachments: atts,
		}
		if err := sender.Send(ctx, msg); err != nil {
			failed++
			log.Printf("fireflies notify: send to %s failed: %v", a.Email, err)
			continue
		}
		sent++
	}
	log.Printf("fireflies notify: %q -> %d sent, %d failed", t.Title, sent, failed)
	if sent > 0 {
		if err := os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)), 0o644); err != nil {
			log.Printf("fireflies notify: write marker: %v", err)
		}
	}
}

// readAttachments loads the human-readable transcript + summary from a meeting
// folder as email attachments (skips any that are missing).
func readAttachments(dir string) []email.Attachment {
	var out []email.Attachment
	for _, name := range []string{"summary.txt", "transcript.txt"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || len(b) == 0 {
			continue
		}
		out = append(out, email.Attachment{
			Filename:    name,
			ContentType: "text/plain; charset=UTF-8",
			Data:        b,
		})
	}
	return out
}

// recordingEmailBody builds a short HTML body with the meeting title, time, and
// AI overview (when present), noting the attached files. Shared by the webhook
// auto-notify and the manual "send recording" endpoint.
func recordingEmailBody(title, dateString, transcriptURL string, brand onboarding.Brand, dir string) string {
	overview := ""
	if b, err := os.ReadFile(filepath.Join(dir, "summary.txt")); err == nil {
		overview = strings.TrimSpace(string(b))
		if len(overview) > 1500 {
			overview = overview[:1500] + "…"
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<p>Hi,</p>")
	fmt.Fprintf(&b, "<p>Here are the notes from <strong>%s</strong>", html.EscapeString(orDefault(title, "your meeting")))
	if dateString != "" {
		fmt.Fprintf(&b, " (%s)", html.EscapeString(dateString))
	}
	fmt.Fprintf(&b, ".</p>")
	if overview != "" {
		fmt.Fprintf(&b, "<pre style=\"white-space:pre-wrap;font-family:inherit\">%s</pre>", html.EscapeString(overview))
	}
	fmt.Fprintf(&b, "<p>The full summary and transcript are attached.</p>")
	if brand.CompanyName != "" {
		fmt.Fprintf(&b, "<p>— %s</p>", html.EscapeString(brand.CompanyName))
	}
	return b.String()
}

// parseMeetingStart parses a Fireflies dateString, tolerating RFC3339 and a few
// common variants.
func parseMeetingStart(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339Nano, time.RFC3339,
		"2006-01-02T15:04:05.000Z", "2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05", "2006-01-02",
	} {
		if ts, err := time.Parse(layout, s); err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

func validHubSignature(secret, header string, body []byte) bool {
	header = strings.TrimSpace(header)
	header = strings.TrimPrefix(header, "sha256=")
	if header == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(header), []byte(expected))
}
