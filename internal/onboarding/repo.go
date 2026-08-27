package onboarding

import (
	"context"
	"errors"
	"time"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/jackc/pgx/v5"
)

// ErrInvalidToken is returned when an onboarding token is unknown, expired, or
// already used (spec §7). It is deliberately generic to avoid leaking which.
var ErrInvalidToken = errors.New("onboarding: invalid, expired, or used token")

// Resolved identifies the employee (and their org) a consumed token belonged to.
type Resolved struct {
	EmployeeID int64
	OrgID      db.OrgID
}

// Repo persists onboarding tokens (hash only) and consumes them atomically.
type Repo struct {
	pool *db.Pool
}

func NewRepo(pool *db.Pool) *Repo { return &Repo{pool: pool} }

// Create stores a new onboarding token hash for an employee (spec §6). The raw
// token is never persisted; the caller emails it once.
func (r *Repo) Create(ctx context.Context, employeeID int64, tokenHash []byte, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO onboarding_tokens (employee_id, token_hash, expires_at)
		VALUES ($1, $2, $3)`, employeeID, tokenHash, expiresAt)
	return err
}

// Consume atomically validates and single-uses a presented raw token (spec §7).
// It marks used_at in the same statement that checks validity, so a token can be
// redeemed at most once even under concurrent requests. It then resolves the
// owning employee's organization.
//
// The employees lookup here intentionally has no organization_id filter: the
// onboarding token IS the mechanism that discovers which employee/org this is.
func (r *Repo) Consume(ctx context.Context, rawToken string) (*Resolved, error) {
	hash := HashToken(rawToken)

	var employeeID int64
	err := r.pool.QueryRow(ctx, `
		UPDATE onboarding_tokens
		SET used_at = now()
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
		RETURNING employee_id`, hash).Scan(&employeeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}

	var orgID db.OrgID
	// tenant-scope-exempt: onboarding token is the org-discovery mechanism (spec §7).
	err = r.pool.QueryRow(ctx,
		`SELECT organization_id FROM employees WHERE id = $1`, employeeID).Scan(&orgID)
	if err != nil {
		return nil, err
	}
	return &Resolved{EmployeeID: employeeID, OrgID: orgID}, nil
}

// Peek validates a token WITHOUT consuming it, for rendering the consent page
// before the employee proceeds to Zoho (spec §5, §23). It does not mark used_at.
func (r *Repo) Peek(ctx context.Context, rawToken string) (*Resolved, error) {
	hash := HashToken(rawToken)
	var employeeID int64
	var orgID db.OrgID
	// tenant-scope-exempt: onboarding token is the org-discovery mechanism (spec §7).
	err := r.pool.QueryRow(ctx, `
		SELECT t.employee_id, e.organization_id
		FROM onboarding_tokens t
		JOIN employees e ON e.id = t.employee_id
		WHERE t.token_hash = $1 AND t.used_at IS NULL AND t.expires_at > now()`,
		hash).Scan(&employeeID, &orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	return &Resolved{EmployeeID: employeeID, OrgID: orgID}, nil
}
