# Access Control

## Roles (spec §41)

`internal/rbac` defines four roles with a server-side permission matrix:

- **org_admin** — manage org, employees, calendars, policies; view status; revoke.
- **calendar_manager** — manage sync rules; view status; manage calendars.
- **employee** — view own status; connect/reconnect; revoke self.
- **auditor** — read-only org status.

All checks are server-side (`Principal.Can`). No permission is inferred client-side.

## Tenant isolation (spec §48)

Every tenant-scoped query filters by `organization_id`. `internal/db/tenant.go`
provides a runtime guard (`CheckOrgScope`) and a `TenantScope` wrapper; a static
test (`TestNoRawTenantQueries`) fails the build if any code queries a tenant table
without `organization_id`. Cross-org access is a tested failure.

## Sessions (spec §38)

HMAC-signed cookies with `HttpOnly`, `Secure` (prod), `SameSite=Lax`, short TTL,
rotation via re-issue, and a per-session CSRF token required on unsafe methods.

## Authentication (spec §39-40)

- **Admins**: OIDC/SSO (Zoho Directory / Entra / Google Workspace) via the
  `IdentityProvider` seam; MFA enforced by the IdP. No local password database.
- **Employees**: Zoho OAuth only; no separate employee password.
