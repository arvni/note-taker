# Fathom — Zoho Calendar → Fathom Bridge

Secure, multi-user calendar synchronization: discovers company employees, obtains
per-employee delegated OAuth consent to Zoho Calendar, detects qualifying meetings,
and mirrors them into a Google Calendar that Fathom records.

See [`PLAN.md`](PLAN.md) for the phased roadmap and [`TASKS.md`](TASKS.md) for the
ticket-level breakdown. Requirements source: the spec markdown in this directory.

## Status

Phase 0 (Zoho POC, spec §58) + Phase 1 foundations are scaffolded:

- `internal/config` — typed env config; rejects Mail/`.ALL` scopes (§11, §33)
- `internal/crypto` — AES-256-GCM token encryption at rest (§13-14)
- `internal/audit` — audit log with enforced secret redaction (§35)
- `internal/onboarding` — high-entropy single-use tokens + state machine (§4, §6-7)
- `internal/oauth` — Zoho Authorization Code flow, state, identity binding (§8-13, §25-26)
- `internal/calendar` — calendar discovery, event fetch, meeting detection (§27, §29)
- `cmd/poc` — end-to-end POC of the 8 spec §58 steps
- `cmd/server`, `cmd/worker` — scaffold entrypoints

## Quick start

```bash
cp .env.example .env          # fill in Zoho + crypto values
make up                       # start postgres + redis
go test ./...                 # run unit tests
make poc                      # run the Zoho POC (needs a real test account)
```

Generate a dev crypto key: `openssl rand -base64 32` → `CRYPTO_MASTER_KEY`.

## Security invariants

Never: store passwords · request Zoho Mail scopes · expose tokens to the browser ·
log tokens · store plaintext refresh tokens · trust browser-supplied email · reuse
onboarding tokens · accept arbitrary redirect URIs. See spec §57 and `docs/security/`.

## Layout

`cmd/` entrypoints · `internal/` packages (one per concern) · `internal/db/migrations`
SQL · `docs/security/` threat model & policies · `test/` integration + e2e.
