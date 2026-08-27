// Package sync orchestrates the outbound flow: scan Zoho for qualifying meetings
// (Phase 6), then create, update, or cancel the corresponding destination events
// in Google Calendar (spec §12-16 objectives, §42). Idempotency comes from the
// event_mappings unique (employee_id, source_event_id) constraint plus per-row
// destination tracking (spec §48).
package sync

import (
	"context"
	"log"
	"time"

	"github.com/arvinizadi/fathom/internal/audit"
	"github.com/arvinizadi/fathom/internal/calendar"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/google"
)

// Scanner refreshes the source event mappings for an employee (calendar.Service).
type Scanner interface {
	Scan(ctx context.Context, org db.OrgID, employeeID int64) (int, error)
}

// Destination creates/updates/deletes events in the destination calendar
// (google.Client).
type Destination interface {
	CreateEvent(ctx context.Context, e google.Event) (string, error)
	UpdateEvent(ctx context.Context, eventID string, e google.Event) error
	DeleteEvent(ctx context.Context, eventID string) error
}

// MappingRepo is the sync-state persistence (calendar.Repo).
type MappingRepo interface {
	Now(ctx context.Context) (time.Time, error)
	ListToCreate(ctx context.Context, employeeID int64) ([]calendar.PendingEvent, error)
	ListToUpdate(ctx context.Context, employeeID int64) ([]calendar.PendingEvent, error)
	ListToCancel(ctx context.Context, employeeID int64, scanStart time.Time) ([]calendar.PendingEvent, error)
	MarkCreated(ctx context.Context, mappingID int64, destEventID string, sourceUpdatedAt *time.Time) error
	MarkUpdated(ctx context.Context, mappingID int64, sourceUpdatedAt *time.Time) error
	MarkCancelled(ctx context.Context, mappingID int64) error
}

// Engine runs the per-employee synchronization.
type Engine struct {
	scanner Scanner
	dest    Destination
	repo    MappingRepo
	audit   *audit.Logger
}

func NewEngine(scanner Scanner, dest Destination, repo MappingRepo, auditLog *audit.Logger) *Engine {
	return &Engine{scanner: scanner, dest: dest, repo: repo, audit: auditLog}
}

// Report summarizes a sync run.
type Report struct {
	Created   int
	Updated   int
	Cancelled int
}

// SyncEmployee scans the source, then reconciles destination events (spec §42).
func (e *Engine) SyncEmployee(ctx context.Context, org db.OrgID, employeeID int64) (Report, error) {
	var rep Report
	// Use the DB clock so scanStart is comparable to last_seen_at (also DB-set),
	// avoiding false cancellations from app/DB clock skew.
	scanStart, err := e.repo.Now(ctx)
	if err != nil {
		return rep, err
	}

	if _, err := e.scanner.Scan(ctx, org, employeeID); err != nil {
		return rep, err
	}

	// CREATE
	toCreate, err := e.repo.ListToCreate(ctx, employeeID)
	if err != nil {
		return rep, err
	}
	for _, m := range toCreate {
		ev, ok := destEvent(m)
		if !ok {
			continue // missing start/end — cannot create a valid destination event
		}
		id, err := e.dest.CreateEvent(ctx, ev)
		if err != nil {
			log.Printf("sync create employee=%d src=%s: %v", employeeID, m.SourceEventID, err)
			continue
		}
		if err := e.repo.MarkCreated(ctx, m.ID, id, m.SourceUpdatedAt); err != nil {
			return rep, err
		}
		rep.Created++
		e.log(ctx, org, employeeID, audit.MeetingSynced, map[string]any{"provider": m.MeetingProvider})
	}

	// UPDATE
	toUpdate, err := e.repo.ListToUpdate(ctx, employeeID)
	if err != nil {
		return rep, err
	}
	for _, m := range toUpdate {
		ev, ok := destEvent(m)
		if !ok {
			continue
		}
		if err := e.dest.UpdateEvent(ctx, m.DestinationEventID, ev); err != nil {
			log.Printf("sync update employee=%d src=%s: %v", employeeID, m.SourceEventID, err)
			continue
		}
		if err := e.repo.MarkUpdated(ctx, m.ID, m.SourceUpdatedAt); err != nil {
			return rep, err
		}
		rep.Updated++
	}

	// CANCEL (source events no longer seen in this scan)
	toCancel, err := e.repo.ListToCancel(ctx, employeeID, scanStart)
	if err != nil {
		return rep, err
	}
	for _, m := range toCancel {
		if err := e.dest.DeleteEvent(ctx, m.DestinationEventID); err != nil {
			log.Printf("sync cancel employee=%d src=%s: %v", employeeID, m.SourceEventID, err)
			continue
		}
		if err := e.repo.MarkCancelled(ctx, m.ID); err != nil {
			return rep, err
		}
		rep.Cancelled++
	}

	return rep, nil
}

// destEvent builds the destination event, placing the meeting URL in both
// location and description so Fathom can detect the conference (spec §29, §42).
func destEvent(m calendar.PendingEvent) (google.Event, bool) {
	if m.StartsAt == nil || m.EndsAt == nil {
		return google.Event{}, false
	}
	desc := "Synced meeting"
	if m.MeetingURL != "" {
		desc = "Join: " + m.MeetingURL
	}
	return google.Event{
		Summary:     m.Title,
		Location:    m.MeetingURL,
		Description: desc,
		Start:       *m.StartsAt,
		End:         *m.EndsAt,
	}, true
}

func (e *Engine) log(ctx context.Context, org db.OrgID, empID int64, action audit.Action, md map[string]any) {
	if e.audit == nil {
		return
	}
	_ = e.audit.Log(ctx, audit.Entry{OrganizationID: int64(org), EmployeeID: empID, Action: action, Metadata: md})
}
