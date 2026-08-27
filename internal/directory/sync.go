package directory

import (
	"context"
	"log"
	"time"

	"github.com/arvinizadi/fathom/internal/db"
)

// SyncFunc performs one directory reconciliation pass for all organizations.
// The concrete implementation (which directory source and per-org access token
// to use) depends on the org-level authorization decision (spec §54-55) and is
// wired once that is resolved; until then the worker runs a no-op logger.
type SyncFunc func(ctx context.Context) error

// RunPeriodic invokes fn immediately and then every interval until ctx is
// cancelled (spec §20: default hourly). Errors are logged, not fatal, so a
// transient directory outage does not kill the worker.
func RunPeriodic(ctx context.Context, interval time.Duration, fn SyncFunc) {
	run := func() {
		if err := fn(ctx); err != nil {
			log.Printf("directory sync: %v", err)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Print("directory sync: stopping")
			return
		case <-ticker.C:
			run()
		}
	}
}

// ReconcileOrg fetches users from a source for one org and reconciles them.
// Sources that need an access token receive it via the tokenFn callback.
type ReconcileOrg struct {
	Org        db.OrgID
	Reconciler *Reconciler
}

// FromUsers reconciles an already-fetched user snapshot (e.g. a CSV upload) for
// this org and logs a summary (spec §20).
func (r ReconcileOrg) FromUsers(ctx context.Context, users []User) (Report, error) {
	rep, err := r.Reconciler.Reconcile(ctx, r.Org, users)
	if err != nil {
		return rep, err
	}
	log.Printf("directory sync org=%d: created=%d updated=%d disabled=%d reactivated=%d conflicts=%d",
		r.Org, rep.Created, rep.Updated, rep.Disabled, rep.Reactivated, len(rep.Conflicts))
	return rep, nil
}
