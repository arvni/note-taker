package httpx

import (
	"context"
	"log"
	"net/http"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/fathom"
	"github.com/arvinizadi/fathom/internal/rbac"
	"github.com/arvinizadi/fathom/internal/tokens"
)

// FathomWebhookStore persists the registered webhook (implemented by
// *tokens.FathomWebhookStore).
type FathomWebhookStore interface {
	Save(ctx context.Context, org db.OrgID, webhookID, destURL string) error
	Get(ctx context.Context, org db.OrgID) (*tokens.FathomWebhook, error)
}

// FathomAdminHandler lets an admin register the Fathom webhook so recordings are
// linked back to meetings (spec §43). The API key is resolved per org (stored
// settings, env fallback). Requires ManageCalendars.
type FathomAdminHandler struct {
	apiBase    string
	keyFor     func(ctx context.Context, org db.OrgID) string
	store      FathomWebhookStore
	auth       *AuthMiddleware
	publicBase string
}

func NewFathomAdminHandler(apiBase string, keyFor func(ctx context.Context, org db.OrgID) string, store FathomWebhookStore, auth *AuthMiddleware, publicBase string) *FathomAdminHandler {
	return &FathomAdminHandler{apiBase: apiBase, keyFor: keyFor, store: store, auth: auth, publicBase: publicBase}
}

func (h *FathomAdminHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/fathom/status", h.auth.RequirePerm(rbac.ManageCalendars, h.status))
	mux.HandleFunc("POST /api/v1/fathom/register", h.auth.RequirePerm(rbac.ManageCalendars, h.register))
}

func (h *FathomAdminHandler) destURL() string { return h.publicBase + "/webhooks/fathom" }

func (h *FathomAdminHandler) status(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	registered := false
	destURL := h.destURL()
	if wh, err := h.store.Get(r.Context(), db.OrgID(p.OrgID)); err == nil {
		registered = true
		destURL = wh.DestinationURL
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"api_key_configured": h.keyFor(r.Context(), db.OrgID(p.OrgID)) != "",
		"registered":         registered, "destination_url": destURL,
	})
}

func (h *FathomAdminHandler) register(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	org := db.OrgID(p.OrgID)
	key := h.keyFor(r.Context(), org)
	if key == "" {
		writeErr(w, http.StatusServiceUnavailable, "set a Fathom API key in Settings first")
		return
	}
	client := fathom.NewClient(h.apiBase, key)
	id, err := client.CreateWebhook(r.Context(), fathom.WebhookSpec{
		DestinationURL: h.destURL(), IncludeTranscript: true, IncludeSummary: true, IncludeActionItems: true,
	})
	if err != nil {
		log.Printf("fathom register: %v", err)
		writeErr(w, http.StatusBadGateway, "could not register the webhook with Fathom")
		return
	}
	if err := h.store.Save(r.Context(), org, id, h.destURL()); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save webhook")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhook_id": id, "destination_url": h.destURL()})
}
