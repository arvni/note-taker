package oauth

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

// Client talks to Zoho's OAuth endpoints. All secret-bearing exchanges happen
// server-side; the client_secret is never exposed to the browser (spec §12).
type Client struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	AccountsBase string // e.g. https://accounts.zoho.com
	Scopes       []string
	HTTP         *http.Client
}

func NewClient(clientID, clientSecret, redirectURI, accountsBase string, scopes []string) *Client {
	return &Client{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURI:  redirectURI,
		AccountsBase: strings.TrimRight(accountsBase, "/"),
		Scopes:       scopes,
		HTTP:         &http.Client{Timeout: 20 * time.Second},
	}
}

// AuthorizeURL builds the consent URL. state is the CSRF token (spec §8-9).
// access_type=offline requests a refresh token; prompt=consent per Zoho docs.
func (c *Client) AuthorizeURL(state string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", c.ClientID)
	q.Set("scope", strings.Join(c.Scopes, ","))
	q.Set("redirect_uri", c.RedirectURI)
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	q.Set("state", state)
	return c.AccountsBase + "/oauth/v2/auth?" + q.Encode()
}

// TokenResponse is Zoho's token endpoint payload.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	APIDomain    string `json:"api_domain"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"` // seconds (~3600, spec §15)
	Error        string `json:"error"`
}

func (t *TokenResponse) ExpiresAt() time.Time {
	return time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
}

// ExchangeCode swaps an authorization code for tokens, server-side only (spec §12).
func (c *Client) ExchangeCode(ctx context.Context, code string) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("redirect_uri", c.RedirectURI)
	form.Set("code", code)
	return c.postToken(ctx, form)
}

// Refresh obtains a new access token from a refresh token. It does NOT request a
// new refresh token (spec §15).
func (c *Client) Refresh(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("refresh_token", refreshToken)
	tr, err := c.postToken(ctx, form)
	if err != nil {
		return nil, err
	}
	// Zoho returns only a new access token on refresh; keep the existing one.
	tr.RefreshToken = refreshToken
	return tr, nil
}

// Revoke revokes a refresh token via Zoho's revocation endpoint (spec §17).
func (c *Client) Revoke(ctx context.Context, refreshToken string) error {
	form := url.Values{}
	form.Set("token", refreshToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.AccountsBase+"/oauth/v2/token/revoke", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("zoho revoke: status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (c *Client) postToken(ctx context.Context, form url.Values) (*TokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.AccountsBase+"/oauth/v2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var tr TokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("zoho token: decode: %w (body: %s)", err, string(body))
	}
	if tr.Error != "" {
		return nil, wrapTokenError(tr.Error)
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("zoho token: empty access_token (body: %s)", string(body))
	}
	return &tr, nil
}
