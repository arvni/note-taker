package calendar

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// PendingEvent is a mapping row the sync engine needs to act on.
type PendingEvent struct {
	ID                 int64
	EmployeeID         int64
	CalendarUID        string
	SourceEventID      string
	Title              string
	StartsAt           *time.Time
	EndsAt             *time.Time
	MeetingProvider    string
	MeetingURL         string
	SourceUpdatedAt    *time.Time
	DestinationEventID string
}

// ListToCreate returns qualifying mappings that have no destination event yet
// and are not cancelled (spec §12 objective).
func (r *Repo) ListToCreate(ctx context.Context, employeeID int64) ([]PendingEvent, error) {
	return r.listPending(ctx, `
		SELECT id, employee_id, calendar_uid, source_event_id, coalesce(title,''),
		       starts_at, ends_at, coalesce(meeting_provider,''), coalesce(meeting_url,''),
		       source_updated_at, ''
		FROM event_mappings
		WHERE employee_id = $1 AND destination_event_id IS NULL AND cancelled_at IS NULL`, employeeID)
}

// ListToUpdate returns mappings whose source changed since it was last synced to
// the destination (spec §14 objective).
func (r *Repo) ListToUpdate(ctx context.Context, employeeID int64) ([]PendingEvent, error) {
	return r.listPending(ctx, `
		SELECT id, employee_id, calendar_uid, source_event_id, coalesce(title,''),
		       starts_at, ends_at, coalesce(meeting_provider,''), coalesce(meeting_url,''),
		       source_updated_at, destination_event_id
		FROM event_mappings
		WHERE employee_id = $1 AND destination_event_id IS NOT NULL AND cancelled_at IS NULL
		  AND source_updated_at IS NOT NULL
		  AND (destination_synced_at IS NULL OR source_updated_at > destination_synced_at)`, employeeID)
}

// ListToCancel returns mappings with a destination event that were not seen in
// the current scan (i.e. cancelled at the source) (spec §15 objective).
func (r *Repo) ListToCancel(ctx context.Context, employeeID int64, scanStart time.Time) ([]PendingEvent, error) {
	return r.listPending(ctx, `
		SELECT id, employee_id, calendar_uid, source_event_id, coalesce(title,''),
		       starts_at, ends_at, coalesce(meeting_provider,''), coalesce(meeting_url,''),
		       source_updated_at, destination_event_id
		FROM event_mappings
		WHERE employee_id = $1 AND destination_event_id IS NOT NULL AND cancelled_at IS NULL
		  AND last_seen_at < $2`, employeeID, scanStart)
}

func (r *Repo) listPending(ctx context.Context, q string, args ...any) ([]PendingEvent, error) {
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingEvent
	for rows.Next() {
		var e PendingEvent
		if err := rows.Scan(&e.ID, &e.EmployeeID, &e.CalendarUID, &e.SourceEventID, &e.Title,
			&e.StartsAt, &e.EndsAt, &e.MeetingProvider, &e.MeetingURL, &e.SourceUpdatedAt,
			&e.DestinationEventID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MarkCreated records the destination event id and marks it synced to the
// current source version (spec §13 objective; idempotency).
func (r *Repo) MarkCreated(ctx context.Context, mappingID int64, destEventID string, sourceUpdatedAt *time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE event_mappings
		SET destination_event_id = $1, destination_synced_at = $2, updated_at = now()
		WHERE id = $3`, destEventID, sourceUpdatedAt, mappingID)
	return err
}

// MarkUpdated advances the synced-source marker after a destination update.
func (r *Repo) MarkUpdated(ctx context.Context, mappingID int64, sourceUpdatedAt *time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE event_mappings SET destination_synced_at = $1, updated_at = now()
		WHERE id = $2`, sourceUpdatedAt, mappingID)
	return err
}

// MarkCancelled records that a destination event was removed.
func (r *Repo) MarkCancelled(ctx context.Context, mappingID int64) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE event_mappings SET cancelled_at = now(), updated_at = now()
		WHERE id = $1`, mappingID)
	return err
}

// RecordedMatch identifies the employee/event a Fathom recording corresponds to.
type RecordedMatch struct {
	EmployeeID    int64
	SourceEventID string
	Title         string
}

// FindByMeetingURL correlates a Fathom webhook's meeting_url back to a synced
// meeting so the recording chain can be confirmed and audited (spec §42).
func (r *Repo) FindByMeetingURL(ctx context.Context, meetingURL string) (*RecordedMatch, bool, error) {
	var m RecordedMatch
	err := r.pool.QueryRow(ctx, `
		SELECT employee_id, source_event_id, coalesce(title,'')
		FROM event_mappings
		WHERE meeting_url = $1
		ORDER BY updated_at DESC
		LIMIT 1`, meetingURL).Scan(&m.EmployeeID, &m.SourceEventID, &m.Title)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &m, true, nil
}

// Now returns the database clock time, used as the scan boundary so it is
// comparable to last_seen_at (also set by the DB clock), avoiding app/DB clock
// skew when deciding cancellations.
func (r *Repo) Now(ctx context.Context) (time.Time, error) {
	var t time.Time
	err := r.pool.QueryRow(ctx, `SELECT now()`).Scan(&t)
	return t, err
}
