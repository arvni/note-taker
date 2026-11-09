package httpx

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/directory"
	"github.com/arvinizadi/fathom/internal/rbac"
	"github.com/arvinizadi/fathom/internal/synccontrol"
	"github.com/redis/go-redis/v9"
)

// SyncControlHandler lets admins trigger a calendar resync ("Sync now" or
// "Full resync") and see the health of the sync: last run, the Fathom
// destination calendar's connection, and employees whose Zoho link broke.
type SyncControlHandler struct {
	rdb      *redis.Client
	emps     *directory.Repo
	auth     *AuthMiddleware
	interval func(r *http.Request) time.Duration
}

func NewSyncControlHandler(rdb *redis.Client, emps *directory.Repo, auth *AuthMiddleware, interval func(r *http.Request) time.Duration) *SyncControlHandler {
	return &SyncControlHandler{rdb: rdb, emps: emps, auth: auth, interval: interval}
}

func (h *SyncControlHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/sync/status", h.auth.RequirePerm(rbac.ManageCalendars, h.status))
	mux.HandleFunc("POST /api/v1/sync/run", h.auth.RequirePerm(rbac.ManageCalendars, h.run))
}

func (h *SyncControlHandler) run(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Full bool `json:"full"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid body")
			return
		}
	}
	if err := synccontrol.Request(r.Context(), h.rdb, req.Full); err != nil {
		log.Printf("sync control: request: %v", err)
		writeErr(w, http.StatusInternalServerError, "could not queue the sync")
		return
	}
	p, _ := PrincipalFrom(r)
	log.Printf("sync control: %s requested a %s sync", p.Email, map[bool]string{true: "full", false: "quick"}[req.Full])
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "full": req.Full})
}

func (h *SyncControlHandler) status(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	ctx := r.Context()
	last, err := synccontrol.LoadLast(ctx, h.rdb)
	if err != nil {
		log.Printf("sync control: last run: %v", err)
	}
	pending, pendingFull := synccontrol.Pending(ctx, h.rdb)
	destErr, err := synccontrol.LoadDestError(ctx, h.rdb, p.OrgID)
	if err != nil {
		log.Printf("sync control: destination state: %v", err)
	}
	disconnected, err := h.emps.ListDisconnected(ctx, db.OrgID(p.OrgID))
	if err != nil {
		log.Printf("sync control: disconnected employees: %v", err)
		disconnected = []directory.Disconnected{}
	}
	out := map[string]any{
		"last_run":          last,
		"pending":           pending,
		"pending_full":      pendingFull,
		"destination_error": destErr,
		"disconnected":      disconnected,
	}
	if h.interval != nil {
		out["interval_seconds"] = int(h.interval(r).Seconds())
	}
	writeJSON(w, http.StatusOK, out)
}
