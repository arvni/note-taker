# Implementation Plan — Zoho Company Calendar → Fathom Calendar Bridge

> Derived from `Zoho Company Calendar → Fathom Calendar Bridge — Secure Multi-User OAuth Application.md`.
> This plan turns the 58-section spec into an ordered, buildable roadmap. Section references (e.g. `spec §13`) point back to the source document.

---

## 0. Guiding Principles (non-negotiable)

These constraints from spec §57 shape every phase. Treat them as acceptance gates, not afterthoughts.

**MUST NOT:** store passwords · request Zoho Mail scopes · expose tokens to frontend JS · put tokens/codes in email links · log tokens · store plaintext refresh tokens · trust browser-supplied email · assume admin access == employee calendar access · auto-access personal calendars · use undocumented Zoho/Fathom APIs · scrape · bypass consent · reuse onboarding tokens · accept arbitrary redirect URIs · allow cross-org data access.

**MUST:** OAuth 2.0 Authorization Code (server-side) · minimum scopes · HTTPS · validate OAuth `state` · verify authenticated Zoho identity · encrypt refresh tokens · secret manager/KMS · token-refresh locking · revocation · offboarding · audit logs · tenant isolation · rate limiting · idempotency · data minimization · documented threat model · tested failure paths.

**Authorization model (spec §2, §54):** per-employee delegated OAuth with user-scoped tokens. The admin never receives or impersonates employee credentials. Do **not** assume an org-level shortcut exists until verified against the company's actual Zoho configuration.

---

## 1. Tech Stack (spec §56)

| Layer | Choice | Notes |
|---|---|---|
| Language | Go | Per recommended architecture |
| Database | PostgreSQL | Employees, tokens, mappings, audit |
| Cache / locks / rate-limit | Redis | Distributed refresh lock, rate limiting |
| Secrets | KMS/Vault (prod) · env var w/ secret mgmt (small deploy) | Encryption key never in Postgres (spec §14) |
| Encryption | AES-256-GCM (authenticated) | Tokens at rest (spec §13) |
| Source API | Zoho Directory + Zoho Calendar (official) | No scraping |
| Destination | Google Calendar → Fathom | Fathom detects meeting on Google Calendar |
| Admin auth | OIDC/SSO (Zoho Directory / Entra / Google Workspace) | No new password DB (spec §39) |
| Employee auth | Zoho OAuth only | No employee password (spec §40) |

---

## 2. Phase Roadmap (build order)

### PHASE 0 — Zoho Integration Proof of Concept  *(spec §58 — do this FIRST)*
**Goal:** validate assumptions against a *real* employee test account before building anything else.

POC does only:
1. OAuth (Authorization Code, server-side)
2. Obtain authenticated user identity
3. Obtain Calendar READ permission
4. List calendars
5. List events
6. Detect meeting URL (Meet/Zoom/Teams)
7. Refresh access token
8. Revoke token

**Exit criteria:** all 8 steps work end-to-end with a real Zoho test account, and the org-level-vs-per-user authorization question (spec §54) is answered definitively.

---

### PHASE 1 — Foundations & Data Model  *(spec §34)*
- Project scaffold (Go modules, config, HTTPS/TLS, health checks).
- Postgres migrations for core tables (see §3 below).
- Redis wiring (locks + rate limiter).
- Secret management + AES-256-GCM envelope encryption helper (spec §13–14).
- Structured audit-log writer that **refuses to log token values** (spec §35).
- Multi-tenant scoping: every query enforces `organization_id` (spec §48 privilege-escalation mitigation).

---

### PHASE 2 — Employee Directory Sync  *(spec §3, §20, §55)*
- Zoho Directory API client → pull `zoho_user_id, email, name, department, status`.
- CSV import fallback (`email,name,department,status`) when the API can't provide directory data.
- Hourly sync job (or webhook if available):
  - new user → create employee record → queue onboarding email
  - active → continue · inactive → disable (triggers offboarding, Phase 7)
  - email changed → update carefully; **never** reassign a credential to a new email. Credential stays bound to original Zoho account ID.
