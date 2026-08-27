package oauth

import (
	"context"
	"errors"
	"time"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/jackc/pgx/v5"
)

// ErrInvalidState is returned for unknown, expired, used, or mismatched state
// (spec §9). Generic on purpose to avoid leaking which condition failed.
var ErrInvalidState = errors.New("oauth: invalid, expired, or used state")

// StateRepo persists OAuth CSRF state (hash only, single-use) (spec §9).
type StateRepo struct {
	pool *db.Pool
}

func NewStateRepo(pool *db.Pool) *StateRepo { return &StateRepo{pool: pool} }

// Create stores a new state hash bound to an employee (spec §9). No employee ID
// or email is embedded in the state value itself.
func (r *StateRepo) Create(ctx context.Context, employeeID int64, stateHash []byte, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO oauth_states (employee_id, provider, state_hash, expires_at)
		VALUES ($1, 'zoho', $2, $3)`, employeeID, stateHash, expiresAt)
	return err
}

// Consumed identifies the employee/org a validated state belonged to.
type Consumed struct {
	EmployeeID int64
	OrgID      db.OrgID
}

// Consume atomically validates and single-uses a presented state value, then
// resolves the owning employee/org (spec §9). The callback must verify: exists,
// hash matches, not expired, not used, provider==zoho, valid employee — all of
// which this single statement enforces before returning.
func (r *StateRepo) Consume(ctx context.Context, rawState string) (*Consumed, error) {
	hash := HashState(rawState)

	var employeeID int64
	err := r.pool.QueryRow(ctx, `
		UPDATE oauth_states
		SET used_at = now()
		WHERE state_hash = $1 AND provider = 'zoho'
		  AND used_at IS NULL AND expires_at > now()
		RETURNING employee_id`, hash).Scan(&employeeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidState
	}
	if err != nil {
		return nil, err
	}

	var orgID db.OrgID
	// tenant-scope-exempt: state consumption resolves the employee's org (spec §9).
	if err := r.pool.QueryRow(ctx,
		`SELECT organization_id FROM employees WHERE id = $1`, employeeID).Scan(&orgID); err != nil {
		return nil, err
	}
	return &Consumed{EmployeeID: employeeID, OrgID: orgID}, nil
}
