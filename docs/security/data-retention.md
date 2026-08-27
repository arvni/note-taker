# Data Retention & Privacy

## Retention (spec §50)

Retention is configurable and enforced by `internal/retention` (a daily worker
purge job). **The values below are EXAMPLES, not legal advice** — the company's
legal/privacy team must review and set them (spec §50, §52).

| Data | Config | Example default | Enforced by |
|---|---|---|---|
| OAuth audit logs | `RETENTION_AUDIT` | 365 days | `Purger` deletes `audit_log` |
| Sync mappings (cancelled) | `RETENTION_MAPPINGS` | 365 days | deletes cancelled `event_mappings` |
| Onboarding/OAuth-state records | fixed 7 days | 7 days | housekeeping delete |
| Fathom transcript | company policy | per policy | not persisted by default (see below) |

Purge cadence: `RETENTION_PURGE_INTERVAL` (default 24h).

## Data minimization (spec §32)

For each qualifying meeting the app stores only: source event id, calendar id,
title, start/end, meeting provider, meeting URL, source-updated timestamp, and the
destination mapping. It does **not** store full descriptions, attachments, private
notes, unnecessary attendee data, or (by default) Fathom transcript/summary bodies.
No Zoho Mail scopes are requested (spec §33).

## GDPR / privacy considerations (spec §51-52)

The system may process employee identity, meeting metadata, meeting URLs, and
(where a company enables it) transcripts. Therefore:

- **Purpose limitation** — data is used only to synchronize qualifying meetings for
  the company's recording workflow.
- **Data minimization** — see above.
- **Retention** — configurable and enforced; documented here.
- **Access control** — RBAC + tenant isolation (`access-control.md`).
- **Revocation & deletion** — employees can revoke; offboarding deletes tokens;
  retention purges metadata.
- **Auditability** — every security-sensitive action is audited without token
  values.

**OAuth technical authorization is not, by itself, a legal basis for recording**
(spec §52). The company must provide a Privacy Notice, Recording Policy, Data
Retention Policy, and support contact, and — where GDPR or another regime
applies — have its legal/privacy team approve the processing model. Do not treat
this document as legal sign-off.
