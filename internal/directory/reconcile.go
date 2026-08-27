package directory

import (
	"context"
	"fmt"

	"github.com/arvinizadi/fathom/internal/db"
)

// Store is the subset of employee persistence the reconciler needs. *Repo
// implements it; tests use an in-memory fake.
type Store interface {
	ListByOrg(ctx context.Context, org db.OrgID) ([]Employee, error)
	Create(ctx context.Context, org db.OrgID, u User) (*Employee, error)
	UpdateProfile(ctx context.Context, org db.OrgID, id int64, name, department string, status Status) error
	UpdateEmail(ctx context.Context, org db.OrgID, id int64, email string) error
	SetStatus(ctx context.Context, org db.OrgID, id int64, status Status) error
}

// Inviter queues an onboarding invitation for a newly discovered employee.
// Implemented in Phase 3; a nil Inviter is treated as a no-op.
type Inviter interface {
	Invite(ctx context.Context, org db.OrgID, employeeID int64) error
}

// Offboarder tears down access when an employee goes inactive (spec §19).
// Implemented in Phase 7; a nil Offboarder is treated as a no-op.
type Offboarder interface {
	Offboard(ctx context.Context, org db.OrgID, employeeID int64) error
}

// Reconciler compares a directory snapshot against stored employees and applies
// creates/updates/disables (spec §20).
type Reconciler struct {
	store      Store
	inviter    Inviter
	offboarder Offboarder
}

func NewReconciler(store Store, inviter Inviter, offboarder Offboarder) *Reconciler {
	return &Reconciler{store: store, inviter: inviter, offboarder: offboarder}
}

// Report summarizes a reconciliation run.
type Report struct {
	Created     int
	Updated     int
	Disabled    int
	Reactivated int
	Conflicts   []string // email collisions across distinct Zoho accounts (spec §20)
}

// Reconcile applies the directory snapshot `users` to the org's employees.
// Matching prefers the authoritative zoho_user_id, falling back to email, so an
// email change on a known Zoho account updates in place rather than creating a
// duplicate or reassigning a credential (spec §20).
func (rc *Reconciler) Reconcile(ctx context.Context, org db.OrgID, users []User) (Report, error) {
	var rep Report

	existing, err := rc.store.ListByOrg(ctx, org)
	if err != nil {
		return rep, err
	}
	byZoho := map[string]*Employee{}
	byEmail := map[string]*Employee{}
	for i := range existing {
		e := &existing[i]
		if e.ZohoUserID != "" {
			byZoho[e.ZohoUserID] = e
		}
		byEmail[e.Email] = e
	}

	for _, raw := range users {
		u := raw.Normalize()
		if !u.Valid() {
			continue
		}

		// Match: zoho_user_id first (authoritative), then email.
		var match *Employee
		if u.ZohoUserID != "" {
			match = byZoho[u.ZohoUserID]
		}
		if match == nil {
			match = byEmail[u.Email]
		}

		if match == nil {
			if err := rc.create(ctx, org, u, &rep); err != nil {
				return rep, err
			}
			continue
		}

		if err := rc.update(ctx, org, match, u, byEmail, &rep); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func (rc *Reconciler) create(ctx context.Context, org db.OrgID, u User, rep *Report) error {
	emp, err := rc.store.Create(ctx, org, u)
	if err != nil {
		return fmt.Errorf("create %s: %w", u.Email, err)
	}
	rep.Created++
	// Only invite active employees; an inactive new record stays dormant.
	if u.Status == Active && rc.inviter != nil {
		if err := rc.inviter.Invite(ctx, org, emp.ID); err != nil {
			return fmt.Errorf("invite %s: %w", u.Email, err)
		}
	}
	return nil
}

func (rc *Reconciler) update(ctx context.Context, org db.OrgID, e *Employee, u User, byEmail map[string]*Employee, rep *Report) error {
	// Handle a careful email change — only legitimate when matched by zoho id.
	if u.Email != e.Email {
		if other, ok := byEmail[u.Email]; ok && other.ID != e.ID {
			// Target email already belongs to a different employee: refuse to
			// merge/reassign (spec §20). Flag for admin review.
			rep.Conflicts = append(rep.Conflicts,
				fmt.Sprintf("email %s already bound to employee %d; not reassigning from %d", u.Email, other.ID, e.ID))
			return nil
		}
		if e.ZohoUserID != "" && u.ZohoUserID == e.ZohoUserID {
			if err := rc.store.UpdateEmail(ctx, org, e.ID, u.Email); err != nil {
				return err
			}
			e.Email = u.Email
			rep.Updated++
		} else {
			// No authoritative binding to justify an email change — skip.
			rep.Conflicts = append(rep.Conflicts,
				fmt.Sprintf("email change for employee %d lacks matching zoho_user_id; skipped", e.ID))
			return nil
		}
	}

	// Reactivation: inactive -> active.
	if u.Status == Active && e.Status == Inactive {
		if err := rc.store.SetStatus(ctx, org, e.ID, Active); err != nil {
			return err
		}
		e.Status = Active
		rep.Reactivated++
	}

	// Deactivation: active -> inactive triggers offboarding (spec §19).
	if u.Status == Inactive && e.Status == Active {
		if err := rc.store.SetStatus(ctx, org, e.ID, Inactive); err != nil {
			return err
		}
		e.Status = Inactive
		rep.Disabled++
		if rc.offboarder != nil {
			if err := rc.offboarder.Offboard(ctx, org, e.ID); err != nil {
				return fmt.Errorf("offboard %d: %w", e.ID, err)
			}
		}
	}

	// Profile drift (name/department) — update in place.
	if u.Name != e.Name || u.Department != e.Department {
		if err := rc.store.UpdateProfile(ctx, org, e.ID, u.Name, u.Department, e.Status); err != nil {
			return err
		}
		rep.Updated++
	}
	return nil
}
