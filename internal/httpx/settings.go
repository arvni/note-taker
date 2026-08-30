package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/rbac"
	"github.com/arvinizadi/fathom/internal/tokens"
)

// ZohoSettingsRepo persists per-org Zoho config (implemented by
// *tokens.ZohoSettingsStore).
type ZohoSettingsRepo interface {
	Get(ctx context.Context, org db.OrgID) (*tokens.ZohoSettings, error)
	Save(ctx context.Context, org db.OrgID, in tokens.ZohoSettings) error
}

// AppSettingsRepo persists per-org app settings (email, branding, Fathom).
type AppSettingsRepo interface {
	Get(ctx context.Context, org db.OrgID) (*tokens.AppSettings, error)
	Save(ctx context.Context, org db.OrgID, in tokens.AppSettings) error
}

// SettingsHandler lets an admin configure the org's Zoho app + email/branding/
// Fathom in the UI, so one deployment serves any org (spec §8, §21-23, §53).
// Requires ManagePolicies.
type SettingsHandler struct {
	store     ZohoSettingsRepo
	app       AppSettingsRepo
	auth      *AuthMiddleware
	redirect  string // the Zoho callback URI to register in the Zoho console
	gRedirect string // the Google OAuth callback URI to register in Google Cloud
}

func NewSettingsHandler(store ZohoSettingsRepo, app AppSettingsRepo, auth *AuthMiddleware, redirect, googleRedirect string) *SettingsHandler {
	return &SettingsHandler{store: store, app: app, auth: auth, redirect: redirect, gRedirect: googleRedirect}
}

func (h *SettingsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/settings/zoho", h.auth.RequirePerm(rbac.ManagePolicies, h.get))
	mux.HandleFunc("PUT /api/v1/settings/zoho", h.auth.RequirePerm(rbac.ManagePolicies, h.put))
	mux.HandleFunc("GET /api/v1/settings/app", h.auth.RequirePerm(rbac.ManagePolicies, h.getApp))
	mux.HandleFunc("PUT /api/v1/settings/app", h.auth.RequirePerm(rbac.ManagePolicies, h.putApp))
}

