package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Identity is the authenticated Zoho user, fetched from an official endpoint to
// bind the credential to the correct employee record (spec §25-26).
type Identity struct {
	ZohoUserID string
	Email      string
	Name       string
}

// FetchIdentity retrieves the authenticated user's profile from Zoho Accounts
// (/oauth/user/info). The returned ZohoUserID/Email must be verified against the
// expected employee record before attaching the credential (spec §25).
func FetchIdentity(ctx context.Context, accountsBase, accessToken string) (*Identity, error) {
	url := strings.TrimRight(accountsBase, "/") + "/oauth/user/info"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Zoho-oauthtoken "+accessToken)
	hc := &http.Client{Timeout: 15 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("zoho identity: status %d: %s", resp.StatusCode, string(body))
	}
	var raw struct {
		ZUID        json.Number `json:"ZUID"`
		Email       string      `json:"Email"`
		DisplayName string      `json:"Display_Name"`
		FirstName   string      `json:"First_Name"`
		LastName    string      `json:"Last_Name"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("zoho identity: decode: %w (body: %s)", err, string(body))
	}
	name := strings.TrimSpace(raw.DisplayName)
	if name == "" {
		name = strings.TrimSpace(raw.FirstName + " " + raw.LastName)
	}
	return &Identity{
		ZohoUserID: raw.ZUID.String(),
		Email:      raw.Email,
		Name:       name,
	}, nil
}

// VerifyBinding checks the OAuth identity against the expected employee record
// (spec §26). Prefer Zoho user-id match; fall back to case-insensitive email.
func VerifyBinding(got *Identity, expectedZohoUserID, expectedEmail string) error {
	if expectedZohoUserID != "" && got.ZohoUserID != "" {
		if got.ZohoUserID != expectedZohoUserID {
			return fmt.Errorf("identity mismatch: zoho_user_id got=%s expected=%s", got.ZohoUserID, expectedZohoUserID)
		}
		return nil
	}
	if !strings.EqualFold(got.Email, expectedEmail) {
		return fmt.Errorf("identity mismatch: email got=%s expected=%s", got.Email, expectedEmail)
	}
	return nil
}
