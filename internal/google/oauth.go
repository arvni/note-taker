package google

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OAuthClient runs Google's authorization-code flow to connect a destination
// calendar (spec §42). The client secret stays server-side (spec §12).
type OAuthClient struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	HTTP         *http.Client
}

func NewOAuthClient(clientID, clientSecret, redirectURL string) *OAuthClient {
	return &OAuthClient{ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURL,
		HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// AuthCodeURL builds the consent URL requesting calendar access with offline
// access (so we get a refresh token) (spec §42).
func (c *OAuthClient) AuthCodeURL(state string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", c.ClientID)
	q.Set("redirect_uri", c.RedirectURL)
	q.Set("scope", CalendarScope)
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	q.Set("include_granted_scopes", "true")
	q.Set("state", state)
	return "https://accounts.google.com/o/oauth2/v2/auth?" + q.Encode()
}

// Exchange swaps the authorization code for an access + refresh token.
func (c *OAuthClient) Exchange(ctx context.Context, code string) (accessToken, refreshToken string, err error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("redirect_uri", c.RedirectURL)
	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Error        string `json:"error"`
	}
	if err := c.postToken(ctx, form, &tr); err != nil {
		return "", "", err
	}
	if tr.RefreshToken == "" {
		return "", "", fmt.Errorf("google oauth: no refresh_token (re-consent required)")
	}
	return tr.AccessToken, tr.RefreshToken, nil
}

// Refresh exchanges a refresh token for a new access token.
func (c *OAuthClient) Refresh(ctx context.Context, refreshToken string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	var tr struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := c.postToken(ctx, form, &tr); err != nil {
		return "", err
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("google oauth: empty access_token on refresh")
	}
	return tr.AccessToken, nil
}

func (c *OAuthClient) postToken(ctx context.Context, form url.Values, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("google oauth: status %d: %s", resp.StatusCode, truncate(body))
	}
	return json.Unmarshal(body, out)
}

// RefreshTokenSource is a TokenSource backed by a stored refresh token, caching
// the access token until near expiry.
type RefreshTokenSource struct {
	oauth        *OAuthClient
	refreshToken string
	mu           sync.Mutex
	token        string
	expires      time.Time
}

func NewRefreshTokenSource(oauth *OAuthClient, refreshToken string) *RefreshTokenSource {
	return &RefreshTokenSource{oauth: oauth, refreshToken: refreshToken}
}

func (s *RefreshTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && time.Until(s.expires) > time.Minute {
		return s.token, nil
	}
	tok, err := s.oauth.Refresh(ctx, s.refreshToken)
	if err != nil {
		return "", err
	}
	s.token = tok
	s.expires = time.Now().Add(55 * time.Minute)
	return tok, nil
}
