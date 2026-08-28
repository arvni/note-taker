// Package httpx wires the HTTP surface: the consent/OAuth flow, dashboards, and
// API. This file implements the Zoho onboarding OAuth flow (spec §8-13, §24-26,
// §44).
package httpx

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"time"

	"github.com/arvinizadi/fathom/internal/audit"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/directory"
	"github.com/arvinizadi/fathom/internal/oauth"
	"github.com/arvinizadi/fathom/internal/onboarding"
	"github.com/arvinizadi/fathom/internal/tokens"
)

// stateTTL bounds the OAuth CSRF state lifetime (spec §9).
const stateTTL = 10 * time.Minute

// OAuthDeps are the collaborators the OAuth flow needs.
type OAuthDeps struct {
	Onboarding *onboarding.Repo
	States     *oauth.StateRepo
	Employees  *directory.Repo
	Creds      *tokens.Store
	Zoho       func(ctx context.Context, org db.OrgID) (oauth.OrgConfig, error)
	Audit      *audit.Logger
	Templates  *template.Template

	CompanyName string
	AppName     string
	Permissions []string
	SupportAddr string
	PrivacyURL  string
	TermsURL    string
	Security    SecurityRecorder
	ConnectRL   Middleware
	OAuthRL     Middleware
}

// SecurityRecorder records security events for threshold alerting (spec §36).
type SecurityRecorder interface {
	RecordEvent(ctx context.Context, event, key string)
}

// OAuthHandler serves the consent page and the Zoho OAuth start/callback.
type OAuthHandler struct{ d OAuthDeps }

func NewOAuthHandler(d OAuthDeps) *OAuthHandler { return &OAuthHandler{d: d} }

// Register wires the routes onto mux (spec §8).
func (h *OAuthHandler) Register(mux *http.ServeMux) {
	connect := chain(h.d.ConnectRL)
	oauthMW := chain(h.d.OAuthRL)
	mux.HandleFunc("GET /connect/{token}", connect(h.connect))
	mux.HandleFunc("GET /oauth/zoho/start", oauthMW(h.start))
	mux.HandleFunc("GET /oauth/zoho/callback", oauthMW(h.callback))
}

// connect renders the consent page for a valid onboarding token WITHOUT
// consuming it (spec §5, §23). The token is validated via Peek.
func (h *OAuthHandler) connect(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	res, err := h.d.Onboarding.Peek(r.Context(), token)
	if err != nil {
		h.renderError(w, http.StatusNotFound, "This link is invalid or has expired.")
		return
	}
	emp, err := h.d.Employees.GetByID(r.Context(), res.OrgID, res.EmployeeID)
	if err != nil {
		h.renderError(w, http.StatusNotFound, "This link is invalid or has expired.")
		return
	}
	h.audit(r.Context(), res.OrgID, res.EmployeeID, audit.InvitationOpened, nil)

	h.render(w, "consent.html", map[string]any{
		"CompanyName":   h.d.CompanyName,
		"AppName":       h.d.AppName,
		"EmployeeEmail": emp.Email,
		"Permissions":   h.d.Permissions,
		"StartURL":      "/oauth/zoho/start?t=" + template.URLQueryEscaper(token),
		"PrivacyURL":    h.d.PrivacyURL,
		"TermsURL":      h.d.TermsURL,
	})
}

// start consumes the single-use onboarding token, creates CSRF state, and
// redirects to Zoho (spec §8-9, §44 steps 12-14).
func (h *OAuthHandler) start(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("t")
	res, err := h.d.Onboarding.Consume(r.Context(), token)
	if err != nil {
		h.security("suspicious_onboarding", clientIP(r))
		h.renderError(w, http.StatusBadRequest, "This link is invalid, expired, or already used.")
		return
	}

	if err := h.d.Employees.SetOnboardingStatus(r.Context(), res.OrgID, res.EmployeeID, string(onboarding.AuthorizationStarted)); err != nil {
		log.Printf("oauth start: set status: %v", err)
	}

	oc, err := h.d.Zoho(r.Context(), res.OrgID)
	if err != nil || !oc.Configured() {
		h.renderError(w, http.StatusServiceUnavailable, "Calendar integration is not configured yet. Please contact your administrator.")
		return
	}
	rawState, stateHash, err := oauth.GenerateState()
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Could not start authorization. Please try again.")
		return
	}
	if err := h.d.States.Create(r.Context(), res.EmployeeID, stateHash, time.Now().Add(stateTTL)); err != nil {
		h.renderError(w, http.StatusInternalServerError, "Could not start authorization. Please try again.")
		return
	}
	h.audit(r.Context(), res.OrgID, res.EmployeeID, audit.OAuthStarted, nil)

	http.Redirect(w, r, oc.Client().AuthorizeURL(rawState), http.StatusFound)
}

