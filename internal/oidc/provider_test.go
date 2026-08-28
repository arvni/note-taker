package oidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeOP is a minimal OpenID Provider: discovery, JWKS, and a token endpoint
// that returns a signed id_token.
type fakeOP struct {
	key      *rsa.PrivateKey
	kid      string
	issuer   string
	clientID string
	claims   map[string]any
}

func (f *fakeOP) server(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 f.issuer,
			"authorization_endpoint": srv.URL + "/auth",
			"token_endpoint":         srv.URL + "/token",
			"jwks_uri":               srv.URL + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(f.key.PublicKey.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}) // 65537
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{
			{"kid": f.kid, "kty": "RSA", "n": n, "e": e},
		}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"id_token": f.signIDToken(t)})
	})
	srv = httptest.NewServer(mux)
	f.issuer = srv.URL
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeOP) signIDToken(t *testing.T) string {
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": f.kid})
	cb, _ := json.Marshal(f.claims)
	signingInput := b64(hdr) + "." + b64(cb)
	h := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, h[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func newFakeOP(t *testing.T, clientID string, claims map[string]any) *fakeOP {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	return &fakeOP{key: key, kid: "k1", clientID: clientID, claims: claims}
}

func TestOIDCExchangeAndVerify(t *testing.T) {
	claims := map[string]any{
		"sub": "1234", "email": "admin@company.com", "email_verified": true,
		"aud": "client-abc", "nonce": "n-xyz", "exp": time.Now().Add(time.Hour).Unix(),
	}
	op := newFakeOP(t, "client-abc", claims)
	srv := op.server(t)
	op.claims["iss"] = srv.URL

	p := New(Config{IssuerURL: srv.URL, ClientID: "client-abc", ClientSecret: "s", RedirectURL: "https://app/cb"})
	c, err := p.Exchange(context.Background(), "code", "n-xyz")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if c.Email != "admin@company.com" || c.Subject != "1234" {
		t.Fatalf("claims wrong: %+v", c)
	}
}

func TestOIDCRejectsBadNonce(t *testing.T) {
	claims := map[string]any{"sub": "1", "email": "a@b.com", "email_verified": true,
		"aud": "cid", "nonce": "real", "exp": time.Now().Add(time.Hour).Unix()}
	op := newFakeOP(t, "cid", claims)
	srv := op.server(t)
	op.claims["iss"] = srv.URL
	p := New(Config{IssuerURL: srv.URL, ClientID: "cid"})
	if _, err := p.Exchange(context.Background(), "code", "EXPECTED-DIFFERENT"); err == nil {
		t.Fatal("expected nonce mismatch error")
	}
}

func TestOIDCRejectsBadAudience(t *testing.T) {
	claims := map[string]any{"sub": "1", "email": "a@b.com", "email_verified": true,
		"aud": "someone-else", "exp": time.Now().Add(time.Hour).Unix()}
	op := newFakeOP(t, "cid", claims)
	srv := op.server(t)
	op.claims["iss"] = srv.URL
	p := New(Config{IssuerURL: srv.URL, ClientID: "cid"})
	if _, err := p.Exchange(context.Background(), "code", ""); err == nil {
		t.Fatal("expected audience mismatch error")
	}
}

func TestOIDCRejectsTamperedSignature(t *testing.T) {
	claims := map[string]any{"sub": "1", "email": "a@b.com", "email_verified": true,
		"aud": "cid", "exp": time.Now().Add(time.Hour).Unix()}
	op := newFakeOP(t, "cid", claims)
	srv := op.server(t)
	op.claims["iss"] = srv.URL
	p := New(Config{IssuerURL: srv.URL, ClientID: "cid"})
	raw := op.signIDToken(t)
	tampered := raw[:len(raw)-4] + "AAAA"
	if _, err := p.VerifyIDToken(context.Background(), tampered, ""); err == nil {
		t.Fatal("expected signature verification failure")
	}
}

func TestOIDCRejectsExpired(t *testing.T) {
	claims := map[string]any{"sub": "1", "email": "a@b.com", "email_verified": true,
		"aud": "cid", "exp": time.Now().Add(-time.Minute).Unix()}
	op := newFakeOP(t, "cid", claims)
	srv := op.server(t)
	op.claims["iss"] = srv.URL
	p := New(Config{IssuerURL: srv.URL, ClientID: "cid"})
	if _, err := p.VerifyIDToken(context.Background(), op.signIDToken(t), ""); err == nil {
		t.Fatal("expected expired token error")
	}
}
