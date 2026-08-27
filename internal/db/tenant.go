package db

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// OrgID identifies a tenant (organization). A distinct type prevents passing an
// employee_id where an organization_id is required (spec §48).
type OrgID int64

// directTenantTables carry an organization_id column and MUST be filtered by it
// on every access (spec §48). The remaining employee-scoped tables
// (onboarding_tokens, oauth_states, oauth_credentials, calendars, event_mappings)
// isolate tenants transitively via employee_id, so access to them must go
// through an employee row that was itself resolved within an org scope.
var directTenantTables = map[string]bool{
	"employees": true,
	"audit_log": true,
}

// tableRefRe finds table names introduced by FROM/JOIN/UPDATE/DELETE FROM/INTO.
var tableRefRe = regexp.MustCompile(`(?is)\b(?:from|join|update|into)\s+([a-z_][a-z0-9_]*)`)

// CheckOrgScope returns an error if sql touches a direct tenant-scoped table
// without referencing organization_id (spec §48). It is a defense-in-depth
// guard, not a full SQL parser; the static test in tenant_guard_test.go covers
// the source tree.
func CheckOrgScope(sql string) error {
	lower := strings.ToLower(sql)
	for _, m := range tableRefRe.FindAllStringSubmatch(lower, -1) {
		table := m[1]
		if directTenantTables[table] && !strings.Contains(lower, "organization_id") {
			return fmt.Errorf("db: query touches tenant table %q without organization_id (spec §48): %s", table, sql)
		}
	}
	return nil
}

// TenantScope binds queries to a single organization. Its Exec/Query/QueryRow
// validate org scoping before hitting the database (spec §48).
type TenantScope struct {
	pool *Pool
	org  OrgID
}

// Tenant returns a scope bound to org.
func (p *Pool) Tenant(org OrgID) *TenantScope {
	return &TenantScope{pool: p, org: org}
}

// Org exposes the bound organization id so callers can pass it as a parameter.
func (t *TenantScope) Org() OrgID { return t.org }

// Exec validates org scoping, then executes.
func (t *TenantScope) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if err := CheckOrgScope(sql); err != nil {
		return pgconn.CommandTag{}, err
	}
	return t.pool.Exec(ctx, sql, args...)
}

// Query validates org scoping, then queries.
func (t *TenantScope) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if err := CheckOrgScope(sql); err != nil {
		return nil, err
	}
	return t.pool.Query(ctx, sql, args...)
}

// QueryRow validates org scoping, then queries a single row. A validation error
// surfaces on Scan via the returned errRow.
func (t *TenantScope) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if err := CheckOrgScope(sql); err != nil {
		return errRow{err}
	}
	return t.pool.QueryRow(ctx, sql, args...)
}

// errRow is a pgx.Row that always fails with a fixed error.
type errRow struct{ err error }

func (e errRow) Scan(_ ...any) error { return e.err }
