// Package retention enforces configurable data-retention policies (spec §50).
// The default periods are examples, NOT legal advice — they must be reviewed and
// set per the company's policy (spec §50-52).
package retention

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/arvinizadi/fathom/internal/db"
)

// Policy holds the configurable retention windows (spec §50).
type Policy struct {
	AuditLog      time.Duration // e.g. 365d
	EventMappings time.Duration // cancelled sync mappings, e.g. 365d
	ExpiredTokens time.Duration // housekeeping for used/expired onboarding+state
}

// DefaultPolicy returns example values (spec §50). Review before production.
func DefaultPolicy() Policy {
	return Policy{
		AuditLog:      365 * 24 * time.Hour,
		EventMappings: 365 * 24 * time.Hour,
		ExpiredTokens: 7 * 24 * time.Hour,
	}
}

// Purger deletes data past its retention window (spec §50).
type Purger struct {
	pool   *db.Pool
	policy Policy
}

func NewPurger(pool *db.Pool, policy Policy) *Purger { return &Purger{pool: pool, policy: policy} }

// Result reports what a purge removed.
type Result struct {
	AuditLogs     int64
	EventMappings int64
	OnboardingTok int64
	OAuthStates   int64
}

// Purge runs all retention deletions and returns counts (spec §50).
func (p *Purger) Purge(ctx context.Context) (Result, error) {
	var res Result
	var err error

	// tenant-scope-exempt: retention purge is global maintenance across all orgs (spec §50).
	if res.AuditLogs, err = p.exec(ctx,
		`DELETE FROM audit_log WHERE created_at < now() - $1::interval`, p.policy.AuditLog); err != nil {
		return res, err
	}
	if res.EventMappings, err = p.exec(ctx,
		`DELETE FROM event_mappings WHERE cancelled_at IS NOT NULL AND cancelled_at < now() - $1::interval`,
		p.policy.EventMappings); err != nil {
		return res, err
	}
	if res.OnboardingTok, err = p.exec(ctx,
		`DELETE FROM onboarding_tokens WHERE (used_at IS NOT NULL OR expires_at < now()) AND created_at < now() - $1::interval`,
		p.policy.ExpiredTokens); err != nil {
		return res, err
	}
	if res.OAuthStates, err = p.exec(ctx,
		`DELETE FROM oauth_states WHERE (used_at IS NOT NULL OR expires_at < now()) AND created_at < now() - $1::interval`,
		p.policy.ExpiredTokens); err != nil {
		return res, err
	}

	log.Printf("retention purge: audit=%d mappings=%d onboarding_tokens=%d oauth_states=%d",
		res.AuditLogs, res.EventMappings, res.OnboardingTok, res.OAuthStates)
	return res, nil
}

func (p *Purger) exec(ctx context.Context, q string, d time.Duration) (int64, error) {
	tag, err := p.pool.Exec(ctx, q, fmt.Sprintf("%d seconds", int64(d.Seconds())))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
