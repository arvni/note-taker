package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migration is a single versioned pair of up/down SQL.
type migration struct {
	version int64
	name    string
	up      string
	down    string
}

// loadMigrations reads and pairs the embedded NNNN_name.up.sql / .down.sql files.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	byVersion := map[int64]*migration{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		var dir string
		switch {
		case strings.HasSuffix(name, ".up.sql"):
			dir = "up"
		case strings.HasSuffix(name, ".down.sql"):
			dir = "down"
		default:
			return nil, fmt.Errorf("db: migration %q must end in .up.sql or .down.sql", name)
		}
		prefix := strings.SplitN(name, "_", 2)
		if len(prefix) != 2 {
			return nil, fmt.Errorf("db: migration %q must be NNNN_name.up|down.sql", name)
		}
		version, err := strconv.ParseInt(prefix[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("db: migration %q has non-numeric version: %w", name, err)
		}
		body, err := fs.ReadFile(migrationFS, "migrations/"+name)
		if err != nil {
			return nil, err
		}
		m := byVersion[version]
		if m == nil {
			m = &migration{version: version, name: strings.TrimSuffix(prefix[1], "."+dir+".sql")}
			byVersion[version] = m
		}
		if dir == "up" {
			m.up = string(body)
		} else {
			m.down = string(body)
		}
	}

	out := make([]migration, 0, len(byVersion))
	for _, m := range byVersion {
		if m.up == "" || m.down == "" {
			return nil, fmt.Errorf("db: migration version %d is missing an up or down file", m.version)
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

func ensureMigrationsTable(ctx context.Context, pool *Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    BIGINT PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	return err
}

func appliedVersions(ctx context.Context, pool *Pool) (map[int64]bool, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := map[int64]bool{}
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// MigrateUp applies all pending migrations in version order, each in its own
// transaction. Returns the number of migrations applied.
func MigrateUp(ctx context.Context, pool *Pool) (int, error) {
	if err := ensureMigrationsTable(ctx, pool); err != nil {
		return 0, err
	}
	migs, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	applied, err := appliedVersions(ctx, pool)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, m := range migs {
		if applied[m.version] {
			continue
		}
		if err := runInTx(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, m.up); err != nil {
				return fmt.Errorf("apply %04d_%s: %w", m.version, m.name, err)
			}
			_, err := tx.Exec(ctx,
				`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, m.version, m.name)
			return err
		}); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// MigrateDown rolls back the most recently applied migration. Returns the
// version rolled back, or 0 if there was nothing to roll back.
func MigrateDown(ctx context.Context, pool *Pool) (int64, error) {
	if err := ensureMigrationsTable(ctx, pool); err != nil {
		return 0, err
	}
	migs, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	applied, err := appliedVersions(ctx, pool)
	if err != nil {
		return 0, err
	}
	// Find the highest applied version.
	var target *migration
	for i := len(migs) - 1; i >= 0; i-- {
		if applied[migs[i].version] {
			target = &migs[i]
			break
		}
	}
	if target == nil {
		return 0, nil
	}
	if err := runInTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, target.down); err != nil {
			return fmt.Errorf("rollback %04d_%s: %w", target.version, target.name, err)
		}
		_, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, target.version)
		return err
	}); err != nil {
		return 0, err
	}
	return target.version, nil
}

func runInTx(ctx context.Context, pool *Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
