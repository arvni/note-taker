// Package oidc implements the OpenID Connect authorization-code flow with ID
// token verification against the provider's JWKS (spec §39). Admin login uses
// this; there is no local password database. Stdlib only.
package oidc

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Config configures an OIDC provider (e.g. Google Workspace, Entra, Zoho).
type Config struct {
	IssuerURL    string // e.g. https://accounts.google.com
	ClientID     string
	ClientSecret string
	RedirectURL  string // https://<domain>/auth/callback
	HostedDomain string // optional: restrict to a Google Workspace domain (hd)
}

// discovery is the subset of the OIDC discovery document we use.
type discovery struct {
	Issuer        string `json:"issuer"`
	AuthEndpoint  string `json:"authorization_endpoint"`
	TokenEndpoint string `json:"token_endpoint"`
	JWKSURI       string `json:"jwks_uri"`
}

// Claims are the verified ID-token claims we rely on.
type Claims struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	HostedDomain  string `json:"hd"`
	Nonce         string `json:"nonce"`
	Issuer        string `json:"iss"`
	Audience      string `json:"aud"`
	Expiry        int64  `json:"exp"`
}

// Provider performs the OIDC flow and caches discovery + JWKS.
type Provider struct {
	cfg  Config
	http *http.Client

	mu    sync.Mutex
	disco *discovery
	keys  map[string]*rsa.PublicKey
}

func New(cfg Config) *Provider {
	return &Provider{cfg: cfg, http: &http.Client{Timeout: 15 * time.Second}}
}

func (p *Provider) discover(ctx context.Context) (*discovery, error) {
	p.mu.Lock()
	d := p.disco
	p.mu.Unlock()
	if d != nil {
		return d, nil
	}
	u := strings.TrimRight(p.cfg.IssuerURL, "/") + "/.well-known/openid-configuration"
	var out discovery
	if err := p.getJSON(ctx, u, &out); err != nil {
		return nil, fmt.Errorf("oidc: discovery: %w", err)
	}
	p.mu.Lock()
	p.disco = &out
	p.mu.Unlock()
	return &out, nil
}

// AuthCodeURL builds the authorization URL with state and nonce (spec §39).
func (p *Provider) AuthCodeURL(ctx context.Context, state, nonce string) (string, error) {
	d, err := p.discover(ctx)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", p.cfg.ClientID)
	q.Set("redirect_uri", p.cfg.RedirectURL)
	q.Set("scope", "openid email profile")
	q.Set("state", state)
	q.Set("nonce", nonce)
	if p.cfg.HostedDomain != "" {
		q.Set("hd", p.cfg.HostedDomain)
	}
	return d.AuthEndpoint + "?" + q.Encode(), nil
}

// Exchange swaps the code for tokens and returns the verified ID-token claims.
func (p *Provider) Exchange(ctx context.Context, code, expectedNonce string) (*Claims, error) {
	d, err := p.discover(ctx)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", p.cfg.ClientID)
	form.Set("client_secret", p.cfg.ClientSecret)
	form.Set("redirect_uri", p.cfg.RedirectURL)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc: token exchange: status %d: %s", resp.StatusCode, trunc(body))
	}
	var tr struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, err
	}
	if tr.IDToken == "" {
		return nil, fmt.Errorf("oidc: no id_token in response")
	}
	return p.VerifyIDToken(ctx, tr.IDToken, expectedNonce)
}

// VerifyIDToken verifies the JWT signature against the provider JWKS and checks
// iss/aud/exp/nonce/email_verified/hd (spec §39).
func (p *Provider) VerifyIDToken(ctx context.Context, raw, expectedNonce string) (*Claims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("oidc: malformed id_token")
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(hb, &hdr); err != nil {
		return nil, err
	}
	if hdr.Alg != "RS256" {
		return nil, fmt.Errorf("oidc: unsupported alg %q", hdr.Alg)
	}
	key, err := p.keyByID(ctx, hdr.Kid)
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, h[:], sig); err != nil {
		return nil, fmt.Errorf("oidc: signature verification failed: %w", err)
	}

	cb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var c Claims
	if err := json.Unmarshal(cb, &c); err != nil {
		return nil, err
	}
	// aud can be a string or array; normalize by re-checking the raw claim.
	if !audienceMatches(cb, p.cfg.ClientID) {
		return nil, fmt.Errorf("oidc: audience mismatch")
	}
	if strings.TrimRight(c.Issuer, "/") != strings.TrimRight(p.cfg.IssuerURL, "/") {
		return nil, fmt.Errorf("oidc: issuer mismatch: %s", c.Issuer)
	}
	if time.Now().Unix() > c.Expiry {
		return nil, fmt.Errorf("oidc: id_token expired")
	}
	if expectedNonce != "" && c.Nonce != expectedNonce {
		return nil, fmt.Errorf("oidc: nonce mismatch")
	}
	if !c.EmailVerified {
		return nil, fmt.Errorf("oidc: email not verified")
	}
	if p.cfg.HostedDomain != "" && !strings.EqualFold(c.HostedDomain, p.cfg.HostedDomain) {
		return nil, fmt.Errorf("oidc: hosted domain mismatch")
	}
	return &c, nil
}

func (p *Provider) keyByID(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	p.mu.Lock()
	k := p.keys[kid]
	p.mu.Unlock()
	if k != nil {
		return k, nil
	}
	if err := p.refreshKeys(ctx); err != nil {
		return nil, err
	}
	p.mu.Lock()
	k = p.keys[kid]
	p.mu.Unlock()
	if k == nil {
		return nil, fmt.Errorf("oidc: no JWKS key for kid %q", kid)
	}
	return k, nil
}

func (p *Provider) refreshKeys(ctx context.Context) error {
	d, err := p.discover(ctx)
	if err != nil {
		return err
	}
	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := p.getJSON(ctx, d.JWKSURI, &jwks); err != nil {
		return fmt.Errorf("oidc: jwks: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey)
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" {
			continue
		}
		nb, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eb, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(nb),
			E: int(new(big.Int).SetBytes(eb).Int64()),
		}
	}
	p.mu.Lock()
	p.keys = keys
	p.mu.Unlock()
	return nil
}

func (p *Provider) getJSON(ctx context.Context, u string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d: %s", resp.StatusCode, trunc(body))
	}
	return json.Unmarshal(body, out)
}

func audienceMatches(claimsJSON []byte, clientID string) bool {
	var probe struct {
		Aud json.RawMessage `json:"aud"`
	}
	if err := json.Unmarshal(claimsJSON, &probe); err != nil {
		return false
	}
	var single string
	if json.Unmarshal(probe.Aud, &single) == nil {
		return single == clientID
	}
	var many []string
	if json.Unmarshal(probe.Aud, &many) == nil {
		for _, a := range many {
			if a == clientID {
				return true
			}
		}
	}
	return false
}

func trunc(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "…"
	}
	return string(b)
}
