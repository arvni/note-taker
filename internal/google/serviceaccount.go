package google

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// CalendarScope is the OAuth scope needed to write events (spec §42).
const CalendarScope = "https://www.googleapis.com/auth/calendar"

// serviceAccountKey is the subset of a Google service-account JSON we use.
type serviceAccountKey struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// ServiceAccountTokenSource mints Google API access tokens from a service
// account using the JWT-bearer grant (RFC 7523), caching until near expiry. It
// implements TokenSource. Set Subject for domain-wide delegation (impersonate a
// user); leave empty to act as the service account on calendars shared with it.
type ServiceAccountTokenSource struct {
	email      string
	privateKey *rsa.PrivateKey
	tokenURI   string
	scope      string
	subject    string
	http       *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
}

// NewServiceAccountTokenSource parses a service-account JSON key.
func NewServiceAccountTokenSource(keyJSON []byte, scope, subject string) (*ServiceAccountTokenSource, error) {
	var k serviceAccountKey
	if err := json.Unmarshal(keyJSON, &k); err != nil {
		return nil, fmt.Errorf("google: parse service account: %w", err)
	}
	if k.ClientEmail == "" || k.PrivateKey == "" {
		return nil, fmt.Errorf("google: service account missing client_email or private_key")
	}
	if k.TokenURI == "" {
		k.TokenURI = "https://oauth2.googleapis.com/token"
	}
	pk, err := parsePrivateKey(k.PrivateKey)
	if err != nil {
		return nil, err
	}
	if scope == "" {
		scope = CalendarScope
	}
	return &ServiceAccountTokenSource{
		email: k.ClientEmail, privateKey: pk, tokenURI: k.TokenURI, scope: scope, subject: subject,
		http: &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// Token returns a cached or freshly-minted access token (spec §42).
func (s *ServiceAccountTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && time.Until(s.expires) > time.Minute {
		return s.token, nil
	}
	assertion, err := s.signedJWT(time.Now())
	if err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", assertion)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("google token: status %d: %s", resp.StatusCode, truncate(body))
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("google token: decode: %w", err)
	}
	if tr.Error != "" || tr.AccessToken == "" {
		return "", fmt.Errorf("google token: %s", firstNonEmpty(tr.Error, "empty access_token"))
	}
	s.token = tr.AccessToken
	s.expires = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return s.token, nil
}

// signedJWT builds and RS256-signs the assertion JWT (RFC 7523).
func (s *ServiceAccountTokenSource) signedJWT(now time.Time) (string, error) {
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iss":   s.email,
		"scope": s.scope,
		"aud":   s.tokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	if s.subject != "" {
		claims["sub"] = s.subject // domain-wide delegation
	}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	signingInput := b64(hb) + "." + b64(cb)

	h := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.privateKey, crypto.SHA256, h[:])
	if err != nil {
		return "", fmt.Errorf("google: sign jwt: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("google: no PEM block in private key")
	}
	// Service-account keys are PKCS#8.
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("google: private key is not RSA")
		}
		return rsaKey, nil
	}
	// Fall back to PKCS#1.
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
