package fathom

import (
	"encoding/json"
	"fmt"
)

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
