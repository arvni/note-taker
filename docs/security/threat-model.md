# Threat Model

Threats and mitigations for the Zoho → Fathom Calendar Bridge (spec §48). Each
mitigation names where it lives in the codebase.

| Threat | Mitigation | Where |
|---|---|---|
| OAuth CSRF | Single-use, hashed, short-lived `state`; verified + invalidated in one statement | `internal/oauth/staterepo.go` |
| Token theft | AES-256-GCM encryption at rest; key outside Postgres; never logged; never sent to browser | `internal/crypto`, `internal/tokens/store.go`, `internal/audit` |
| Employee identity substitution | Zoho identity fetched from official endpoint and verified against the employee's Zoho account id before any credential is stored | `internal/oauth/identity.go`, `internal/httpx/oauth.go` |
| Onboarding-link theft | 48-byte token, hash-only storage, single-use, 7-day expiry | `internal/onboarding` |
| Duplicate meetings | `event_mappings` unique `(employee_id, source_event_id)` + per-row destination tracking | `internal/db/migrations`, `internal/calendar/syncrepo.go` |
| Privilege escalation | RBAC with server-side checks; every tenant query filtered by `organization_id`; static + runtime guard | `internal/rbac`, `internal/db/tenant.go` |
| Calendar privacy leakage | Minimum scopes, no Mail scopes, personal calendars disabled by default, privacy filter, data minimization | `internal/policy`, `internal/config` (scope guard) |
| OAuth credential exposure | HTTPS, secret manager, encryption, no logging, revocation/rotation | `internal/tokens`, `cmd/server` |
| Compromised app server | KMS/Vault key custody, least privilege, short-lived access tokens, audit logging | deployment + `internal/crypto` |
| Brute force / abuse | Per-IP rate limiting on `/connect`, `/oauth`, `/api`; security-event alerting | `internal/httpx/ratelimit.go`, `internal/security` |

## Trust boundaries

- **Browser ↔ backend**: never trust browser-supplied email; the client secret and
  tokens never reach the browser; the code exchange is server-side only.
- **App ↔ Zoho / Google / Fathom**: official documented APIs only; no scraping.
- **Tenant ↔ tenant**: `organization_id` scoping on every access; cross-org access
  is a tested failure.
