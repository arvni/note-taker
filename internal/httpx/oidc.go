package httpx

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/arvinizadi/fathom/internal/oidc"
	"github.com/arvinizadi/fathom/internal/rbac"
)

// OIDCFlow is the OIDC provider behavior the login handler needs (implemented by
// *oidc.Provider). It performs the authorization-code flow with JWKS-verified ID
// tokens (spec §39).
type OIDCFlow interface {
	AuthCodeURL(ctx context.Context, state, nonce string) (string, error)
	Exchange(ctx context.Context, code, nonce string) (*oidc.Claims, error)
}

// AdminMapper maps verified OIDC claims to an application principal, or rejects
// the login (spec §39, §41). A typical implementation is an email allowlist.
type AdminMapper interface {
	Map(c *oidc.Claims) (*rbac.Principal, error)
}

// AllowlistMapper grants org_admin to a fixed set of verified emails within one
// organization. Multi-org deployments should back this with a database table.
type AllowlistMapper struct {
	OrgID  int64
	Admins map[string]bool // lowercased emails
}

func NewAllowlistMapper(orgID int64, emails []string) *AllowlistMapper {
	m := &AllowlistMapper{OrgID: orgID, Admins: map[string]bool{}}
	for _, e := range emails {
		m.Admins[strings.ToLower(strings.TrimSpace(e))] = true
	}
	return m
}

func (m *AllowlistMapper) Map(c *oidc.Claims) (*rbac.Principal, error) {
	if !m.Admins[strings.ToLower(c.Email)] {
		return nil, fmt.Errorf("oidc: %s is not an authorized admin", c.Email)
	}
	return &rbac.Principal{OrgID: m.OrgID, Role: rbac.OrgAdmin, Subject: c.Subject, Email: c.Email}, nil
}

// LoginHandler drives admin OIDC login and issues the session (spec §38-39).
type LoginHandler struct {
	flow     OIDCFlow
	mapper   AdminMapper
	sessions *rbac.SessionManager
	stateKey []byte // HMAC key for the short-lived state/nonce cookie
	secure   bool
}

func NewLoginHandler(flow OIDCFlow, mapper AdminMapper, sessions *rbac.SessionManager, stateKey []byte, secure bool) *LoginHandler {
	return &LoginHandler{flow: flow, mapper: mapper, sessions: sessions, stateKey: stateKey, secure: secure}
}

const stateCookie = "fathom_oidc_state"

func (h *LoginHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", h.login)
	mux.HandleFunc("GET /auth/callback", h.callback)
	mux.HandleFunc("POST /logout", h.logout)
}

type stateData struct {
	State string `json:"s"`
	Nonce string `json:"n"`
	Exp   int64  `json:"e"`
}

func (h *LoginHandler) login(w http.ResponseWriter, r *http.Request) {
	if h.flow == nil {
		http.Error(w, "SSO is not configured", http.StatusNotImplemented)
		return
	}
	state, _ := randToken()
	nonce, _ := randToken()
	// Bind state+nonce to the browser via a short signed cookie (CSRF for OIDC).
	sd := stateData{State: state, Nonce: nonce, Exp: time.Now().Add(10 * time.Minute).Unix()}
	raw, _ := json.Marshal(sd)
	payload := base64.RawURLEncoding.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{
		Name: stateCookie, Value: payload + "." + h.sign(payload), Path: "/",
		HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteLaxMode, MaxAge: 600,
	})
	url, err := h.flow.AuthCodeURL(r.Context(), state, nonce)
	if err != nil {
		http.Error(w, "sso error", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

func (h *LoginHandler) callback(w http.ResponseWriter, r *http.Request) {
	if h.flow == nil {
		http.Error(w, "SSO is not configured", http.StatusNotImplemented)
		return
	}
	sd, err := h.readState(r)
	if err != nil {
		http.Error(w, "invalid or expired login session", http.StatusBadRequest)
		return
	}
	if subtle.ConstantTimeCompare([]byte(sd.State), []byte(r.URL.Query().Get("state"))) != 1 {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	claims, err := h.flow.Exchange(r.Context(), code, sd.Nonce)
	if err != nil {
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	principal, err := h.mapper.Map(claims)
	if err != nil {
		http.Error(w, "not authorized", http.StatusForbidden)
		return
	}
	// Clear the state cookie and issue the session.
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: h.secure})
	if _, err := h.sessions.Issue(w, *principal); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (h *LoginHandler) logout(w http.ResponseWriter, r *http.Request) {
	h.sessions.Clear(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *LoginHandler) readState(r *http.Request) (*stateData, error) {
	c, err := r.Cookie(stateCookie)
	if err != nil {
		return nil, err
	}
	i := strings.LastIndexByte(c.Value, '.')
	if i < 0 {
		return nil, fmt.Errorf("bad cookie")
	}
	payload, sig := c.Value[:i], c.Value[i+1:]
	if subtle.ConstantTimeCompare([]byte(sig), []byte(h.sign(payload))) != 1 {
		return nil, fmt.Errorf("bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, err
	}
	var sd stateData
	if err := json.Unmarshal(raw, &sd); err != nil {
		return nil, err
	}
	if time.Now().Unix() > sd.Exp {
		return nil, fmt.Errorf("expired")
	}
	return &sd, nil
}

func (h *LoginHandler) sign(payload string) string {
	mac := hmac.New(sha256.New, h.stateKey)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func randToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
