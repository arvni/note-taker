# Fathom — Zoho Calendar → Fathom Bridge

![CI](https://github.com/arvinizadi/fathom/actions/workflows/ci.yml/badge.svg)

Secure, multi-user calendar synchronization: discovers company employees, obtains
per-employee delegated OAuth consent to Zoho Calendar, detects qualifying meetings,
mirrors them into a Google Calendar that Fathom records, and confirms the chain.

See [`PLAN.md`](PLAN.md) for the roadmap and [`TASKS.md`](TASKS.md) for the
ticket-level breakdown. Requirements source: the spec markdown in this directory.
Security documentation: [`docs/security/`](docs/security).

## Pipeline (all phases implemented)

Directory sync → onboarding invite → consent → Zoho OAuth (server-side, identity-
verified) → encrypted credential → refresh/revoke/reconnect/offboard → calendar
discovery → meeting detection + privacy filter → Google Calendar sync
(create/update/cancel) → Fathom recording confirmation, behind RBAC dashboards,
rate limiting, security monitoring, and retention purge.

## Packages

| Package | Responsibility | Key spec |
|---|---|---|
| `config` | typed env; rejects Mail/`.ALL` scopes | §11, §33 |
| `crypto` | AES-256-GCM token encryption | §13-14 |
| `audit` | audit log with enforced secret redaction | §35 |
| `db` | pgx pool, migrations, tenant-scope guard | §34, §48 |
| `redisx` | client, distributed lock, rate limiter | §16, §37 |
| `directory` | Zoho Directory + CSV, reconciler, dashboards | §3, §20, §46 |
| `onboarding` | tokens, state machine, invite service | §4-7, §44 |
| `email` | transactional sender + templates | §21-22 |
| `oauth` | Zoho Authorization Code flow, state, identity | §8-13, §25-26 |
| `tokens` | encrypted store, refresh/lock, revoke, offboard | §13-19 |
| `calendar` | discovery, detection, minimized store, sync-state | §27-32 |
| `policy` | personal/work classification, privacy filter | §28-31 |
| `google` | Google Calendar destination client | §42 |
| `sync` | create/update/cancel engine | §42 |
| `fathom` | Fathom API client + webhook receiver | §43 |
| `rbac` | roles, sessions, CSRF | §38-41 |
| `security` | threshold-based alerting | §36 |
| `retention` | configurable purge jobs | §50 |
| `httpx` | HTTP handlers + middleware | §8, §37, §46-47 |

## Quick start

```bash
cp .env.example .env          # fill Zoho + crypto + session values
openssl rand -base64 32       # -> CRYPTO_MASTER_KEY and SESSION_KEY
make up                       # start postgres + redis
make migrate                  # apply schema
go test ./...                 # unit + integration tests (deps auto-skip if absent)
make server                   # run the HTTP server
make worker                   # run the sync + retention worker
make poc                      # Zoho POC (needs a real test account, spec §58)
```

## Testing

Unit tests run anywhere. Integration tests are gated on `DATABASE_URL` /
`REDIS_URL` and skip cleanly when absent; with Docker deps up they exercise real
Postgres/Redis. The spec §42 live recording-chain test (`test/e2e`) requires real
Fathom + Google and is gated on `FATHOM_E2E`.

## Known integration seams (require external credentials)

- **Admin SSO** — `httpx.IdentityProvider` (OIDC/JWKS), wired per company IdP.
- **Google auth** — `google.TokenSource` (service-account JWT exchange).
- **Directory source** — per-org Zoho Directory token, pending the §54 org-auth
  decision (CSV import works today).

## Security invariants (spec §57)

Never: store passwords · request Zoho Mail scopes · expose tokens to the browser ·
log tokens · store plaintext refresh tokens · trust browser-supplied email · reuse
onboarding tokens · accept arbitrary redirect URIs · allow cross-org access.