- Keep Directory access conceptually separate from Calendar authorization (spec §55).

---

### PHASE 3 — Onboarding & Consent Invitations  *(spec §4–7, §21–23)*
- Onboarding state machine: `not_invited → invited → opened → authorization_started → authorized | authorization_failed | revoked | disabled` (spec §4).
- High-entropy onboarding token: 32-byte min (48 preferred), store **hash only**, 7-day configurable expiry, single-use (`used_at`) (spec §6–7).
- Email service (spec §21): send from dedicated address (e.g. `calendar-integration@company.com`) via approved transactional/Zoho Mail mechanism. Never from employee accounts, never using employee credentials.
- Email content (spec §22–23): purpose, requested permissions, privacy notice, authorization button, expiration, support contact. No tokens/codes in URL. Anti-phishing: verified TLS domain, no URL shorteners/IP links.
- Consent page (spec §23): show company name, app name, employee email, requested permissions, privacy policy, terms — *before* redirecting to Zoho.

---

### PHASE 4 — OAuth Flow & Token Handling  *(spec §8–13, §24–26)*
- Endpoints: `GET /connect/:token`, `GET /oauth/zoho/start`, `GET /oauth/zoho/callback` (spec §8).
- `oauth_states` table: CSRF `state` (crypto-random, hashed), single-use, short expiry; **no** employee ID/email embedded in the value. Callback verifies existence, hash, expiry, unused, provider==zoho, valid employee association, then invalidates immediately (spec §9).
- Redirect URI registered in Zoho API Console; HTTPS only; never accept arbitrary redirect from query params (spec §10). Follow Zoho's current docs re: PKCE (don't assume required) (spec §8).
- Scopes (spec §11, §53): start read-only `ZohoCalendar.calendar.READ` + `ZohoCalendar.event.READ`. Never request `.ALL` unless genuinely needed.
- Token exchange server-side only; client_secret never reaches browser (spec §12).
- **Account binding (spec §25–26):** after exchange, fetch authenticated Zoho identity from official endpoint, verify it matches the expected employee record's Zoho account ID. Mismatch → `authorization_failed` + admin review. Never trust browser-supplied email.
- Store credentials in `oauth_credentials` with **encrypted** access + refresh tokens (spec §13).
- Confirmation page (spec §24): show account, granted permissions, "Connected" — never show token values.

---

### PHASE 5 — Token Lifecycle Management  *(spec §15–18, §45)*
- Refresh when access token (~1h) is near expiry; don't mint new refresh tokens unnecessarily (spec §15).
- Distributed refresh lock in Redis: `zoho-token-refresh:{employee_id}`, one worker at a time (spec §16).
- Revocation endpoint `POST /api/employees/:id/revoke` (spec §17): mark revoked → revoke Zoho refresh token if appropriate → stop sync jobs → keep audit → delete encrypted tokens per retention policy.
- Auto-detect revocation (spec §18): on invalid-refresh-token auth error → `status = revoked`, stop retrying, surface "Reconnect Zoho Calendar" action + email.
- Reauthorization flow (spec §45): disconnected employees repeat full OAuth consent; never reuse invalid creds.

---

### PHASE 6 — Calendar Discovery & Meeting Detection  *(spec §27–33)*
- After OAuth, `GET /calendars` → store `calendar_id, name, type, timezone, owner`. Do **not** auto-sync every calendar (spec §27).
- Default calendar policy (spec §28): monitor only company/work calendars; **personal calendars disabled by default** (major privacy requirement). Admin/employee can select.
- Meeting detection (spec §29): `GET /calendars/{uid}/events`; detect Google Meet / Zoom / Teams from location, description, conference info.
- Privacy filtering (spec §30): apply calendar/meeting/employee/organization policy before storing. Personal / internal-HR / private → ignore; qualifying client meetings → sync.
- Employee-level settings (spec §31): `recording_enabled`, `allowed_calendar_ids`; keep org-policy vs employee-preference distinct (org may override where appropriate).
- Data minimization (spec §32): store only source event ID, calendar ID, title, start, end, provider, meeting URL, source-updated timestamp, destination mapping. Avoid full description/attachments/notes/attendees unless required.
- No Mail scopes ever (spec §33).

