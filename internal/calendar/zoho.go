package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// APIClient reads calendars and events from the Zoho Calendar API using an
// access token with READ scopes only (spec §27, §29).
type APIClient struct {
	Base string // e.g. https://calendar.zoho.com/api/v1
	HTTP *http.Client
}

func NewAPIClient(base string) *APIClient {
	return &APIClient{Base: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// Calendar is a discovered calendar (spec §27). Field names confirmed against a
// live Zoho response: `type` is numeric, so the human-readable string type comes
// from `caltype` (e.g. "own"); `owner` is the numeric ZUID as a string.
type Calendar struct {
	UID      string `json:"uid"`
	Name     string `json:"name"`
	Type     string `json:"caltype"`
	Timezone string `json:"timezone"`
	Owner    string `json:"owner"`
}

// Event is a minimized calendar event (spec §32).
type Event struct {
	UID         string
	Title       string
	Location    string
	Description string
	Start       string
	End         string
	UpdatedAt   string // maps to Zoho lastmodifiedtime (spec §32 source_updated)
	IsPrivate   bool
}

// ListCalendars retrieves calendars for the authenticated user (spec §27).
func (c *APIClient) ListCalendars(ctx context.Context, accessToken string) ([]Calendar, error) {
	body, err := c.get(ctx, accessToken, "/calendars")
	if err != nil {
		return nil, err
	}
	var out struct {
		Calendars []Calendar `json:"calendars"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("zoho calendars: decode: %w (body: %s)", err, truncate(body))
	}
	return out.Calendars, nil
}

// ListEvents retrieves events for a calendar within [start, end] (spec §29).
// Zoho requires a mandatory `range` parameter and the span cannot exceed 31 days
// (per Zoho Calendar API docs). Times are formatted as yyyyMMdd'T'HHmmss'Z'.
func (c *APIClient) ListEvents(ctx context.Context, accessToken, calendarUID string, start, end time.Time) ([]Event, error) {
	if end.Sub(start) > 31*24*time.Hour {
		return nil, fmt.Errorf("zoho events: range %s..%s exceeds 31-day maximum", start, end)
	}
	rng := fmt.Sprintf(`{"start":"%s","end":"%s"}`,
		start.UTC().Format("20060102T150405Z"), end.UTC().Format("20060102T150405Z"))
	q := url.Values{}
	q.Set("range", rng)

	body, err := c.get(ctx, accessToken, "/calendars/"+calendarUID+"/events?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var out struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("zoho events: decode: %w (body: %s)", err, truncate(body))
	}
	events := make([]Event, 0, len(out.Events))
	for _, m := range out.Events {
		// Zoho returns a sentinel {"message":"No events found."} for an empty
		// range rather than an empty array; skip anything without a uid.
		if str(m["uid"]) == "" {
			continue
		}
		// In the events LIST response, start/end are nested under `dateandtime`
		// (confirmed live); fall back to top-level for the detail endpoint shape.
		start, end := str(m["start"]), str(m["end"])
		if dt, ok := m["dateandtime"].(map[string]any); ok {
			if s := str(dt["start"]); s != "" {
				start = s
			}
			if e := str(dt["end"]); e != "" {
				end = e
			}
		}
		events = append(events, Event{
			UID:         str(m["uid"]),
			Title:       str(m["title"]),
			Location:    str(m["location"]),
			Description: str(m["description"]),
			Start:       start,
			End:         end,
			UpdatedAt:   str(m["lastmodifiedtime"]),
			IsPrivate:   boolOf(m["isprivate"]),
		})
	}
	return events, nil
}

func (c *APIClient) get(ctx context.Context, accessToken, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Zoho-oauthtoken "+accessToken)
	// Zoho omits event descriptions unless this Accept variant is requested; the
	// description carries the video-meeting join link for external (Teams/Outlook)
	// invites, so we need it for meeting detection (spec §29).
	req.Header.Set("Accept", "application/json+large")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("zoho GET %s: status %d: %s", path, resp.StatusCode, truncate(body))
	}
	return body, nil
}

func boolOf(v any) bool {
	b, _ := v.(bool)
	return b
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func truncate(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "…"
	}
	return string(b)
}
