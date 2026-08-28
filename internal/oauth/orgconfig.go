package oauth

import "strings"

// OrgConfig is a resolved per-organization Zoho configuration. It builds the
// OAuth client used for that org's onboarding, refresh, and API calls (spec §8).
type OrgConfig struct {
	ClientID     string
	ClientSecret string
	AccountsBase string
	CalendarBase string
	RedirectURI  string
	Scopes       []string
}

// Client builds the OAuth client for this org config.
func (c OrgConfig) Client() *Client {
	return NewClient(c.ClientID, c.ClientSecret, c.RedirectURI, c.AccountsBase, c.Scopes)
}

// Configured reports whether the minimum fields are present.
func (c OrgConfig) Configured() bool {
	return c.ClientID != "" && c.ClientSecret != ""
}

// SplitScopes parses a comma-separated scope string.
func SplitScopes(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