---

### PHASE 7 — Offboarding  *(spec §19)*  *(mandatory)*
When an employee goes inactive in the directory:
1. Stop calendar sync · 2. Cancel queued jobs · 3. Revoke OAuth if appropriate · 4. Remove stored tokens · 5. Mark disconnected · 6. Preserve minimal audit info · 7. Stop all calendar access.

---

### PHASE 8 — Synchronization Engine  *(spec §12–16 of objectives, §42)*
- Map qualifying Zoho source events → Google Calendar destination events.
- `event_mappings` for source↔destination tracking (spec §32).
- Idempotency + unique constraints to **prevent duplicates** (spec §48 duplicate-meeting mitigation).
- Update destination when source changes; cancel/remove destination when source is cancelled.
- Integration test confirming the full chain (spec §42): destination event created → Fathom detects meeting → Fathom records. Do not assume creating a Google event guarantees Fathom recording.

---

### PHASE 9 — Fathom Integration  *(spec §43)*
- Use only currently documented official Fathom APIs; no undocumented endpoints.
- Support post-meeting processing where officially available: meeting, transcript, summary, action items, webhooks.
- Keep Fathom code isolated from Zoho code (clean module boundary).

---

### PHASE 10 — Dashboards & RBAC  *(spec §38–41, §46–47)*
- Roles (spec §41): organization admin · calendar manager · employee · read-only auditor. Server-side authorization on every action.
- Admin auth via OIDC/SSO + MFA where possible (spec §38–39).
- Company dashboard (spec §46): totals (connected/pending/revoked/failed) + per-employee status, calendars, last sync, meetings synced, last error.
- Employee dashboard (spec §47): own data only; Reconnect / Disconnect / Manage calendars.
- Session security (spec §38): Secure + HttpOnly + SameSite cookies, short lifetime, rotation, CSRF.

---

### PHASE 11 — Rate Limiting & Security Monitoring  *(spec §36–37)*
- Redis rate limiting on `/connect/*`, `/oauth/*`, `/api/*` (esp. OAuth start/callback, onboarding-token validation). Configurable limits (e.g. 10 callback attempts/IP/10 min) (spec §37).
- Security alerts (spec §36): repeated failed OAuth, identity mismatch, unexpected account identity, repeated refresh failures, suspicious onboarding activity, unusual API volume.

---

### PHASE 12 — Documentation, Retention & Compliance  *(spec §49–52)*
- Security docs under `docs/security/` (spec §49): `threat-model.md`, `oauth-security.md`, `token-storage.md`, `employee-onboarding.md`, `data-retention.md`, `access-control.md`, `incident-response.md`, `offboarding.md`.
- Configurable retention (spec §50): OAuth audit logs ~365d, calendar metadata ~90d, sync mappings ~365d, Fathom transcript per policy. Values are examples — make configurable, document, don't assume legally correct.
- Privacy/GDPR (spec §51–52): document purpose limitation, minimization, retention, access control, revocation, deletion, auditability. Distinguish OAuth technical authorization from company recording/legal basis. Legal/privacy team approves the processing model.

---

## 3. Data Model Summary  *(spec §34, §13, §9, §6)*

