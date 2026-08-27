// Package fathom is the Fathom integration client. It uses only Fathom's
// documented external API (spec §43) and is deliberately isolated from Zoho and
// calendar code — it imports neither, so the two integrations stay decoupled.
//
// Documented surface (https://developers.fathom.ai, base
// https://api.fathom.ai/external/v1):
//
//	GET    /meetings                 list meetings
//	GET    /recordings/transcript    speaker-attributed transcript
//	GET    /recordings/summary       AI summary
//	POST   /webhooks                 subscribe to new meeting content
//	DELETE /webhooks/{id}            remove a subscription
package fathom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to the Fathom external API.
type Client struct {
	Base   string
	apiKey string
	HTTP   *http.Client
}

func NewClient(base, apiKey string) *Client {
	if base == "" {
		base = "https://api.fathom.ai/external/v1"
	}
	return &Client{Base: strings.TrimRight(base, "/"), apiKey: apiKey, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// Meeting is a Fathom meeting/recording (spec §43). Fields are mapped
// defensively; the meeting_url links back to the source calendar event.
type Meeting struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	MeetingURL   string `json:"meeting_url"`
	RecordingURL string `json:"recording_url"`
	ScheduledAt  string `json:"scheduled_start_time"`
	CreatedAt    string `json:"created_at"`
}

// ListMeetings returns recent meetings, optionally filtered by query params
// (e.g. cursor, created_after). Used to confirm Fathom recorded a meeting.
func (c *Client) ListMeetings(ctx context.Context, params url.Values) ([]Meeting, error) {
	path := "/meetings"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	var out struct {
		Items    []Meeting `json:"items"`
		Meetings []Meeting `json:"meetings"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if out.Items != nil {
		return out.Items, nil
	}
	return out.Meetings, nil
}

// Transcript is a speaker-attributed transcript (spec §43). Callers decide
// whether to persist it per the company retention policy (spec §32, §50).
type Transcript struct {
	RecordingID string `json:"recording_id"`
	Text        string `json:"transcript"`
}

// GetTranscript fetches a recording's transcript.
func (c *Client) GetTranscript(ctx context.Context, recordingID string) (*Transcript, error) {
	q := url.Values{}
	q.Set("recording_id", recordingID)
	var t Transcript
	if err := c.do(ctx, http.MethodGet, "/recordings/transcript?"+q.Encode(), nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// WebhookSpec configures a webhook subscription (spec §43). At least one of the
// include_* flags must be true, per Fathom's documentation.
type WebhookSpec struct {
	DestinationURL     string `json:"destination_url"`
	IncludeTranscript  bool   `json:"include_transcript,omitempty"`
	IncludeSummary     bool   `json:"include_summary,omitempty"`
	IncludeActionItems bool   `json:"include_action_items,omitempty"`
	IncludeCRMMatches  bool   `json:"include_crm_matches,omitempty"`
}

// CreateWebhook subscribes to new meeting content and returns the webhook id.
func (c *Client) CreateWebhook(ctx context.Context, spec WebhookSpec) (string, error) {
	if spec.DestinationURL == "" {
		return "", fmt.Errorf("fathom: webhook destination_url required")
	}
	if !spec.IncludeTranscript && !spec.IncludeSummary && !spec.IncludeActionItems && !spec.IncludeCRMMatches {
		return "", fmt.Errorf("fathom: at least one include_* flag must be set")
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/webhooks", spec, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// DeleteWebhook removes a webhook subscription. Missing is treated as gone.
func (c *Client) DeleteWebhook(ctx context.Context, id string) error {
	err := c.do(ctx, http.MethodDelete, "/webhooks/"+url.PathEscape(id), nil, nil)
	if fe, ok := err.(*apiError); ok && (fe.status == http.StatusNotFound || fe.status == http.StatusGone) {
		return nil
	}
	return err
}

type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string { return fmt.Sprintf("fathom: status %d: %s", e.status, e.body) }

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &apiError{status: resp.StatusCode, body: truncate(raw)}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("fathom: decode: %w", err)
		}
	}
	return nil
}

func truncate(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "…"
	}
	return string(b)
}
