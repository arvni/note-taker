package onboarding

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/arvinizadi/fathom/internal/db"
)

// seedEmployee creates a throwaway org + employee and returns their ids.
func seedEmployee(t *testing.T, pool *db.Pool) (empID int64, orgID db.OrgID) {
	t.Helper()
	ctx := context.Background()
	var oid int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO organizations (name) VALUES ('itest-org') RETURNING id`).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	var eid int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO employees (organization_id, email, status, onboarding_status)
		VALUES ($1, $2, 'active', 'not_invited') RETURNING id`,
		oid, "itest@company.com").Scan(&eid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM onboarding_tokens WHERE employee_id = $1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM employees WHERE id = $1`, eid)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, oid)
	})
	return eid, db.OrgID(oid)
}

func testPool(t *testing.T) *db.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping DB integration test")
	}
	pool, err := db.Connect(context.Background(), url)
	if err != nil {
		t.Skipf("cannot connect to DB: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestOnboardingToken_SingleUseAndExpiry(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	ctx := context.Background()

	empID, orgID := seedEmployee(t, pool)

	raw, hash, err := GenerateToken(48)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, empID, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Peek does not consume.
	if r, err := repo.Peek(ctx, raw); err != nil || r.EmployeeID != empID || r.OrgID != orgID {
		t.Fatalf("Peek failed: r=%+v err=%v", r, err)
	}

	// First consume succeeds and resolves org.
	r, err := repo.Consume(ctx, raw)
	if err != nil {
		t.Fatalf("first Consume: %v", err)
	}
	if r.EmployeeID != empID || r.OrgID != orgID {
		t.Fatalf("Consume resolved wrong: %+v", r)
	}

	// Second consume fails — single use (spec §7).
	if _, err := repo.Consume(ctx, raw); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("second Consume: got %v, want ErrInvalidToken", err)
	}
	// Peek after consume also fails.
	if _, err := repo.Peek(ctx, raw); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Peek after consume: got %v, want ErrInvalidToken", err)
	}
}

func TestOnboardingToken_ExpiredIsRejected(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	ctx := context.Background()
	empID, _ := seedEmployee(t, pool)

	raw, hash, _ := GenerateToken(48)
	if err := repo.Create(ctx, empID, hash, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Consume(ctx, raw); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired token: got %v, want ErrInvalidToken", err)
	}
}

func TestOnboardingToken_UnknownIsRejected(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	if _, err := repo.Consume(context.Background(), "totally-unknown-token"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("unknown token: got %v, want ErrInvalidToken", err)
	}
}
