package httpx

import (
	"context"
	"encoding/json"
	"errors"
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

// SettingsHandler lets an admin configure the org's Zoho OAuth app in the UI, so
// one deployment serves any Zoho org (spec §8, §53). Requires ManagePolicies.
type SettingsHandler struct {
	store    ZohoSettingsRepo
	auth     *AuthMiddleware
	redirect string // the callback URI to register in the Zoho console
}

func NewSettingsHandler(store ZohoSettingsRepo, auth *AuthMiddleware, redirect string) *SettingsHandler {
	return &SettingsHandler{store: store, auth: auth, redirect: redirect}
}

func (h *SettingsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/settings/zoho", h.auth.RequirePerm(rbac.ManagePolicies, h.get))
	mux.HandleFunc("PUT /api/v1/settings/zoho", h.auth.RequirePerm(rbac.ManagePolicies, h.put))
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
		writeErr(w, http.StatusInternalServerError, "could not save settings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
