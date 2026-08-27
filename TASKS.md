# Task Breakdown — Zoho → Fathom Calendar Bridge

> Granular per-phase tickets derived from `PLAN.md` and the source spec.
> Ticket IDs: `P{phase}-{n}`. Each ticket lists **Files**, **Acceptance**, and **Deps**.
> Section refs (`§n`) point to the source spec.

---

## Project Structure (target Go layout)

```
fathom/
├── cmd/
│   ├── server/            # HTTP server entrypoint (web, OAuth, dashboards, API)
│   │   └── main.go
│   ├── worker/            # background jobs (directory sync, token refresh, calendar sync)
│   │   └── main.go
│   └── poc/               # Phase 0 CLI proof-of-concept (throwaway-ish, kept for regression)
│       └── main.go
├── internal/
│   ├── config/            # env + secret loading, typed config
│   ├── db/                # pgx pool, migrations runner, sqlc-generated queries
│   │   └── migrations/    # NNNN_name.up.sql / .down.sql
│   ├── redisx/            # redis client, distributed lock, rate limiter
│   ├── crypto/            # AES-256-GCM envelope encryption, KMS abstraction
│   ├── audit/             # audit-log writer (token-value redaction enforced)
│   ├── directory/         # Zoho Directory client + CSV import + sync reconciler
│   ├── onboarding/        # state machine, onboarding tokens, invitations
│   ├── email/             # transactional email service (dedicated sender)
│   ├── oauth/             # Zoho OAuth: state, start, callback, token exchange, identity
│   ├── tokens/            # credential store, refresh lifecycle, revocation
│   ├── calendar/          # Zoho calendar discovery, event fetch, meeting detection
│   ├── policy/            # privacy filtering, calendar allowlist, org-vs-employee policy
│   ├── sync/              # source→destination sync engine, mappings, idempotency
│   ├── google/            # Google Calendar destination client
│   ├── fathom/            # Fathom API client (isolated from Zoho)
│   ├── rbac/              # roles, authorization middleware, tenant scoping
│   ├── httpx/             # router, session, CSRF, security headers, rate-limit middleware
│   ├── dashboard/         # admin + employee dashboard handlers/views
│   └── security/          # security-event detection + alerting
├── docs/
│   └── security/          # §49 documents
├── web/                   # templates + static assets (consent, confirmation, dashboards)
├── test/
│   ├── integration/       # DB/Redis-backed tests
│   └── e2e/               # Fathom recording chain test (§42)
├── PLAN.md
├── TASKS.md
├── Makefile
├── docker-compose.yml     # local postgres + redis
├── .env.example
└── go.mod
```

**Conventions:** every DB query carries `organization_id` (tenant isolation, §48). No token value ever enters logs (§35). All external calls use official documented APIs only (§57).

---

## PHASE 0 — Zoho Integration POC  *(§58 — do first)*

Goal: prove the 8 steps against a **real** Zoho test account before building anything else. CLI, not the full server.

| ID | Task | Files | Acceptance | Deps |
|---|---|---|---|---|
| P0-1 | Register Zoho API client (console) + record client_id/secret/redirect in `.env.example` | `.env.example`, `docs/security/oauth-security.md` | Redirect URI registered; secrets loaded from env, never committed | — |
| P0-2 | POC OAuth start: build authorize URL with `ZohoCalendar.calendar.READ ZohoCalendar.event.READ`, `state`, correct DC domain | `cmd/poc/main.go`, `internal/oauth/` | Browser reaches Zoho consent with minimal scopes | P0-1 |
| P0-3 | POC callback + server-side code→token exchange | `cmd/poc/main.go`, `internal/oauth/` | Access + refresh token obtained server-side; secret never in browser (§12) | P0-2 |
| P0-4 | Obtain authenticated Zoho identity from official endpoint | `internal/oauth/identity.go` | Returns Zoho user/account id + email | P0-3 |
| P0-5 | List calendars | `internal/calendar/discover.go` | Prints calendar_id/name/type/timezone/owner | P0-3 |
| P0-6 | List events for a calendar | `internal/calendar/events.go` | Returns event list via `event.READ` | P0-5 |
| P0-7 | Detect meeting URL (Meet/Zoom/Teams) from location/description/conference | `internal/calendar/detect.go` | Correctly classifies provider on sample events | P0-6 |
| P0-8 | Refresh access token | `internal/tokens/refresh.go` | New access token from refresh token; no new refresh minted (§15) | P0-3 |
| P0-9 | Revoke token | `internal/tokens/revoke.go` | Zoho revocation endpoint returns success; token no longer works (§17) | P0-3 |
| P0-10 | **Answer §54:** document whether org-level Calendar auth exists or per-user OAuth is mandatory | `docs/security/oauth-security.md` | Written conclusion with evidence | P0-3..9 |