| Table | Key fields | Security notes |
|---|---|---|
| `employees` | id, organization_id, zoho_user_id, email, name, department, status, onboarding_status, timestamps | tenant-scoped by organization_id |
| `onboarding_tokens` | id, employee_id, token_hash, expires_at, used_at, created_at | hash only, single-use, short expiry |
| `oauth_states` | id, employee_id, provider, state_hash, expires_at, used_at, created_at | hash only, single-use, no PII in value |
| `oauth_credentials` | id, employee_id, provider, provider_account_id, access_token_ciphertext, refresh_token_ciphertext, access_token_expires_at, scopes, api_domain, status, last_refresh_at, last_successful_api_call_at, revoked_at, timestamps | AES-256-GCM encrypted tokens |
| `calendars` | calendar_id, employee_id, name, type, timezone, owner, enabled | personal disabled by default |
| `event_mappings` | source_event_id, calendar_id, title, start, end, provider, meeting_url, source_updated_at, destination_event_id | minimized data, unique constraint for idempotency |
| `audit_log` | actor, action, entity, organization_id, metadata, created_at | never contains token values |

---

## 4. Audit Events  *(spec §35)*
`employee_invitation_sent`, `employee_invitation_opened`, `oauth_started`, `oauth_completed`, `oauth_failed`, `token_refreshed`, `token_refresh_failed`, `calendar_connected`, `calendar_disconnected`, `calendar_enabled`, `calendar_disabled`, `meeting_synchronized`, `employee_revoked`, `employee_disabled`, `admin_reconnected`.

---

## 5. Threat Model → Mitigations  *(spec §48)*

| Threat | Mitigation |
|---|---|
| OAuth CSRF | single-use `state`, short expiry |
| Token theft | encryption at rest, KMS, no logging, no frontend exposure |
| Identity substitution | Zoho identity verification, employee-ID binding, account-ID check |
| Onboarding-link theft | high-entropy token, short expiry, single-use, hashed in DB |
| Duplicate meetings | event mappings, idempotency, unique constraints |
| Privilege escalation | RBAC, organization_id enforcement, server-side authz |
| Calendar privacy leakage | min scopes, calendar allowlist, data minimization, no Mail scopes |
| Credential exposure | HTTPS, secret manager, encryption, secure logs, rotation/revocation |
| Compromised app server | KMS, least privilege, network isolation, short-lived tokens, audit, hardening |

---

## 6. Required Test Coverage  *(spec §57)*
Authorization failures · token revocation · employee offboarding · identity mismatch · duplicate onboarding · duplicate event synchronization · plus the Phase 8 Fathom end-to-end recording test (spec §42).

---

## 7. Open Questions to Resolve Before/During Phase 0
1. Does the company's Zoho org support any approved org-level Calendar authorization, or is per-user OAuth mandatory? (spec §54) — **answer during POC.**
2. Which secret backend for this deployment (KMS/Vault vs managed env secret)? (spec §14)
3. Which admin IdP (Zoho Directory SSO / Entra / Google Workspace)? (spec §39)
4. Approved transactional email mechanism for onboarding sends? (spec §21)
5. Legally-approved retention values and GDPR processing basis? (spec §50–52)
6. Confirm current documented Fathom API surface (webhooks/transcript availability). (spec §43)

---

## 8. Milestone Sequence (dependency-ordered)

```
Phase 0 (POC, validates assumptions)
      ↓
Phase 1 (foundations) ──→ Phase 2 (directory sync)
      ↓                          ↓
Phase 3 (onboarding) ──→ Phase 4 (OAuth) ──→ Phase 5 (token lifecycle)
      ↓                          ↓
Phase 6 (calendar discovery/detection)
      ↓
Phase 7 (offboarding)   Phase 8 (sync engine) ──→ Phase 9 (Fathom)
      ↓                          ↓
Phase 10 (dashboards/RBAC) ──→ Phase 11 (rate limit/monitoring)
      ↓
Phase 12 (docs/retention/compliance) — runs alongside, finalized last
```

**Critical gate:** Do not build the full sync engine (Phase 8+) until the Phase 0 POC succeeds with a real employee test account (spec §58).
