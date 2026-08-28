package httpx

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/google"
	"github.com/arvinizadi/fathom/internal/rbac"
)

// DestinationCalendar is the destination Google Calendar the app writes to.
type DestinationCalendar interface {
	ListEvents(ctx context.Context, timeMin, timeMax time.Time) ([]google.ListedEvent, error)
	CreateEvent(ctx context.Context, e google.Event) (string, error)
}

// Destination resolves the connected destination calendar for an org (from the
// stored Google credential), or reports it as not connected.
type Destination struct {
	Cal            DestinationCalendar
	CalendarID     string
	ConnectedEmail string
	Connected      bool
}

// DestinationResolver builds the destination for an org per request.
type DestinationResolver func(ctx context.Context, org db.OrgID) Destination

// DestinationHandler exposes the Fathom-watched calendar: status, list, and
// manual add. Behind admin auth + CSRF; requires ManageCalendars.
type DestinationHandler struct {
	resolve DestinationResolver
	auth    *AuthMiddleware
}

func NewDestinationHandler(resolve DestinationResolver, auth *AuthMiddleware) *DestinationHandler {
	return &DestinationHandler{resolve: resolve, auth: auth}
}

func (h *DestinationHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/destination/status", h.auth.RequirePerm(rbac.ManageCalendars, h.status))
	mux.HandleFunc("GET /api/v1/destination/events", h.auth.RequirePerm(rbac.ManageCalendars, h.list))
	mux.HandleFunc("POST /api/v1/destination/events", h.auth.RequirePerm(rbac.ManageCalendars, h.create))
}

func (h *DestinationHandler) status(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	d := h.resolve(r.Context(), db.OrgID(p.OrgID))
	writeJSON(w, http.StatusOK, map[string]any{
		"connected": d.Connected, "calendar_id": d.CalendarID, "connected_email": d.ConnectedEmail,
		"connect_url": "/admin/google/connect",
	})
}

func (h *DestinationHandler) list(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	d := h.resolve(r.Context(), db.OrgID(p.OrgID))
	if !d.Connected {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false, "events": []any{}})
		return
	}
	events, err := d.Cal.ListEvents(r.Context(), time.Now().Add(-7*24*time.Hour), time.Now().Add(60*24*time.Hour))
	if err != nil {
		log.Printf("destination list: %v", err)
		writeErr(w, http.StatusBadGateway, "could not read the destination calendar")
		return
	}
	if events == nil {
		events = []google.ListedEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "calendar_id": d.CalendarID, "events": events})
}

type createEventReq struct {
	Summary     string `json:"summary"`
	Location    string `json:"location"`
	Description string `json:"description"`
	Start       string `json:"start"`
	End         string `json:"end"`
}

func (h *DestinationHandler) create(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	d := h.resolve(r.Context(), db.OrgID(p.OrgID))
	if !d.Connected {
		writeErr(w, http.StatusConflict, "connect a Google Calendar first")
		return
	}
	var req createEventReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	start, err1 := time.Parse(time.RFC3339, req.Start)
	end, err2 := time.Parse(time.RFC3339, req.End)
	if req.Summary == "" || err1 != nil || err2 != nil {
		writeErr(w, http.StatusBadRequest, "summary, start and end (RFC3339) are required")
		return
	}
	if !end.After(start) {
		writeErr(w, http.StatusBadRequest, "end must be after start")
		return
	}
	id, err := d.Cal.CreateEvent(r.Context(), google.Event{
		Summary: req.Summary, Location: req.Location, Description: req.Description, Start: start, End: end,
	})
	if err != nil {
		log.Printf("destination create: %v", err)
		writeErr(w, http.StatusBadGateway, "could not create the event")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}