**Exit gate:** P0-1..10 all pass with a real test account. Do not start Phase 8+ until this holds (§58).

---

## PHASE 1 — Foundations & Data Model  *(§34, §13, §14, §35, §48)*

| ID | Task | Files | Acceptance | Deps |
|---|---|---|---|---|
| P1-1 | Repo scaffold, go.mod, Makefile, docker-compose (pg+redis), config loader | `cmd/server/main.go`, `internal/config/`, `docker-compose.yml`, `Makefile` | `make up` runs server + deps; typed config from env | — |
| P1-2 | TLS/HTTPS + security headers + health/readiness | `internal/httpx/` | HTTPS in prod; `/healthz` `/readyz` | P1-1 |
| P1-3 | Migration runner + `employees`, `onboarding_tokens`, `oauth_states`, `oauth_credentials`, `calendars`, `event_mappings`, `audit_log` | `internal/db/migrations/` | `make migrate` applies/rolls back clean | P1-1 |
| P1-4 | Redis client + distributed lock + rate limiter primitives | `internal/redisx/` | Lock acquire/release + token-bucket unit tested | P1-1 |
| P1-5 | Secret mgmt + AES-256-GCM envelope encryption; key NOT in Postgres | `internal/crypto/`, `internal/config/` | Encrypt/decrypt round-trip; key from KMS/env-secret (§14) | P1-1 |
| P1-6 | Audit writer with enforced token redaction | `internal/audit/` | Attempting to log a token field is dropped/rejected; unit test proves it (§35) | P1-3 |
| P1-7 | Tenant-scoping helper: all queries require `organization_id` | `internal/db/` | Query without org scope fails lint/test guard (§48) | P1-3 |

---

## PHASE 2 — Directory Sync  *(§3, §20, §55)*

| ID | Task | Files | Acceptance | Deps |
|---|---|---|---|---|
| P2-1 | Zoho Directory client: fetch users (id/email/name/dept/status) | `internal/directory/zoho.go` | Returns normalized user list; official API only | P1-3 |
| P2-2 | CSV import fallback (`email,name,department,status`) | `internal/directory/csv.go` | Admin upload parsed + validated | P1-3 |
| P2-3 | Reconciler: new→create+queue invite, active→continue, inactive→disable, email-changed→careful update | `internal/directory/sync.go` | Credential never reassigned to new email; bound to zoho_user_id (§20) | P2-1, P3-* |
| P2-4 | Hourly scheduled job (or webhook) | `cmd/worker/main.go` | Runs on interval; idempotent | P2-3 |

---

## PHASE 3 — Onboarding & Consent  *(§4–7, §21–23)*

| ID | Task | Files | Acceptance | Deps |
|---|---|---|---|---|
| P3-1 | Onboarding state machine (`not_invited`…`disabled`) | `internal/onboarding/state.go` | Only legal transitions allowed; unit tested (§4) | P1-3 |
| P3-2 | Onboarding token: 32B min/48B pref, hash-only store, 7d configurable expiry, single-use | `internal/onboarding/token.go` | Raw token never stored; `used_at` blocks reuse (§6–7) | P1-3, P1-5 |
| P3-3 | Email service, dedicated sender, approved transport | `internal/email/` | Sends from `calendar-integration@…`; no employee creds (§21) | P1-1 |
| P3-4 | Invitation template: purpose/permissions/privacy/button/expiry/support; no tokens in URL | `web/templates/invite.html`, `internal/onboarding/` | Anti-phishing checks pass; no secrets in link (§22–23) | P3-2, P3-3 |
| P3-5 | Consent page: company/app/email/permissions/privacy/terms before Zoho redirect | `web/templates/consent.html`, `internal/onboarding/` | Renders all required items pre-redirect (§23) | P3-2 |

---

## PHASE 4 — OAuth Flow & Token Storage  *(§8–13, §24–26)*

