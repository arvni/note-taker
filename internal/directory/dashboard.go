package directory

import (
	"context"

	"github.com/arvinizadi/fathom/internal/db"
)

// OrgStats summarizes onboarding status for the admin dashboard (spec §46).
type OrgStats struct {
	Total     int
	Connected int
	Pending   int
	Revoked   int
	Failed    int
}

// Stats returns onboarding counts for an organization (spec §46).
func (r *Repo) Stats(ctx context.Context, org db.OrgID) (OrgStats, error) {
	t := r.pool.Tenant(org)
	rows, err := t.Query(ctx, `
		SELECT onboarding_status, count(*)
		FROM employees WHERE organization_id = $1
		GROUP BY onboarding_status`, org)
	if err != nil {
		return OrgStats{}, err
	}
	defer rows.Close()
	var s OrgStats
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return s, err
		}
		s.Total += n
		switch status {
		case "authorized":
			s.Connected += n
		case "invited", "opened", "authorization_started", "not_invited":
			s.Pending += n
		case "revoked":
			s.Revoked += n
		case "authorization_failed":
			s.Failed += n
		}
	}
	return s, rows.Err()
}

// EmployeeRow is a per-employee row for the admin dashboard (spec §46).
type EmployeeRow struct {
	ID               int64
	Email            string
	Name             string
	Department       string
	OnboardingStatus string
	CalendarCount    int
	MeetingsSynced   int
}

// EmployeeRows returns per-employee dashboard rows for an org (spec §46).
func (r *Repo) EmployeeRows(ctx context.Context, org db.OrgID) ([]EmployeeRow, error) {
	t := r.pool.Tenant(org)
	rows, err := t.Query(ctx, `
		SELECT e.id, e.email, coalesce(e.name,''), coalesce(e.department,''), e.onboarding_status,
		       (SELECT count(*) FROM calendars c WHERE c.employee_id = e.id AND c.enabled),
		       (SELECT count(*) FROM event_mappings m WHERE m.employee_id = e.id AND m.destination_event_id IS NOT NULL AND m.cancelled_at IS NULL)
		FROM employees e
		WHERE e.organization_id = $1
		ORDER BY e.email`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EmployeeRow
	for rows.Next() {
		var er EmployeeRow
		if err := rows.Scan(&er.ID, &er.Email, &er.Name, &er.Department, &er.OnboardingStatus,
			&er.CalendarCount, &er.MeetingsSynced); err != nil {
			return nil, err
		}
		out = append(out, er)
	}
	return out, rows.Err()
}
