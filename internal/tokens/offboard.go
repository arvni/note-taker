package tokens

import (
	"context"
	"log"

	"github.com/arvinizadi/fathom/internal/audit"
	"github.com/arvinizadi/fathom/internal/db"
)

// Offboarder tears down all access when an employee becomes inactive in the
// directory (spec §19). It implements directory.Offboarder. The sequence:
//  1. stop calendar sync      — implicit: credential deleted + employee disabled
//  2. cancel queued jobs      — implicit: no job runs for a disabled employee
//  3. revoke OAuth access     — best-effort Zoho refresh-token revoke
//  4. remove stored tokens    — delete the encrypted credential
//  5. mark disconnected       — onboarding status -> disabled
//  6. preserve minimal audit  — audit employee_disabled (no token values)
//  7. stop accessing calendar — no credential remains to use
type Offboarder struct {
	store     *Store
	zohoFor   ZohoRevokerFor
	employees OnboardingSetter
	audit     *audit.Logger
}

func NewOffboarder(store *Store, zohoFor ZohoRevokerFor, employees OnboardingSetter, auditLog *audit.Logger) *Offboarder {
	return &Offboarder{store: store, zohoFor: zohoFor, employees: employees, audit: auditLog}
}

// Offboard executes the offboarding sequence for an employee (spec §19). It is
// idempotent: a missing credential is not an error.
func (o *Offboarder) Offboard(ctx context.Context, org db.OrgID, employeeID int64) error {
	// 3. Best-effort Zoho revocation before deleting the token locally.
	if cred, err := o.store.Get(ctx, employeeID); err == nil {
		if zoho, ferr := o.zohoFor(ctx, org); ferr == nil {
			if err := zoho.Revoke(ctx, cred.RefreshToken); err != nil {
				log.Printf("offboard: zoho revoke (continuing): %v", err)
			}
		}
	} else if err != ErrNotFound {
		return err
	}

	// 4. Remove stored tokens (spec §19).
	if err := o.store.Delete(ctx, employeeID); err != nil {
		return err
	}

	// 5. Mark disconnected.
	if err := o.employees.SetOnboardingStatus(ctx, org, employeeID, "disabled"); err != nil {
		return err
	}

	// 6. Preserve minimal audit info.
	if o.audit != nil {
		_ = o.audit.Log(ctx, audit.Entry{
			OrganizationID: int64(org), EmployeeID: employeeID, Action: audit.EmployeeDisabled,
		})
	}
	return nil
}
