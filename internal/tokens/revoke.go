package tokens

import (
	"context"
	"log"

	"github.com/arvinizadi/fathom/internal/audit"
	"github.com/arvinizadi/fathom/internal/db"
)

// ZohoRevoker revokes a refresh token at Zoho (implemented by *oauth.Client).
type ZohoRevoker interface {
	Revoke(ctx context.Context, refreshToken string) error
}

// OnboardingSetter updates an employee's onboarding status (implemented by
// *directory.Repo).
type OnboardingSetter interface {
	SetOnboardingStatus(ctx context.Context, org db.OrgID, id int64, status string) error
}

// Revoker performs employee revocation (spec §17): mark the credential revoked,
// revoke the Zoho refresh token, flip onboarding status, and audit. Stopping
// sync jobs happens implicitly — AccessToken returns ErrRevoked once status is
// no longer active. Token deletion per retention policy is handled in Phase 12.
type Revoker struct {
	store     *Store
	zoho      ZohoRevoker
	employees OnboardingSetter
	audit     *audit.Logger
}

func NewRevoker(store *Store, zoho ZohoRevoker, employees OnboardingSetter, auditLog *audit.Logger) *Revoker {
	return &Revoker{store: store, zoho: zoho, employees: employees, audit: auditLog}
}

// Revoke revokes access for an employee (spec §17).
func (r *Revoker) Revoke(ctx context.Context, org db.OrgID, employeeID int64) error {
	// Best-effort Zoho revocation using the stored refresh token.
	if cred, err := r.store.Get(ctx, employeeID); err == nil {
		if err := r.zoho.Revoke(ctx, cred.RefreshToken); err != nil {
			log.Printf("revoke: zoho revoke (continuing to mark local revoked): %v", err)
		}
	} else if err != ErrNotFound {
		return err
	}

	if err := r.store.SetStatus(ctx, employeeID, StatusRevoked); err != nil {
		return err
	}
	if err := r.employees.SetOnboardingStatus(ctx, org, employeeID, "revoked"); err != nil {
		return err
	}
	if r.audit != nil {
		_ = r.audit.Log(ctx, audit.Entry{
			OrganizationID: int64(org), EmployeeID: employeeID, Action: audit.EmployeeRevoked,
		})
	}
	return nil
}
