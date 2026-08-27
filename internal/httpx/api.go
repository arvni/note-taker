package httpx

import (
	"context"
	"log"
	"net/http"
	"strconv"

	"github.com/arvinizadi/fathom/internal/db"
)

// Revoker revokes an employee's access (implemented by *tokens.Revoker).
type Revoker interface {
	Revoke(ctx context.Context, org db.OrgID, employeeID int64) error
}

// APIHandler serves authenticated admin/employee API routes.
type APIHandler struct {
	revoker Revoker
	rl      Middleware
}

func NewAPIHandler(revoker Revoker, rl Middleware) *APIHandler {
	return &APIHandler{revoker: revoker, rl: rl}
}

// Register wires API routes. These require an authenticated org on the request
// context (set by admin auth middleware, Phase 10); without it they 401 so the
// endpoints are never exposed unscoped (spec §17, §41).
func (h *APIHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/employees/{id}/revoke", chain(h.rl)(h.revoke))
}

func (h *APIHandler) revoke(w http.ResponseWriter, r *http.Request) {
	org, ok := orgFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid employee id", http.StatusBadRequest)
		return
	}
	if err := h.revoker.Revoke(r.Context(), org, id); err != nil {
		log.Printf("api: revoke employee %d: %v", id, err)
		http.Error(w, "revoke failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
