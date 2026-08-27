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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func makeServiceAccountJSON(t *testing.T, tokenURI string) ([]byte, *rsa.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	j, _ := json.Marshal(map[string]string{
		"client_email": "svc@project.iam.gserviceaccount.com",
		"private_key":  string(pemBytes),
		"token_uri":    tokenURI,
	})
	return j, &key.PublicKey
}

func TestServiceAccountToken(t *testing.T) {
	var gotAssertion string
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotAssertion = r.Form.Get("assertion")
		if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			w.WriteHeader(400)
			return
		}
		calls++
		json.NewEncoder(w).Encode(map[string]any{"access_token": "ya29.token", "expires_in": 3600})
	}))
	defer srv.Close()

	keyJSON, pub := makeServiceAccountJSON(t, srv.URL)
	ts, err := NewServiceAccountTokenSource(keyJSON, CalendarScope, "")
	if err != nil {
		t.Fatal(err)
	}

	tok, err := ts.Token(context.Background())
	if err != nil || tok != "ya29.token" {
		t.Fatalf("token: %q err=%v", tok, err)
	}

	// Verify the JWT: 3 parts, RS256 signature valid, correct claims.
	parts := strings.Split(gotAssertion, ".")
	if len(parts) != 3 {
		t.Fatalf("assertion not a JWT: %d parts", len(parts))
	}
	signingInput := parts[0] + "." + parts[1]
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	h := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], sig); err != nil {
		t.Fatalf("JWT signature invalid: %v", err)
	}
	claimsJSON, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]any
	_ = json.Unmarshal(claimsJSON, &claims)
	if claims["iss"] != "svc@project.iam.gserviceaccount.com" {
		t.Errorf("iss wrong: %v", claims["iss"])
	}
	if claims["scope"] != CalendarScope || claims["aud"] != srv.URL {
		t.Errorf("scope/aud wrong: %v %v", claims["scope"], claims["aud"])
	}

	// Second call is served from cache (no new token exchange).
	if _, err := ts.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 token exchange (cached after), got %d", calls)
	}
}

func TestServiceAccount_DelegationSubject(t *testing.T) {
	var gotAssertion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotAssertion = r.Form.Get("assertion")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "t", "expires_in": 3600})
	}))
	defer srv.Close()
	keyJSON, _ := makeServiceAccountJSON(t, srv.URL)
	ts, _ := NewServiceAccountTokenSource(keyJSON, CalendarScope, "user@company.com")
	_, _ = ts.Token(context.Background())

	parts := strings.Split(gotAssertion, ".")
	claimsJSON, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]any
	_ = json.Unmarshal(claimsJSON, &claims)
	if claims["sub"] != "user@company.com" {
		t.Fatalf("delegation subject not set: %v", claims["sub"])
	}
}

func TestServiceAccount_RejectsBadKey(t *testing.T) {
	if _, err := NewServiceAccountTokenSource([]byte(`{"client_email":"x"}`), "", ""); err == nil {
		t.Fatal("expected error for missing private_key")
	}
}
