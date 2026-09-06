package calendar

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/arvinizadi/fathom/internal/db"
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
	Description        string
	Location           string
}

// ListToCreate returns qualifying mappings that have no destination event yet
// and are not cancelled (spec §12 objective).
func (r *Repo) ListToCreate(ctx context.Context, employeeID int64) ([]PendingEvent, error) {
	return r.listPending(ctx, `
		SELECT id, employee_id, calendar_uid, source_event_id, coalesce(title,''),
		       starts_at, ends_at, coalesce(meeting_provider,''), coalesce(meeting_url,''),
		       source_updated_at, '', coalesce(description,''), coalesce(location,'')
		FROM event_mappings
		WHERE employee_id = $1 AND destination_event_id IS NULL AND cancelled_at IS NULL`, employeeID)
}

// ListToUpdate returns mappings whose source changed since it was last synced to
// the destination (spec §14 objective).
func (r *Repo) ListToUpdate(ctx context.Context, employeeID int64) ([]PendingEvent, error) {
	return r.listPending(ctx, `
		SELECT id, employee_id, calendar_uid, source_event_id, coalesce(title,''),
		       starts_at, ends_at, coalesce(meeting_provider,''), coalesce(meeting_url,''),
		       source_updated_at, destination_event_id, coalesce(description,''), coalesce(location,'')
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
		       source_updated_at, destination_event_id, coalesce(description,''), coalesce(location,'')
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
			&e.DestinationEventID, &e.Description, &e.Location); err != nil {
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

// MappingView is a synced meeting for the admin detail view.
type MappingView struct {
	SourceEventID      string     `json:"source_event_id"`
	CalendarUID        string     `json:"calendar_uid"`
	Title              string     `json:"title"`
	StartsAt           *time.Time `json:"starts_at"`
	EndsAt             *time.Time `json:"ends_at"`
	MeetingProvider    string     `json:"meeting_provider"`
	MeetingURL         string     `json:"meeting_url"`
	DestinationEventID string     `json:"destination_event_id"`
	Synced             bool       `json:"synced"`
	Cancelled          bool       `json:"cancelled"`
	RecordingURL       string     `json:"recording_url"`
	HasTranscript      bool       `json:"has_transcript"`
	HasSummary         bool       `json:"has_summary"`
	RecordedAt         *time.Time `json:"recorded_at"`
	SharedWith         []string   `json:"shared_with"` // other attendees' emails for this same meeting
}

// ListMappings returns an employee's meeting mappings, newest first, for the
// admin detail view.
func (r *Repo) ListMappings(ctx context.Context, org db.OrgID, employeeID int64) ([]MappingView, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT m.source_event_id, m.calendar_uid, coalesce(m.title,''), m.starts_at, m.ends_at,
		       coalesce(m.meeting_provider,''), coalesce(m.meeting_url,''),
		       coalesce(m.destination_event_id,''), (m.destination_event_id IS NOT NULL),
		       (m.cancelled_at IS NOT NULL), coalesce(m.fathom_recording_url,''),
		       m.fathom_has_transcript, m.fathom_has_summary, m.fathom_recorded_at,
		       coalesce((SELECT array_agg(e2.email ORDER BY e2.email)
		                 FROM event_mappings m2 JOIN employees e2 ON e2.id = m2.employee_id
		                 WHERE m2.source_event_id = m.source_event_id
		                   AND m2.employee_id <> m.employee_id
		                   AND m2.cancelled_at IS NULL
		                   AND e2.organization_id = $2), '{}') AS shared_with
		FROM event_mappings m
		WHERE m.employee_id = $1
		ORDER BY m.starts_at DESC NULLS LAST
		LIMIT 200`, employeeID, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MappingView{}
	for rows.Next() {
		var m MappingView
		if err := rows.Scan(&m.SourceEventID, &m.CalendarUID, &m.Title, &m.StartsAt, &m.EndsAt,
			&m.MeetingProvider, &m.MeetingURL, &m.DestinationEventID, &m.Synced, &m.Cancelled,
			&m.RecordingURL, &m.HasTranscript, &m.HasSummary, &m.RecordedAt, &m.SharedWith); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SaveRecording links a Fathom recording back to the matching meeting(s) by
// meeting_url (spec §42-43). Returns the number of mappings updated.
// LinkRecordingIfAbsent links a Fathom recording to a synced meeting only when
// no recording is linked yet. It is the idempotent path used by the polling
// backup (spec §42): re-polling never overwrites a webhook-delivered link or
// churns updated_at. Returns rows affected (1 = newly linked, 0 = already
// linked or no matching synced meeting).
func (r *Repo) LinkRecordingIfAbsent(ctx context.Context, meetingURL, recordingID, recordingURL string, hasTranscript, hasSummary bool) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE event_mappings
		SET fathom_recording_id = $2, fathom_recording_url = $3,
		    fathom_has_transcript = $4, fathom_has_summary = $5,
		    fathom_recorded_at = now(), updated_at = now()
		WHERE meeting_url = $1 AND fathom_recording_id IS NULL`,
		meetingURL, recordingID, recordingURL, hasTranscript, hasSummary)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *Repo) SaveRecording(ctx context.Context, meetingURL, recordingID, recordingURL string, hasTranscript, hasSummary bool) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE event_mappings
		SET fathom_recording_id = $2, fathom_recording_url = $3,
		    fathom_has_transcript = $4, fathom_has_summary = $5,
		    fathom_recorded_at = now(), updated_at = now()
		WHERE meeting_url = $1`, meetingURL, recordingID, recordingURL, hasTranscript, hasSummary)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// SharedDestinationEventID returns a destination event already synced for the
// same source event by another attendee, so co-attendees reuse one event
// instead of duplicating it on the Fathom calendar (spec §42).
func (r *Repo) SharedDestinationEventID(ctx context.Context, sourceEventID string) (string, bool, error) {
	var id string
	err := r.pool.QueryRow(ctx, `
		SELECT destination_event_id FROM event_mappings
		WHERE source_event_id = $1 AND destination_event_id IS NOT NULL AND cancelled_at IS NULL
		LIMIT 1`, sourceEventID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// SharedDestinationForMeeting finds an existing destination event for the same
// meeting, so co-attendees collapse onto one Fathom event (spec §42). It matches
// first on the exact source_event_id (identical invite id); then — because
// different attendees' calendar copies of the same externally-organized invite
// can carry different ids (e.g. a Zoho id vs an Exchange GlobalObjectId) — falls
// back to an exact normalized-title + start-time match within the org.
func (r *Repo) SharedDestinationForMeeting(ctx context.Context, org db.OrgID, sourceEventID, title string, start *time.Time) (string, bool, error) {
	if id, ok, err := r.SharedDestinationEventID(ctx, sourceEventID); err != nil || ok {
		return id, ok, err
	}
	want := normalizeTitle(title)
	if start == nil || want == "" {
		return "", false, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT m.destination_event_id, coalesce(m.title,'')
		FROM event_mappings m JOIN employees e ON e.id = m.employee_id
		WHERE e.organization_id = $1 AND m.cancelled_at IS NULL
		  AND m.destination_event_id IS NOT NULL AND m.starts_at = $2`, org, *start)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, evTitle string
		if err := rows.Scan(&id, &evTitle); err != nil {
			return "", false, err
		}
		if normalizeTitle(evTitle) == want {
			return id, true, nil
		}
	}
	return "", false, rows.Err()
}

// DestinationShared reports whether another active mapping still references the
// same destination event (a co-attendee), so cancelling one attendee must not
// delete the shared event.
func (r *Repo) DestinationShared(ctx context.Context, destEventID string, excludeMappingID int64) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM event_mappings
			WHERE destination_event_id = $1 AND id <> $2 AND cancelled_at IS NULL)`,
		destEventID, excludeMappingID).Scan(&exists)
	return exists, err
}

// SourceAttendees maps each destination event id to the source employees' emails
// (active mappings), for attribution in the calendar view (spec §42).
func (r *Repo) SourceAttendees(ctx context.Context, org db.OrgID, destEventIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(destEventIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT m.destination_event_id, e.email
		FROM event_mappings m JOIN employees e ON e.id = m.employee_id
		WHERE m.destination_event_id = ANY($1) AND m.cancelled_at IS NULL
		  AND e.organization_id = $2
		ORDER BY e.email`, destEventIDs, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, email string
		if err := rows.Scan(&id, &email); err != nil {
			return nil, err
		}
		out[id] = append(out[id], email)
	}
	return out, rows.Err()
}

// Attendee is an org employee who had a matched meeting on their calendar.
type Attendee struct {
	Email string
	Name  string
}

// MatchAttendees finds the org's employees whose synced calendar had an event
// corresponding to a recorded meeting, so its transcript/summary can be emailed
// to the people who attended. A candidate mapping is one whose start_at falls
// within ±window of the meeting start; the title is then matched by a normalized
// substring compare (either direction) when both titles are present. Returns
// distinct attendees (org-scoped for the tenant guard).
func (r *Repo) MatchAttendees(ctx context.Context, org db.OrgID, start time.Time, window time.Duration, title string) ([]Attendee, error) {
	// Note: cancelled mappings are intentionally included — recordings are for
	// meetings that already happened, whose mappings the sync engine has usually
	// marked cancelled (source event aged out of the scan). Someone who had the
	// meeting is still a valid recipient regardless of that flag.
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT e.email, coalesce(e.name,''), coalesce(m.title,'')
		FROM event_mappings m JOIN employees e ON e.id = m.employee_id
		WHERE e.organization_id = $1
		  AND m.starts_at IS NOT NULL
		  AND m.starts_at BETWEEN $2 AND $3`,
		org, start.Add(-window), start.Add(window))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	want := normalizeTitle(title)
	seen := map[string]bool{}
	var out []Attendee
	for rows.Next() {
		var email, name, evTitle string
		if err := rows.Scan(&email, &name, &evTitle); err != nil {
			return nil, err
		}
		if want != "" {
			got := normalizeTitle(evTitle)
			if got == "" || !(got == want || strings.Contains(got, want) || strings.Contains(want, got)) {
				continue
			}
		}
		if seen[email] {
			continue
		}
		seen[email] = true
		out = append(out, Attendee{Email: email, Name: name})
	}
	return out, rows.Err()
}

// normalizeTitle lowercases and strips non-alphanumeric characters so meeting
// titles from different systems (Fireflies vs the calendar) compare loosely.
func normalizeTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ActiveDestinationEventIDs returns the set of destination event ids still
// referenced by a non-cancelled mapping in the org. Used by prune-orphans to
// delete destination events no mapping points to any more (e.g. after dedup).
func (r *Repo) ActiveDestinationEventIDs(ctx context.Context, org db.OrgID) (map[string]bool, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT m.destination_event_id
		FROM event_mappings m JOIN employees e ON e.id = m.employee_id
		WHERE e.organization_id = $1 AND m.destination_event_id IS NOT NULL
		  AND m.cancelled_at IS NULL`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
