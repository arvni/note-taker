package directory

import (
	"context"
	"errors"
	"time"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/jackc/pgx/v5"
)

// Employee is a stored employee record (spec §34).
type Employee struct {
	ID               int64
	OrgID            db.OrgID
	ZohoUserID       string
	Email            string
	Name             string
	Department       string
	Status           Status
	OnboardingStatus string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ErrNotFound is returned when no employee matches.
var ErrNotFound = errors.New("directory: employee not found")

// Repo is a tenant-scoped employee store. All queries filter by organization_id
// via db.TenantScope (spec §48).
type Repo struct {
	pool *db.Pool
}

func NewRepo(pool *db.Pool) *Repo { return &Repo{pool: pool} }

// GetByEmail returns the employee with the given email within the org.
func (r *Repo) GetByEmail(ctx context.Context, org db.OrgID, email string) (*Employee, error) {
	t := r.pool.Tenant(org)
	row := t.QueryRow(ctx, `
		SELECT id, organization_id, coalesce(zoho_user_id,''), email, coalesce(name,''),
		       coalesce(department,''), status, onboarding_status, created_at, updated_at
		FROM employees
		WHERE organization_id = $1 AND email = $2`, org, email)
	return scanEmployee(row)
}

// ListByOrg returns all employees in the organization.
func (r *Repo) ListByOrg(ctx context.Context, org db.OrgID) ([]Employee, error) {
	t := r.pool.Tenant(org)
	rows, err := t.Query(ctx, `
		SELECT id, organization_id, coalesce(zoho_user_id,''), email, coalesce(name,''),
		       coalesce(department,''), status, onboarding_status, created_at, updated_at
		FROM employees
		WHERE organization_id = $1
		ORDER BY email`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Employee
	for rows.Next() {
		e, err := scanEmployee(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// Create inserts a new employee discovered from the directory and returns it.
func (r *Repo) Create(ctx context.Context, org db.OrgID, u User) (*Employee, error) {
	t := r.pool.Tenant(org)
	var zoho any
	if u.ZohoUserID != "" {
		zoho = u.ZohoUserID
	}
	row := t.QueryRow(ctx, `
		INSERT INTO employees (organization_id, zoho_user_id, email, name, department, status, onboarding_status)
		VALUES ($1, $2, $3, $4, $5, $6, 'not_invited')
		RETURNING id, organization_id, coalesce(zoho_user_id,''), email, coalesce(name,''),
		          coalesce(department,''), status, onboarding_status, created_at, updated_at`,
		org, zoho, u.Email, u.Name, u.Department, string(u.Status))
	return scanEmployee(row)
}

// UpdateProfile updates mutable directory fields (name, department, status,
// zoho_user_id) but never rebinds email or reassigns the credential (spec §20).
func (r *Repo) UpdateProfile(ctx context.Context, org db.OrgID, id int64, name, department string, status Status) error {
	t := r.pool.Tenant(org)
	_, err := t.Exec(ctx, `
		UPDATE employees
		SET name = $1, department = $2, status = $3, updated_at = now()
		WHERE organization_id = $4 AND id = $5`,
		name, department, string(status), org, id)
	return err
}

// SetStatus flips an employee's active/inactive status (spec §20 offboarding).
func (r *Repo) SetStatus(ctx context.Context, org db.OrgID, id int64, status Status) error {
	t := r.pool.Tenant(org)
	_, err := t.Exec(ctx, `
		UPDATE employees SET status = $1, updated_at = now()
		WHERE organization_id = $2 AND id = $3`, string(status), org, id)
	return err
}

func scanEmployee(row pgx.Row) (*Employee, error) {
	var e Employee
	err := row.Scan(&e.ID, &e.OrgID, &e.ZohoUserID, &e.Email, &e.Name,
		&e.Department, &e.Status, &e.OnboardingStatus, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// UpdateEmail changes an employee's email. This is only ever called when the
// employee was matched by their authoritative zoho_user_id (spec §20), so the
// OAuth credential — bound to the Zoho account id, not the email — is never
// reassigned to a different person.
func (r *Repo) UpdateEmail(ctx context.Context, org db.OrgID, id int64, email string) error {
	t := r.pool.Tenant(org)
	_, err := t.Exec(ctx, `
		UPDATE employees SET email = $1, updated_at = now()
		WHERE organization_id = $2 AND id = $3`, email, org, id)
	return err
}

// UpdateNameEmail edits an employee's display name and email from the admin UI.
// The (organization_id, email) unique constraint surfaces a duplicate email as
// an error the handler maps to a 409.
func (r *Repo) UpdateNameEmail(ctx context.Context, org db.OrgID, id int64, name, email string) error {
	t := r.pool.Tenant(org)
	_, err := t.Exec(ctx, `
		UPDATE employees SET name = $1, email = $2, updated_at = now()
		WHERE organization_id = $3 AND id = $4`, name, email, org, id)
	return err
}

// LastInvitedAt returns when the most recent onboarding invitation was sent to
// the employee (the latest onboarding_tokens.created_at), or nil if never
// invited. Used by the admin UI to show "last invited" (spec §44).
func (r *Repo) LastInvitedAt(ctx context.Context, org db.OrgID, id int64) (*time.Time, error) {
	var t *time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT max(created_at) FROM onboarding_tokens WHERE employee_id = $1`, id).Scan(&t)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// GetByID returns the employee by id within the org.
func (r *Repo) GetByID(ctx context.Context, org db.OrgID, id int64) (*Employee, error) {
	t := r.pool.Tenant(org)
	row := t.QueryRow(ctx, `
		SELECT id, organization_id, coalesce(zoho_user_id,''), email, coalesce(name,''),
		       coalesce(department,''), status, onboarding_status, created_at, updated_at
		FROM employees
		WHERE organization_id = $1 AND id = $2`, org, id)
	return scanEmployee(row)
}

// SetOnboardingStatus updates the employee's onboarding lifecycle state (spec §4).
func (r *Repo) SetOnboardingStatus(ctx context.Context, org db.OrgID, id int64, status string) error {
	t := r.pool.Tenant(org)
	_, err := t.Exec(ctx, `
		UPDATE employees SET onboarding_status = $1, updated_at = now()
		WHERE organization_id = $2 AND id = $3`, status, org, id)
	return err
}

// SetZohoUserID binds an employee to their authoritative Zoho account id after
// OAuth identity verification (spec §25-26). It is only set once establishing
// the binding; callers must verify a pre-existing id matches before rebinding.
func (r *Repo) SetZohoUserID(ctx context.Context, org db.OrgID, id int64, zohoUserID string) error {
	t := r.pool.Tenant(org)
	_, err := t.Exec(ctx, `
		UPDATE employees SET zoho_user_id = $1, updated_at = now()
		WHERE organization_id = $2 AND id = $3`, zohoUserID, org, id)
	return err
}

// RecordingSettings returns the recording preference inputs for an employee: the
// employee's own preference plus the org default and whether org policy overrides
// the employee (spec §31).
func (r *Repo) RecordingSettings(ctx context.Context, org db.OrgID, employeeID int64) (empEnabled, orgDefault, orgOverrides bool, err error) {
	t := r.pool.Tenant(org)
	err = t.QueryRow(ctx, `
		SELECT e.recording_enabled, o.recording_default, o.recording_policy_overrides_employee
		FROM employees e
		JOIN organizations o ON o.id = e.organization_id
		WHERE e.organization_id = $1 AND e.id = $2`, org, employeeID).
		Scan(&empEnabled, &orgDefault, &orgOverrides)
	return
}

// AuthorizedEmployee identifies an employee ready for calendar sync.
type AuthorizedEmployee struct {
	Org        db.OrgID
	EmployeeID int64
}

// ListAuthorized enumerates all authorized employees with an active credential
// across every organization, for the background sync loop (spec §42).
func (r *Repo) ListAuthorized(ctx context.Context) ([]AuthorizedEmployee, error) {
	// tenant-scope-exempt: the background sync loop enumerates every org (spec §42).
	rows, err := r.pool.Query(ctx, `
		SELECT e.organization_id, e.id
		FROM employees e
		JOIN oauth_credentials c ON c.employee_id = e.id AND c.provider = 'zoho'
		WHERE e.onboarding_status = 'authorized' AND e.status = 'active' AND c.status = 'active'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuthorizedEmployee
	for rows.Next() {
		var a AuthorizedEmployee
		if err := rows.Scan(&a.Org, &a.EmployeeID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
