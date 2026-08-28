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

// FathomWebhookCreator creates a Fathom webhook subscription (implemented by
// *fathom.Client).
type FathomWebhookCreator interface {
	CreateWebhook(ctx context.Context, spec fathom.WebhookSpec) (string, error)
}

// FathomWebhookStore persists the registered webhook (implemented by
// *tokens.FathomWebhookStore).
type FathomWebhookStore interface {
	Save(ctx context.Context, org db.OrgID, webhookID, destURL string) error
	Get(ctx context.Context, org db.OrgID) (*tokens.FathomWebhook, error)
}

// FathomAdminHandler lets an admin register the Fathom webhook so recordings are
// linked back to meetings (spec §43). Requires ManageCalendars.
type FathomAdminHandler struct {
	client     FathomWebhookCreator
	store      FathomWebhookStore
	auth       *AuthMiddleware
	publicBase string
	apiKeySet  bool
}

func NewFathomAdminHandler(client FathomWebhookCreator, store FathomWebhookStore, auth *AuthMiddleware, publicBase string, apiKeySet bool) *FathomAdminHandler {
	return &FathomAdminHandler{client: client, store: store, auth: auth, publicBase: publicBase, apiKeySet: apiKeySet}
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
		"api_key_configured": h.apiKeySet, "registered": registered, "destination_url": destURL,
	})
}

func (h *FathomAdminHandler) register(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	if !h.apiKeySet || h.client == nil {
		writeErr(w, http.StatusServiceUnavailable, "set FATHOM_API_KEY first")
		return
	}
	id, err := h.client.CreateWebhook(r.Context(), fathom.WebhookSpec{
		DestinationURL: h.destURL(), IncludeTranscript: true, IncludeSummary: true, IncludeActionItems: true,
	})
	if err != nil {
		log.Printf("fathom register: %v", err)
		writeErr(w, http.StatusBadGateway, "could not register the webhook with Fathom")
		return
	}
	if err := h.store.Save(r.Context(), db.OrgID(p.OrgID), id, h.destURL()); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save webhook")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhook_id": id, "destination_url": h.destURL()})
}