// callback validates state, exchanges the code server-side, verifies the Zoho
// identity against the employee, encrypts and stores the credential, and shows
// the confirmation page (spec §9, §12, §25-26, §44 steps 18-24).
func (h *OAuthHandler) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		h.renderError(w, http.StatusBadRequest, "Authorization was declined or failed at Zoho.")
		return
	}
	rawState := q.Get("state")
	code := q.Get("code")
	if rawState == "" || code == "" {
		h.renderError(w, http.StatusBadRequest, "Missing authorization parameters.")
		return
	}

	consumed, err := h.d.States.Consume(r.Context(), rawState)
	if err != nil {
		// Invalid/expired/replayed state — possible CSRF (spec §9, §48).
		h.renderError(w, http.StatusBadRequest, "Your session has expired. Please use your invitation link again.")
		return
	}
	org, empID := consumed.OrgID, consumed.EmployeeID

	emp, err := h.d.Employees.GetByID(r.Context(), org, empID)
	if err != nil {
		h.fail(r.Context(), org, empID, "employee lookup failed")
		h.renderError(w, http.StatusInternalServerError, "Something went wrong. Please contact your administrator.")
		return
	}

	oc, err := h.d.Zoho(r.Context(), org)
	if err != nil || !oc.Configured() {
		h.fail(r.Context(), org, empID, "zoho not configured")
		h.renderError(w, http.StatusServiceUnavailable, "Calendar integration is not configured. Please contact your administrator.")
		return
	}
	zohoClient := oc.Client()

	tok, err := zohoClient.ExchangeCode(r.Context(), code)
	if err != nil {
		h.fail(r.Context(), org, empID, "token exchange failed")
		h.security("failed_oauth", clientIP(r))
		h.renderError(w, http.StatusBadGateway, "Zoho authorization could not be completed. Please try again.")
		return
	}

	id, err := oauth.FetchIdentity(r.Context(), oc.AccountsBase, tok.AccessToken)
	if err != nil {
		h.fail(r.Context(), org, empID, "identity fetch failed")
		h.security("failed_oauth", clientIP(r))
		h.renderError(w, http.StatusBadGateway, "Could not verify your Zoho identity. Please try again.")
		return
	}

	// Account binding (spec §25-26): the authenticated Zoho identity must match
	// the expected employee. Never trust the browser-supplied email.
	if err := oauth.VerifyBinding(id, emp.ZohoUserID, emp.Email); err != nil {
		h.fail(r.Context(), org, empID, "identity mismatch")
		h.security("identity_mismatch", clientIP(r))
		h.security("failed_oauth", clientIP(r))
		h.renderError(w, http.StatusForbidden,
			"The Zoho account you signed in with does not match your company record. An administrator has been notified.")
		return
	}
	// First authorization establishes the binding for directory sources (CSV)
	// that had no Zoho id yet.
	if emp.ZohoUserID == "" && id.ZohoUserID != "" {
		if err := h.d.Employees.SetZohoUserID(r.Context(), org, empID, id.ZohoUserID); err != nil {
			log.Printf("oauth callback: bind zoho id: %v", err)
		}
	}

	if err := h.d.Creds.Upsert(r.Context(), tokens.Credential{
		EmployeeID:        empID,
		ProviderAccountID: id.ZohoUserID,
		AccessToken:       tok.AccessToken,
		RefreshToken:      tok.RefreshToken,
		AccessExpiresAt:   tok.ExpiresAt(),
		Scopes:            oc.Scopes,
		APIDomain:         tok.APIDomain,
		Status:            tokens.StatusActive,
	}); err != nil {
		h.fail(r.Context(), org, empID, "credential storage failed")
		h.renderError(w, http.StatusInternalServerError, "Something went wrong saving your authorization. Please try again.")
		return
	}

	if err := h.d.Employees.SetOnboardingStatus(r.Context(), org, empID, string(onboarding.Authorized)); err != nil {
		log.Printf("oauth callback: set authorized: %v", err)
	}
	h.audit(r.Context(), org, empID, audit.OAuthCompleted, map[string]any{"zoho_account": id.ZohoUserID})

	h.render(w, "connected.html", map[string]any{
		"EmployeeEmail": emp.Email,
		"Permissions":   h.d.Permissions,
	})
}

// fail marks the employee authorization_failed and audits (spec §25, §35).
func (h *OAuthHandler) fail(ctx context.Context, org db.OrgID, empID int64, reason string) {
	if err := h.d.Employees.SetOnboardingStatus(ctx, org, empID, string(onboarding.AuthorizationFailed)); err != nil {
		log.Printf("oauth fail: set status: %v", err)
	}
	h.audit(ctx, org, empID, audit.OAuthFailed, map[string]any{"reason": reason})
}

func (h *OAuthHandler) security(event, key string) {
	if h.d.Security != nil {
		h.d.Security.RecordEvent(context.Background(), event, key)
	}
}

func (h *OAuthHandler) audit(ctx context.Context, org db.OrgID, empID int64, action audit.Action, md map[string]any) {
	if h.d.Audit == nil {
		return
	}
	_ = h.d.Audit.Log(ctx, audit.Entry{
		OrganizationID: int64(org), EmployeeID: empID, Action: action, Metadata: md,
	})
}

func (h *OAuthHandler) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.d.Templates.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("httpx: render %s: %v", name, err)
	}
}

func (h *OAuthHandler) renderError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = h.d.Templates.ExecuteTemplate(w, "error.html", map[string]any{
		"Message":        msg,
		"SupportContact": h.d.SupportAddr,
	})
}
