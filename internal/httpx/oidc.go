package httpx

import (
	"net/http"

	"github.com/arvinizadi/fathom/internal/rbac"
)

// IdentityProvider authenticates an admin via the company's OIDC/SSO provider
// (Zoho Directory SSO, Microsoft Entra ID, or Google Workspace) and returns the
// resulting principal (spec §39). Implementations perform the OIDC authorization
// code flow and verify the ID token against the provider's JWKS; MFA is enforced
// by the IdP. No local password database is created (spec §39-40).
//
// This is the integration seam: a concrete implementation is supplied per the
// company's IdP. Employees do NOT authenticate here — they use Zoho OAuth
// directly (spec §40); their session is issued after the OAuth callback.
type IdentityProvider interface {
	// AuthCodeURL returns the IdP authorization URL for the given state.
	AuthCodeURL(state string) string
	// Exchange verifies the callback and returns the authenticated principal.
	Exchange(r *http.Request) (*rbac.Principal, error)
}

// LoginHandler drives admin OIDC login and issues the session (spec §38-39).
type LoginHandler struct {
	idp      IdentityProvider
	sessions *rbac.SessionManager
}

func NewLoginHandler(idp IdentityProvider, sessions *rbac.SessionManager) *LoginHandler {
	return &LoginHandler{idp: idp, sessions: sessions}
}

// Register wires login/callback/logout. Login/callback are only active when an
// IdP is configured; otherwise they report that SSO is not configured rather
// than exposing any insecure fallback (spec §39).
func (h *LoginHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", h.login)
	mux.HandleFunc("GET /auth/callback", h.callback)
	mux.HandleFunc("POST /logout", h.logout)
}

func (h *LoginHandler) login(w http.ResponseWriter, r *http.Request) {
	if h.idp == nil {
		http.Error(w, "SSO is not configured", http.StatusNotImplemented)
		return
	}
	// A production implementation stores a signed state cookie for CSRF here.
	http.Redirect(w, r, h.idp.AuthCodeURL("state"), http.StatusFound)
}

func (h *LoginHandler) callback(w http.ResponseWriter, r *http.Request) {
	if h.idp == nil {
		http.Error(w, "SSO is not configured", http.StatusNotImplemented)
		return
	}
	p, err := h.idp.Exchange(r)
	if err != nil {
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	if _, err := h.sessions.Issue(w, *p); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (h *LoginHandler) logout(w http.ResponseWriter, r *http.Request) {
	h.sessions.Clear(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