| ID | Task | Files | Acceptance | Deps |
|---|---|---|---|---|
| P4-1 | Routes `GET /connect/:token`, `/oauth/zoho/start`, `/oauth/zoho/callback` | `internal/httpx/routes.go`, `internal/oauth/` | Wired with rate limiting (§8) | P1-2, P11-1 |
| P4-2 | `oauth_states`: crypto-random state, hash-only, short expiry, no PII in value | `internal/oauth/state.go` | Callback verifies exists/hash/expiry/unused/provider/employee then invalidates (§9) | P1-3, P1-5 |
| P4-3 | Registered redirect URI only; reject arbitrary redirect params; HTTPS | `internal/oauth/start.go` | Query-param redirect rejected (§10) | P4-1 |
| P4-4 | Scope config (read-only default), documented; follow Zoho PKCE guidance | `internal/oauth/scopes.go` | No `.ALL`; scopes documented (§11, §53) | P4-1 |
| P4-5 | Server-side token exchange; client_secret never to browser | `internal/oauth/exchange.go` | Exchange only server-side (§12) | P4-2 |
| P4-6 | Account binding: fetch+verify Zoho identity vs employee record; mismatch→failed+review | `internal/oauth/identity.go`, `internal/onboarding/state.go` | Mismatch sets `authorization_failed`, no credential attached (§25–26) | P4-5 |
| P4-7 | Persist credentials with encrypted access+refresh tokens | `internal/tokens/store.go` | Ciphertext at rest; decrypt round-trips (§13) | P1-5, P4-5 |
| P4-8 | Confirmation page (account/permissions/Connected; no tokens shown) | `web/templates/connected.html` | No token values rendered (§24) | P4-7 |

---

## PHASE 5 — Token Lifecycle  *(§15–18, §45)*

| ID | Task | Files | Acceptance | Deps |
|---|---|---|---|---|
| P5-1 | Refresh-on-expiry logic (~1h), avoid minting new refresh tokens | `internal/tokens/refresh.go` | Refreshes near expiry only (§15) | P4-7 |
| P5-2 | Redis refresh lock `zoho-token-refresh:{employee_id}` | `internal/tokens/refresh.go`, `internal/redisx/` | Concurrent refresh serialized; test proves single-writer (§16) | P1-4, P5-1 |
| P5-3 | `POST /api/employees/:id/revoke`: mark→revoke Zoho→stop jobs→audit→delete per retention | `internal/tokens/revoke.go`, `internal/httpx/` | Full sequence + audit event (§17) | P4-7 |
| P5-4 | Auto-detect invalid refresh → `revoked`, stop retries, surface reconnect + email | `internal/tokens/refresh.go`, `internal/email/` | No retry storm; reconnect action created (§18) | P5-1 |
| P5-5 | Reauthorization flow (repeat consent, no reuse of invalid creds) | `internal/onboarding/`, `internal/oauth/` | Disconnected employee redone via full OAuth (§45) | P4-*, P5-4 |

---

## PHASE 6 — Calendar Discovery & Detection  *(§27–33)*

| ID | Task | Files | Acceptance | Deps |
|---|---|---|---|---|
| P6-1 | Discover calendars post-OAuth; store id/name/type/tz/owner; no auto-sync-all | `internal/calendar/discover.go` | Calendars stored, none auto-enabled (§27) | P4-7 |
| P6-2 | Default policy: work calendars monitored, personal disabled by default | `internal/policy/default.go` | Personal calendars off unless explicitly enabled (§28) | P6-1 |
| P6-3 | Event fetch + meeting detection (Meet/Zoom/Teams) | `internal/calendar/events.go`, `internal/calendar/detect.go` | Provider detected from location/desc/conference (§29) | P6-1 |
| P6-4 | Privacy filter (calendar/meeting/employee/org policy) before store | `internal/policy/filter.go` | Personal/HR/private ignored; client meetings pass (§30) | P6-3 |
| P6-5 | Employee settings (`recording_enabled`, `allowed_calendar_ids`); org-vs-preference distinction | `internal/policy/employee.go` | Org override path exists and is explicit (§31) | P6-2 |
| P6-6 | Data minimization on stored events | `internal/calendar/events.go`, `internal/db/` | Only minimal fields persisted (§32) | P6-4 |
| P6-7 | Guard: never request Mail scopes | `internal/oauth/scopes.go` (test) | Scope set contains no Mail scope; test enforces (§33) | P4-4 |

---

## PHASE 7 — Offboarding  *(§19 — mandatory)*

| ID | Task | Files | Acceptance | Deps |
|---|---|---|---|---|
| P7-1 | Inactive-employee handler: stop sync, cancel jobs, revoke, delete tokens, mark disconnected, keep minimal audit | `internal/directory/offboard.go`, `internal/tokens/`, `internal/sync/` | All 7 steps executed; no further calendar access (§19) | P2-3, P5-3, P8-1 |

---

## PHASE 8 — Sync Engine  *(§42, objectives 12–16, §48 duplicate mitigation)*

| ID | Task | Files | Acceptance | Deps |
|---|---|---|---|---|
| P8-1 | Google Calendar destination client | `internal/google/` | Create/update/delete events (official API) | P1-1 |
| P8-2 | Mapping store + unique constraints (idempotency, no duplicates) | `internal/sync/mapping.go`, migration | Same source event never duplicated (§48) | P1-3 |
| P8-3 | Sync engine: create/update/cancel destination on source change/cancel | `internal/sync/engine.go`, `cmd/worker/main.go` | Source change → dest update; source cancel → dest remove | P6-4, P8-1, P8-2 |
| P8-4 | Sync worker scheduling + backoff | `cmd/worker/main.go` | Runs per enabled calendar; resilient to API errors | P8-3 |

