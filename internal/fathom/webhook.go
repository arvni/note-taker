package fathom

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// VerifyWebhook checks a Fathom webhook signature (the "Standard Webhooks" /
// Svix scheme). Headers webhook-id, webhook-timestamp, webhook-signature sign
// the content "{id}.{timestamp}.{body}" with HMAC-SHA256 keyed by the base64-
// decoded secret (after the whsec_ prefix); the signature is base64. The
// timestamp is checked against a 5-minute tolerance to bound replay.
func VerifyWebhook(secret string, h http.Header, body []byte) error {
	id := h.Get("webhook-id")
	ts := h.Get("webhook-timestamp")
	sigHeader := h.Get("webhook-signature")
	if id == "" || ts == "" || sigHeader == "" {
		return fmt.Errorf("fathom: missing webhook signature headers")
	}
	secN, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("fathom: bad webhook-timestamp")
	}
	if d := time.Since(time.Unix(secN, 0)); d > 5*time.Minute || d < -5*time.Minute {
		return fmt.Errorf("fathom: webhook timestamp outside tolerance")
	}
	key := secret
	if i := strings.IndexByte(key, '_'); strings.HasPrefix(key, "whsec_") && i >= 0 {
		key = key[i+1:]
	}
	keyBytes, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return fmt.Errorf("fathom: bad webhook secret encoding")
	}
	mac := hmac.New(sha256.New, keyBytes)
	mac.Write([]byte(id + "." + ts + "." + string(body)))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	// webhook-signature is a space-separated list of "v1,<base64sig>" entries.
	for _, part := range strings.Fields(sigHeader) {
		_, sig, ok := strings.Cut(part, ",")
		if !ok {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) == 1 {
			return nil
		}
	}
	return fmt.Errorf("fathom: webhook signature mismatch")
}

// WebhookPayload is the content Fathom POSTs when a meeting is processed
// (spec §43). Optional artifacts appear only if the subscription requested them.
// Transcript/summary bodies are handled per company retention policy (spec §50);
// this app does not persist them by default (spec §32 data minimization).
type WebhookPayload struct {
	Event        string   `json:"event"`
	MeetingURL   string   `json:"meeting_url"`
	Title        string   `json:"title"`
	RecordingID  string   `json:"recording_id"`
	RecordingURL string   `json:"recording_url"`
	Transcript   string   `json:"transcript,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	ActionItems  []string `json:"action_items,omitempty"`
}

// ParseWebhook decodes a Fathom webhook body.
func ParseWebhook(body []byte) (*WebhookPayload, error) {
	var p WebhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("fathom: parse webhook: %w", err)
	}
	if p.MeetingURL == "" && p.RecordingID == "" {
		return nil, fmt.Errorf("fathom: webhook missing meeting_url and recording_id")
	}
	return &p, nil
}
