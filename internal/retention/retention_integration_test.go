package retention

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/arvinizadi/fathom/internal/db"
)

func TestPurge(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	pool, err := db.Connect(context.Background(), url)
	if err != nil {
		t.Skipf("db: %v", err)
	}
	t.Cleanup(pool.Close)
	ctx := context.Background()

	var oid int64
	_ = pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ('ret-itest') RETURNING id`).Scan(&oid)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE organization_id=$1`, oid)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, oid)
	})

	// One old audit entry (400 days) and one recent.
	_, _ = pool.Exec(ctx, `INSERT INTO audit_log (organization_id, action, created_at)
		VALUES ($1,'old', now() - interval '400 days')`, oid)
	_, _ = pool.Exec(ctx, `INSERT INTO audit_log (organization_id, action, created_at)
		VALUES ($1,'recent', now())`, oid)

	p := NewPurger(pool, Policy{AuditLog: 365 * 24 * time.Hour, EventMappings: 365 * 24 * time.Hour, ExpiredTokens: 7 * 24 * time.Hour})
	res, err := p.Purge(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.AuditLogs < 1 {
		t.Fatalf("expected >=1 audit log purged, got %d", res.AuditLogs)
	}

	// The recent one survives; the old one is gone.
	var recent, old int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE organization_id=$1 AND action='recent'`, oid).Scan(&recent)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE organization_id=$1 AND action='old'`, oid).Scan(&old)
	if recent != 1 {
		t.Fatalf("recent audit wrongly purged: %d", recent)
	}
	if old != 0 {
		t.Fatalf("old audit not purged: %d", old)
	}
}
