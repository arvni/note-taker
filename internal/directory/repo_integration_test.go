package directory

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/arvinizadi/fathom/internal/db"
)

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

func newOrg(t *testing.T, pool *db.Pool, name string) db.OrgID {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO organizations (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM employees WHERE organization_id = $1`, id)
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM organizations WHERE id = $1`, id)
	})
	return db.OrgID(id)
}

func TestRepo_TenantIsolation(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	ctx := context.Background()

	orgA := newOrg(t, pool, "org-A")
	orgB := newOrg(t, pool, "org-B")

	emp, err := repo.Create(ctx, orgA, User{Email: "shared@company.com", Name: "A One", Status: Active})
	if err != nil {
		t.Fatal(err)
	}

	// Org A sees the employee.
	if got, err := repo.GetByEmail(ctx, orgA, "shared@company.com"); err != nil || got.ID != emp.ID {
		t.Fatalf("orgA GetByEmail failed: got=%v err=%v", got, err)
	}
	// Org B must NOT see org A's employee (spec §48).
	if _, err := repo.GetByEmail(ctx, orgB, "shared@company.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant isolation broken: orgB read orgA employee (err=%v)", err)
	}
	// Org B GetByID on org A's id must also be isolated.
	if _, err := repo.GetByID(ctx, orgB, emp.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant isolation broken on GetByID (err=%v)", err)
	}
}

func TestRepo_UpdateEmailInPlace(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	ctx := context.Background()
	org := newOrg(t, pool, "org-email")

	emp, err := repo.Create(ctx, org, User{ZohoUserID: "z-1", Email: "old@company.com", Status: Active})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateEmail(ctx, org, emp.ID, "new@company.com"); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, org, emp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "new@company.com" || got.ZohoUserID != "z-1" {
		t.Fatalf("update email in place failed: %+v", got)
	}
}
