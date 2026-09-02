package httpx

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/fireflies"
)

// FirefliesWebhookHandler receives Fireflies "Transcription completed" webhooks,
// fetches the transcript + summary from the Fireflies API, and downloads them to
// a per-meeting folder (later shipped to the data lake).
type FirefliesWebhookHandler struct {
	keyFor      func(ctx context.Context, org db.OrgID) string
	downloadDir string
	secret      string // optional webhook signing secret (x-hub-signature)
	adminOrg    db.OrgID
}

func NewFirefliesWebhookHandler(keyFor func(ctx context.Context, org db.OrgID) string, downloadDir, secret string, adminOrg db.OrgID) *FirefliesWebhookHandler {
	return &FirefliesWebhookHandler{keyFor: keyFor, downloadDir: downloadDir, secret: secret, adminOrg: adminOrg}
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