func (h *SettingsHandler) getApp(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	out := map[string]any{
		"smtp_host": "", "smtp_port": "587", "smtp_user": "", "has_smtp_pass": false, "email_from": "",
		"company_name": "", "app_name": "", "support_addr": "", "privacy_url": "", "terms_url": "",
		"has_fathom_key":         false,
		"google_oauth_client_id": "", "has_google_secret": false,
		"google_redirect_uri": h.gRedirect, // register this in Google Cloud Console
	}
	if a, err := h.app.Get(r.Context(), db.OrgID(p.OrgID)); err == nil {
		out["smtp_host"] = a.SMTPHost
		out["smtp_port"] = a.SMTPPort
		out["smtp_user"] = a.SMTPUser
		out["has_smtp_pass"] = a.SMTPPass != ""
		out["email_from"] = a.EmailFrom
		out["company_name"] = a.CompanyName
		out["app_name"] = a.AppName
		out["support_addr"] = a.SupportAddr
		out["privacy_url"] = a.PrivacyURL
		out["terms_url"] = a.TermsURL
		out["has_fathom_key"] = a.FathomAPIKey != ""
		out["google_oauth_client_id"] = a.GoogleOAuthClientID
		out["has_google_secret"] = a.GoogleOAuthClientSecret != ""
	} else if !errors.Is(err, tokens.ErrNoAppSettings) {
		log.Printf("settings getApp org=%d: %v", p.OrgID, err)
		writeErr(w, http.StatusInternalServerError, "could not load settings")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type appSettingsReq struct {
	SMTPHost       string `json:"smtp_host"`
	SMTPPort       string `json:"smtp_port"`
	SMTPUser       string `json:"smtp_user"`
	SMTPPass       string `json:"smtp_pass"`
	EmailFrom      string `json:"email_from"`
	CompanyName    string `json:"company_name"`
	AppName        string `json:"app_name"`
	SupportAddr    string `json:"support_addr"`
	PrivacyURL     string `json:"privacy_url"`
	TermsURL       string `json:"terms_url"`
	FathomKey      string `json:"fathom_api_key"`
	GoogleClientID string `json:"google_oauth_client_id"`
	GoogleSecret   string `json:"google_oauth_client_secret"`
}

func (h *SettingsHandler) putApp(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	var req appSettingsReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := h.app.Save(r.Context(), db.OrgID(p.OrgID), tokens.AppSettings{
		SMTPHost: req.SMTPHost, SMTPPort: req.SMTPPort, SMTPUser: req.SMTPUser, SMTPPass: req.SMTPPass,
		EmailFrom: req.EmailFrom, CompanyName: req.CompanyName, AppName: req.AppName,
		SupportAddr: req.SupportAddr, PrivacyURL: req.PrivacyURL, TermsURL: req.TermsURL,
		FathomAPIKey:            req.FathomKey,
		GoogleOAuthClientID:     req.GoogleClientID,
		GoogleOAuthClientSecret: req.GoogleSecret,
	}); err != nil {
		log.Printf("settings putApp org=%d: %v", p.OrgID, err)
		writeErr(w, http.StatusInternalServerError, "could not save settings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *SettingsHandler) get(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	out := map[string]any{
		"configured": false, "client_id": "", "has_secret": false,
		"accounts_base": "https://accounts.zoho.com",
		"calendar_base": "https://calendar.zoho.com/api/v1",
		"scopes":        "ZohoCalendar.calendar.READ,ZohoCalendar.event.READ,aaaserver.profile.READ",
		"redirect_uri":  h.redirect, // register this in the Zoho API Console
	}
	if zs, err := h.store.Get(r.Context(), db.OrgID(p.OrgID)); err == nil {
		out["configured"] = zs.ClientID != ""
		out["client_id"] = zs.ClientID
		out["has_secret"] = zs.ClientSecret != ""
		out["accounts_base"] = zs.AccountsBase
		out["calendar_base"] = zs.CalendarBase
		out["scopes"] = zs.Scopes
		out["directory_users_url"] = zs.DirectoryUsersURL
	} else if !errors.Is(err, tokens.ErrNoZohoSettings) {
		log.Printf("settings getZoho org=%d: %v", p.OrgID, err)
		writeErr(w, http.StatusInternalServerError, "could not load settings")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type zohoSettingsReq struct {
	ClientID          string `json:"client_id"`
	ClientSecret      string `json:"client_secret"`
	AccountsBase      string `json:"accounts_base"`
	CalendarBase      string `json:"calendar_base"`
	Scopes            string `json:"scopes"`
	DirectoryUsersURL string `json:"directory_users_url"`
}

func (h *SettingsHandler) put(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	org := db.OrgID(p.OrgID)
	var req zohoSettingsReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.ClientID == "" {
		writeErr(w, http.StatusBadRequest, "client_id is required")
		return
	}
	// If the secret is left blank on update, keep the existing one.
	if req.ClientSecret == "" {
		if existing, err := h.store.Get(r.Context(), org); err == nil && existing.ClientSecret != "" {
			req.ClientSecret = existing.ClientSecret
		} else {
			writeErr(w, http.StatusBadRequest, "client_secret is required")
			return
		}
	}
	if err := h.store.Save(r.Context(), org, tokens.ZohoSettings{
		ClientID: req.ClientID, ClientSecret: req.ClientSecret,
		AccountsBase: req.AccountsBase, CalendarBase: req.CalendarBase,
		Scopes: req.Scopes, DirectoryUsersURL: req.DirectoryUsersURL,
	}); err != nil {
		log.Printf("settings putZoho org=%d: %v", p.OrgID, err)
		writeErr(w, http.StatusInternalServerError, "could not save settings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
