package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// Calendar is a discovered calendar (spec §27).
type Calendar struct {
	UID      string `json:"uid"`
	Name     string `json:"name"`
	Type     string `json:"type"`
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
	UpdatedAt   string
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

// ListEvents retrieves events for a calendar (spec §29). The response shape is
// normalized defensively since Zoho field names vary by API version.
func (c *APIClient) ListEvents(ctx context.Context, accessToken, calendarUID string) ([]Event, error) {
	body, err := c.get(ctx, accessToken, "/calendars/"+calendarUID+"/events")
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
		events = append(events, Event{
			UID:         str(m["uid"]),
			Title:       str(m["title"]),
			Location:    str(m["location"]),
			Description: str(m["description"]),
			Start:       str(m["dateandtime"]),
			UpdatedAt:   str(m["lastmodifiedtime"]),
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
