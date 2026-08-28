package directory

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ZohoClient fetches the organization's users from Zoho Directory (spec §3, §55).
// It reads only directory fields — never passwords, never Mail (spec §33).
//
// The base URL and required scope are org/data-center specific; set BaseURL to
// the company's Zoho Directory REST endpoint. Field mappings follow Zoho
// Directory's documented user shape (zuid, emails[].email_id, is_active,
// display_name). When an approved Directory API is not available, use the CSV
// fallback (spec §3) which is the guaranteed path.
type ZohoClient struct {
	BaseURL string // e.g. https://directory.zoho.com/api/v1/users
	HTTP    *http.Client
}

func NewZohoClient(baseURL string) *ZohoClient {
	return &ZohoClient{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// ListUsers retrieves and normalizes the org's users using an access token that
// carries the approved Directory read scope.
func (c *ZohoClient) ListUsers(ctx context.Context, accessToken string) ([]User, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL, nil)
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
		return nil, fmt.Errorf("zoho directory: status %d: %s", resp.StatusCode, truncate(body))
	}
	return parseDirectoryUsers(body)
}

// parseDirectoryUsers normalizes Zoho Directory's response defensively. The
// users array may be keyed as "users" or "data" depending on API version.
func parseDirectoryUsers(body []byte) ([]User, error) {
	var envelope struct {
		Users []map[string]any `json:"users"`
		Data  []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("zoho directory: decode: %w (body: %s)", err, truncate(body))
	}
	raw := envelope.Users
	if raw == nil {
		raw = envelope.Data
	}
	out := make([]User, 0, len(raw))
	for _, m := range raw {
		u := User{
			ZohoUserID: str(m["zuid"]),
			Email:      primaryEmail(m),
			Name:       firstNonEmpty(str(m["full_name"]), joinName(m), str(m["display_name"])),
			Department: str(m["department"]),
			Status:     directoryStatus(m),
		}.Normalize()
		if u.Valid() {
			out = append(out, u)
		}
	}
	return out, nil
}

// primaryEmail extracts the email from the documented emails[] array
// (email_id / is_primary) with a top-level fallback.
func primaryEmail(m map[string]any) string {
	if arr, ok := m["emails"].([]any); ok {
		var first string
		for _, e := range arr {
			em, ok := e.(map[string]any)
			if !ok {
				continue
			}
			addr := firstNonEmpty(str(em["email_id"]), str(em["value"]))
			if addr == "" {
				continue
			}
			if first == "" {
				first = addr
			}
			if b, _ := em["is_primary"].(bool); b {
				return addr
			}
		}
		if first != "" {
			return first
		}
	}
	return firstNonEmpty(str(m["primary_email"]), str(m["email_id"]), str(m["email"]), str(m["primaryEmailAddress"]))
}

func directoryStatus(m map[string]any) Status {
	// Zoho Directory v2 reports user_status as a string ("active"/"inactive").
	if st := strings.ToLower(str(m["user_status"])); st != "" {
		if st == "active" {
			return Active
		}
		return Inactive
	}
	// Fallback: some responses use an is_active boolean.
	if b, ok := m["is_active"].(bool); ok && !b {
		return Inactive
	}
	return Active
}

func joinName(m map[string]any) string {
	return strings.TrimSpace(str(m["first_name"]) + " " + str(m["last_name"]))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
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
