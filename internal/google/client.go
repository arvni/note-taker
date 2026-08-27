// Package google is the Google Calendar destination client (spec §42). It writes
// qualifying meetings into the company Google Calendar that Fathom watches. The
// package is intentionally isolated from Zoho code (spec §43 keeps providers
// separate). Authentication is supplied via a TokenSource so the service-account
// / OAuth wiring is a separate concern.
package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// TokenSource yields a Google API access token (e.g. from a service account).
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// Client writes events to a single destination Google Calendar.
type Client struct {
	Base       string // default https://www.googleapis.com/calendar/v3
	CalendarID string // destination calendar id (e.g. the Fathom-watched calendar)
	tokens     TokenSource
	HTTP       *http.Client
}

func NewClient(base, calendarID string, ts TokenSource) *Client {
	if base == "" {
		base = "https://www.googleapis.com/calendar/v3"
	}
	return &Client{Base: strings.TrimRight(base, "/"), CalendarID: calendarID, tokens: ts,
		HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// Event is a destination calendar event. The meeting URL is placed in both
// location and description so Fathom can detect the conference (spec §29, §42).
type Event struct {
	Summary     string
	Location    string
	Description string
	Start       time.Time
	End         time.Time
}

type gTime struct {
	DateTime string `json:"dateTime,omitempty"`
}

type gEvent struct {
	ID          string `json:"id,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Location    string `json:"location,omitempty"`
	Description string `json:"description,omitempty"`
	Start       gTime  `json:"start"`
	End         gTime  `json:"end"`
}

func toGEvent(e Event) gEvent {
	return gEvent{
		Summary: e.Summary, Location: e.Location, Description: e.Description,
		Start: gTime{DateTime: e.Start.Format(time.RFC3339)},
		End:   gTime{DateTime: e.End.Format(time.RFC3339)},
	}
}

// CreateEvent inserts an event and returns its Google event id (spec §42).
func (c *Client) CreateEvent(ctx context.Context, e Event) (string, error) {
	var out gEvent
	if err := c.do(ctx, http.MethodPost, "/calendars/"+c.CalendarID+"/events", toGEvent(e), &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("google: create event returned empty id")
	}
	return out.ID, nil
}

// UpdateEvent updates an existing destination event (spec §14 objective).
func (c *Client) UpdateEvent(ctx context.Context, eventID string, e Event) error {
	return c.do(ctx, http.MethodPut, "/calendars/"+c.CalendarID+"/events/"+eventID, toGEvent(e), nil)
}

// DeleteEvent removes a destination event (spec §15 objective). A 404/410 is
// treated as already-gone (idempotent).
func (c *Client) DeleteEvent(ctx context.Context, eventID string) error {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		c.Base+"/calendars/"+c.CalendarID+"/events/"+eventID, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusNotFound, http.StatusGone:
		return nil
	default:
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("google: delete event: status %d: %s", resp.StatusCode, truncate(body))
	}
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return err
	}
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
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("google: %s %s: status %d: %s", method, path, resp.StatusCode, truncate(raw))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("google: decode: %w", err)
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

// StaticToken is a trivial TokenSource for tests / static-token deployments.
type StaticToken string

func (s StaticToken) Token(context.Context) (string, error) { return string(s), nil }
