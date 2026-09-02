package calendar

import (
	"context"
	"time"

	"github.com/arvinizadi/fathom/internal/db"
)

// StoredCalendar is a discovered calendar row (spec §27-28).
type StoredCalendar struct {
	EmployeeID int64
	UID        string
	Name       string
	Type       string
	Timezone   string
	Owner      string
	Enabled    bool
	IsPersonal bool
}

// Repo persists discovered calendars and minimized event mappings. Calendars and
// event_mappings are employee-scoped; tenant isolation is transitive via the
// employee resolved within an org scope (spec §48).
type Repo struct {
	pool *db.Pool
}

func NewRepo(pool *db.Pool) *Repo { return &Repo{pool: pool} }

// UpsertCalendar stores/updates a discovered calendar. The enabled flag is set
// on first insert (default policy) and preserved on update so admin/employee
// choices are not overwritten by a later discovery (spec §27-28).
func (r *Repo) UpsertCalendar(ctx context.Context, c StoredCalendar) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO calendars (employee_id, calendar_uid, name, calendar_type, timezone, owner, enabled, is_personal)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (employee_id, calendar_uid) DO UPDATE SET
			name = EXCLUDED.name, calendar_type = EXCLUDED.calendar_type,
			timezone = EXCLUDED.timezone, owner = EXCLUDED.owner,
			is_personal = EXCLUDED.is_personal, updated_at = now()`,
		c.EmployeeID, c.UID, c.Name, c.Type, c.Timezone, c.Owner, c.Enabled, c.IsPersonal)
	return err
}

// ListEnabledCalendars returns the calendars currently enabled for monitoring.
func (r *Repo) ListEnabledCalendars(ctx context.Context, employeeID int64) ([]StoredCalendar, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT employee_id, calendar_uid, coalesce(name,''), coalesce(calendar_type,''),
		       coalesce(timezone,''), coalesce(owner,''), enabled, is_personal
		FROM calendars
		WHERE employee_id = $1 AND enabled = true`, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredCalendar
	for rows.Next() {
		var c StoredCalendar
		if err := rows.Scan(&c.EmployeeID, &c.UID, &c.Name, &c.Type, &c.Timezone,
			&c.Owner, &c.Enabled, &c.IsPersonal); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetCalendarEnabled toggles monitoring for a calendar (employee/admin choice,
// spec §28, §31).
func (r *Repo) SetCalendarEnabled(ctx context.Context, employeeID int64, calendarUID string, enabled bool) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE calendars SET enabled = $1, updated_at = now()
		WHERE employee_id = $2 AND calendar_uid = $3`, enabled, employeeID, calendarUID)
	return err
}

// MinimizedEvent is the only event data we persist (spec §32): identifiers,
// timing, and meeting info — never full description, attachments, or attendees.
type MinimizedEvent struct {
	EmployeeID      int64
	CalendarUID     string
	SourceEventID   string
	Title           string
	StartsAt        *time.Time
	EndsAt          *time.Time
	MeetingProvider string
	MeetingURL      string
	Description     string
	Location        string
	SourceUpdatedAt *time.Time
}

// UpsertMapping stores a minimized qualifying event, idempotent on
// (employee_id, source_event_id) so re-scans never duplicate (spec §32, §48).
func (r *Repo) UpsertMapping(ctx context.Context, e MinimizedEvent) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO event_mappings
			(employee_id, calendar_uid, source_event_id, title, starts_at, ends_at,
			 meeting_provider, meeting_url, description, location, source_updated_at, last_seen_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, now())
		ON CONFLICT (employee_id, source_event_id) DO UPDATE SET
			calendar_uid = EXCLUDED.calendar_uid, title = EXCLUDED.title,
			starts_at = EXCLUDED.starts_at, ends_at = EXCLUDED.ends_at,
			meeting_provider = EXCLUDED.meeting_provider, meeting_url = EXCLUDED.meeting_url,
			description = EXCLUDED.description, location = EXCLUDED.location,
			source_updated_at = EXCLUDED.source_updated_at,
			last_seen_at = now(), cancelled_at = NULL, updated_at = now()`,
		e.EmployeeID, e.CalendarUID, e.SourceEventID, e.Title, e.StartsAt, e.EndsAt,
		e.MeetingProvider, e.MeetingURL, e.Description, e.Location, e.SourceUpdatedAt)
	return err
}

// ListAllCalendars returns every discovered calendar for an employee with its
// enabled flag, for the manage-calendars view (spec §47).
func (r *Repo) ListAllCalendars(ctx context.Context, employeeID int64) ([]StoredCalendar, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT employee_id, calendar_uid, coalesce(name,''), coalesce(calendar_type,''),
		       coalesce(timezone,''), coalesce(owner,''), enabled, is_personal
		FROM calendars WHERE employee_id = $1 ORDER BY name`, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredCalendar
	for rows.Next() {
		var c StoredCalendar
		if err := rows.Scan(&c.EmployeeID, &c.UID, &c.Name, &c.Type, &c.Timezone,
			&c.Owner, &c.Enabled, &c.IsPersonal); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