---

## PHASE 9 — Fathom Integration  *(§43)*

| ID | Task | Files | Acceptance | Deps |
|---|---|---|---|---|
| P9-1 | Fathom client (documented endpoints only), isolated module | `internal/fathom/` | No Zoho imports; documented API only (§43) | P1-1 |
| P9-2 | Post-meeting processing where available (meeting/transcript/summary/action-items/webhooks) | `internal/fathom/` | Handles available artifacts + webhook intake | P9-1 |
| P9-3 | **E2E test (§42):** dest event created → Fathom detects → Fathom records | `test/e2e/fathom_chain_test.go` | Chain verified against real/staging Fathom | P8-3, P9-1 |

---

## PHASE 10 — Dashboards & RBAC  *(§38–41, §46–47)*

| ID | Task | Files | Acceptance | Deps |
|---|---|---|---|---|
| P10-1 | RBAC roles + server-side authz middleware + tenant scope | `internal/rbac/` | 4 roles enforced server-side (§41) | P1-7 |
| P10-2 | Admin auth via OIDC/SSO (+MFA where possible) | `internal/rbac/oidc.go`, `internal/httpx/` | No new password DB (§39) | P10-1 |
| P10-3 | Session security: Secure/HttpOnly/SameSite, short TTL, rotation, CSRF | `internal/httpx/session.go` | Cookie flags + CSRF verified (§38) | P1-2 |
| P10-4 | Admin dashboard (totals + per-employee status/calendars/last sync/errors) | `internal/dashboard/admin.go`, `web/` | Renders §46 fields | P10-1 |
| P10-5 | Employee dashboard (own data only; Reconnect/Disconnect/Manage) | `internal/dashboard/employee.go`, `web/` | Scoped to self; buttons work (§47) | P10-1, P5-5, P6-5 |

---

## PHASE 11 — Rate Limiting & Security Monitoring  *(§36–37)*

| ID | Task | Files | Acceptance | Deps |
|---|---|---|---|---|
| P11-1 | Redis rate limiting on `/connect/*`, `/oauth/*`, `/api/*` (configurable) | `internal/httpx/ratelimit.go` | e.g. 10 callback/IP/10min; configurable (§37) | P1-4 |
| P11-2 | Security-event detection + alerting (failed OAuth, identity mismatch, refresh failures, suspicious onboarding, unusual volume) | `internal/security/` | Alerts fire on thresholds (§36) | P1-6, P4-6, P5-4 |

---

## PHASE 12 — Docs, Retention, Compliance  *(§49–52)*  *(runs alongside; finalized last)*

| ID | Task | Files | Acceptance | Deps |
|---|---|---|---|---|
| P12-1 | Security docs set | `docs/security/{threat-model,oauth-security,token-storage,employee-onboarding,data-retention,access-control,incident-response,offboarding}.md` | All 8 files present + reviewed (§49) | ongoing |
| P12-2 | Configurable retention + enforcement jobs | `internal/config/`, `cmd/worker/` | Retention values configurable; purge jobs run (§50) | P1-3 |
| P12-3 | Privacy/GDPR docs + legal-approved processing basis | `docs/security/data-retention.md`, privacy notice | Distinguishes OAuth authz from legal basis; legal sign-off recorded (§51–52) | P12-1 |

---

## Cross-Cutting Test Matrix  *(§57)*

| Test | Ticket(s) covering it |
|---|---|
| Authorization failures | P4-6, P10-1 |
| Token revocation | P5-3, P5-4 |
| Employee offboarding | P7-1 |
| Identity mismatch | P4-6 |
| Duplicate onboarding | P3-2 |
| Duplicate event sync | P8-2 |
| Fathom recording chain | P9-3 |
| Token never logged | P1-6 |
| Refresh single-writer lock | P5-2 |
| No Mail scopes | P6-7 |

---

## Suggested Sprint Grouping

- **Sprint 1:** Phase 0 (POC) — the gate.
- **Sprint 2:** Phase 1 + start Phase 2, Phase 11-1 (rate-limit primitive), Phase 12-1 (doc skeletons).
- **Sprint 3:** Phase 3 + Phase 4.
- **Sprint 4:** Phase 5 + Phase 6.
- **Sprint 5:** Phase 7 + Phase 8.
- **Sprint 6:** Phase 9 + Phase 10.
- **Sprint 7:** Phase 11-2 + Phase 12 finalize + full test-matrix pass.
