package rbac

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// SessionCookieName is the session cookie name.
const SessionCookieName = "fathom_session"

// ErrInvalidSession is returned for a missing, tampered, or expired session.
var ErrInvalidSession = errors.New("rbac: invalid session")

// sessionData is the signed cookie payload.
type sessionData struct {
	Principal Principal `json:"p"`
	IssuedAt  int64     `json:"iat"`
	ExpiresAt int64     `json:"exp"`
	CSRF      string    `json:"csrf"`
}

// SessionManager issues and verifies HMAC-signed session cookies (spec §38). No
// server-side session store is required; the cookie is authenticated, not
// encrypted, so it carries no secrets.
type SessionManager struct {
	key    []byte
	ttl    time.Duration
	secure bool
}

// NewSessionManager builds a manager. secure=true sets the Secure cookie flag
// (production over HTTPS, spec §38).
func NewSessionManager(key []byte, ttl time.Duration, secure bool) *SessionManager {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	return &SessionManager{key: key, ttl: ttl, secure: secure}
}

// Issue writes a fresh signed session cookie for the principal and returns the
// CSRF token bound to it (spec §38). Rotation is achieved by calling Issue again.
func (m *SessionManager) Issue(w http.ResponseWriter, p Principal) (csrf string, err error) {
	now := time.Now()
	csrf, err = randomToken()
	if err != nil {
		return "", err
	}
	data := sessionData{Principal: p, IssuedAt: now.UnixNano(), ExpiresAt: now.Add(m.ttl).UnixNano(), CSRF: csrf}
	raw, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	value := payload + "." + m.sign(payload)

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,                 // no JS access (spec §38)
		Secure:   m.secure,             // HTTPS only in prod (spec §38)
		SameSite: http.SameSiteLaxMode, // CSRF hardening (spec §38)
		Expires:  now.Add(m.ttl),
		MaxAge:   int(m.ttl.Seconds()),
	})
	return csrf, nil
}

// Verify parses and validates the session cookie from a request (spec §38).
func (m *SessionManager) Verify(r *http.Request) (*Principal, string, error) {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return nil, "", ErrInvalidSession
	}
	dot := -1
	for i := len(c.Value) - 1; i >= 0; i-- {
		if c.Value[i] == '.' {
			dot = i
			break
		}
	}
	if dot < 0 {
		return nil, "", ErrInvalidSession
	}
	payload, sig := c.Value[:dot], c.Value[dot+1:]
	if subtle.ConstantTimeCompare([]byte(sig), []byte(m.sign(payload))) != 1 {
		return nil, "", ErrInvalidSession
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, "", ErrInvalidSession
	}
	var data sessionData
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, "", ErrInvalidSession
	}
	if time.Now().UnixNano() > data.ExpiresAt {
		return nil, "", ErrInvalidSession
	}
	return &data.Principal, data.CSRF, nil
}

// Clear removes the session cookie (logout).
func (m *SessionManager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: m.secure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

func (m *SessionManager) sign(payload string) string {
	mac := hmac.New(sha256.New, m.key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// VerifyCSRF compares a submitted CSRF token against the session's (spec §38).
func VerifyCSRF(sessionCSRF, submitted string) error {
	if submitted == "" || subtle.ConstantTimeCompare([]byte(sessionCSRF), []byte(submitted)) != 1 {
		return fmt.Errorf("rbac: CSRF token mismatch")
	}
	return nil
}
