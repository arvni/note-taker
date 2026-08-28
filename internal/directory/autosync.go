package directory

import (
	"context"
	"log"

	"github.com/arvinizadi/fathom/internal/db"
)

// TokenFunc yields an org-level access token for the Directory API. Typically it
// exchanges a stored refresh token (from an admin one-time authorization or a
// Zoho Self Client) for a fresh access token (spec §3, §55). Directory access is
// separate from per-employee calendar authorization (spec §55).
type TokenFunc func(ctx context.Context) (string, error)

// AutoSync pulls the org's users from Zoho Directory and reconciles them into
// employee records (spec §20). It is endpoint-agnostic: the Directory users URL
// and the token source are supplied by configuration, since the exact Zoho
// Directory endpoint/scope is org- and data-center-specific.
type AutoSync struct {
	source     *ZohoClient
	reconciler *Reconciler
	token      TokenFunc
	org        db.OrgID
}

func NewAutoSync(source *ZohoClient, reconciler *Reconciler, token TokenFunc, org db.OrgID) *AutoSync {
	return &AutoSync{source: source, reconciler: reconciler, token: token, org: org}
}

// SyncOnce performs one directory reconciliation pass for the org (spec §20).
func (a *AutoSync) SyncOnce(ctx context.Context) (Report, error) {
	tok, err := a.token(ctx)
	if err != nil {
		return Report{}, err
	}
	users, err := a.source.ListUsers(ctx, tok)
	if err != nil {
		return Report{}, err
	}
	rep, err := a.reconciler.Reconcile(ctx, a.org, users)
	if err != nil {
		return rep, err
	}
	log.Printf("directory auto-sync org=%d: users=%d created=%d updated=%d disabled=%d reactivated=%d conflicts=%d",
		a.org, len(users), rep.Created, rep.Updated, rep.Disabled, rep.Reactivated, len(rep.Conflicts))
	return rep, nil
}
