# Oauth Security

> Status: in progress (spec §8-13, §25-26, §53).

## Verified Zoho API facts (confirmed against official docs, 2026-08)

| Item | Value | Source |
|---|---|---|
| Authorize | `GET {accounts}/oauth/v2/auth` | Zoho Accounts OAuth v2 |
| Token | `POST {accounts}/oauth/v2/token` | Zoho Accounts OAuth v2 |
| Revoke | `POST {accounts}/oauth/v2/token/revoke` | Zoho Accounts OAuth v2 |
| Identity | `GET {accounts}/oauth/user/info` — returns `ZUID`, `Email`, `Display_Name`, `First_Name`, `Last_Name` | Zoho Accounts |
| List calendars | `GET /api/v1/calendars` → `{"calendars":[…]}`; fields `uid`,`id`,`name`,`type`/`caltype`,`timezone`,`owner`,`description`,`isdefault`,`color` | zoho.com/calendar/help/api/get-calendar-list.html |
| List events | `GET /api/v1/calendars/{uid}/events` — **`range` param is MANDATORY**, span ≤ 31 days, format `yyyyMMdd'T'HHmmss'Z'`; event fields `uid`,`title`,`location`,`description`,`start`,`end`,`lastmodifiedtime` | zoho.com/calendar/help/api/get-events-list.html |
| Auth header | `Authorization: Zoho-oauthtoken {access_token}` | Zoho API |
| Access token TTL | ~1 hour (spec §15) | Zoho |

## Scope review (spec §53)

| Scope | Why | Removable? |
|---|---|---|
| `ZohoCalendar.calendar.READ` | discover calendars (§27) | required |
| `ZohoCalendar.event.READ` | read events for detection (§29) | required |
| `aaaserver.profile.READ` | `/oauth/user/info` identity binding (§25-26); without it Zoho returns `INVALID_OAUTHSCOPE` | required for binding |

No Zoho Mail scopes (§33). No `.ALL` scopes (§11). Write scopes deferred until a
real need is established (§53).

## Notes

- Event objects expose no dedicated conference/URL field, so meeting detection
  scans `location` + `description` text (spec §29 approach confirmed correct).
- `accounts` base is data-center specific (`.com`/`.eu`/`.in`/`.com.au`); set
  `ZOHO_ACCOUNTS_BASE` per the company's Zoho DC.

## Open questions

- §54 org-level vs per-user authorization — resolve during the POC against a real
  test account.

## POC results (2026-08-28, real Zoho account)

Ran `make poc` against a live Zoho account (DC: .com). All 8 steps passed:

- Token exchange: `expires_in` 3600s; refresh token returned; scopes exactly the
  three requested — **no Mail scope**; `api_domain: https://www.zohoapis.com`.
- Identity: ZUID is numeric (confirms `json.Number` decoding and §26 binding).
- Calendars: fields corrected against the live response — `type` is numeric, so
  the string type is `caltype`; `owner` is the ZUID as a string. Empty event
  ranges return a `{"message":"No events found."}` sentinel, filtered by uid.
- Refresh: returns a new access token with **no** new refresh token (§15).
- Revoke: `/oauth/v2/token/revoke` returns `{"status":"success"}`.

### §54 conclusion (preliminary)

The authenticated user saw only its **own** calendar (`category: own`,
`caltype: own`). This is consistent with Zoho's documented per-user consent
model: each employee must authorize the application; there is no evidence of an
org-level shortcut from a single user token. To fully rule out an org-level
mechanism, repeat with an admin/super-admin account and the organization APIs —
but the per-user OAuth architecture is the correct default and is confirmed here.

### Events list — nested start/end (2026-08-28)

The events LIST response nests start/end inside a `dateandtime` object
(`{"timezone","start","end"}`), NOT as top-level fields. Times use a timezone
offset (`20260830T110000+0400`), not `Z`. `ListEvents` reads from `dateandtime`
(with top-level fallback) and `parseZohoTime` accepts the offset format. Meeting
detection confirmed live: a `location` of `https://meet.google.com/...` is
detected as `google_meet`. Without this fix, start/end were nil and the sync
engine would skip every event — nothing would reach Google Calendar.

## Directory endpoint validation (2026-08-28, real org)

Iteratively probed with `cmd/dirprobe` against a live org. Findings:

- The legitimate Zoho **Directory OAuth API v2 exists** and is the compliant path
  (spec §3) — no scraping, no Mail scopes:
  `GET https://www.zohoapis.com/directory/api/v2/orgs/{ZOID}/users`
- Scope: `ZohoDirectory.users.READ` (accepted). `ZohoDirectory.orgs.READ` for org
  endpoints.
- ZOID confirmed = 60037266178 (org-details returned a scope error, not
  not-found, once the right id was used).
- Query params: the Directory admin UI uses `?filter=all&limit=50`.

BLOCKER: with the correct endpoint, scope, ZOID, and the exact UI params, the
OAuth call returns HTTP 500 ("Something went wrong"), while the same request
succeeds in the browser via admin session cookies. This is an auth-context /
authorization issue, not a request-shape issue. Likely cause: the OAuth user is a
Zoho **Mail** admin but not a Zoho **Directory** super-admin (the /orgs listing
also 404'd), or a Zoho-side limitation on OAuth access to this endpoint.

Resolution options (deterministic, not guesswork):
1. Verify/grant Zoho **Directory** super-admin to the account issuing the
   org-level token (directory.zoho.com -> Admins), then retry.
2. Use a confirmed Directory super-admin account for ZOHO_DIRECTORY_REFRESH_TOKEN.
3. If it persists, capture the 500's correlation id and open a Zoho support case
   for OAuth access to /directory/api/v2/orgs/{id}/users.

The AutoSync driver is ready; once the 500 is resolved, set:
  ZOHO_DIRECTORY_USERS_URL=https://www.zohoapis.com/directory/api/v2/orgs/60037266178/users?filter=all&limit=50
  ZOHO_SCOPES=...,ZohoDirectory.users.READ   (for the org-level refresh token)
Until then, CSV import is the working employee source (spec §3).


### Directory: Self Client flow (2026-08-28, final)

Ran the documented Self Client flow (cmd/dirprobe): admin-generated grant token
exchanged successfully for an access token, then GET /directory/api/v2/orgs
returned 404 (no orgs). This is the authoritative result: the token's account
administers zero Zoho Directory orgs. API/scope/flow are all correct — the
blocker is Zoho-side org configuration: either biongenetic.com is a Zoho Mail org
not enrolled in Zoho Directory, or the account is not a Directory admin/owner.
No request shape can change a "no orgs" 404. Resolution is a Zoho admin task
(enroll the org in Zoho Directory and/or grant Directory admin), not code.

Decision: CSV import is the employee source for this deployment. The AutoSync
driver + ZohoClient remain ready for any org whose GET /orgs returns an org_id.


### Directory: SOLVED (2026-08-28)

After enrolling the org in Zoho Directory, the Self Client flow returned 200 with
the org and users. Confirmed live values for Bion Genetic (.com):

- GET /directory/api/v2/orgs -> org_id 936948838 (numeric; NOT the console ZOID).
- GET /directory/api/v2/orgs/936948838/users?page=1&per_page=500&include=emails -> 200.
- Real user fields (v2): user_id, zuid, primary_email, emails[].{email_id,is_primary,
  is_verified}, first_name, last_name, full_name, display_name (a JOB TITLE, not the
  name), user_status ("active"/"inactive"), user_type. There is no department field.

Mapping fixed in internal/directory/zoho.go: name from full_name (not display_name),
status from user_status string (not is_active bool), email from primary_email/emails.
Regression test uses the captured JSON.

Production config (org-level token from a Self Client; refresh with the Self Client
credentials):
  ZOHO_DIRECTORY_USERS_URL=https://www.zohoapis.com/directory/api/v2/orgs/936948838/users?page=1&per_page=500&include=emails
  ZOHO_DIRECTORY_CLIENT_ID / ZOHO_DIRECTORY_CLIENT_SECRET = the Self Client
  ZOHO_DIRECTORY_REFRESH_TOKEN = from exchanging a Self Client grant with
    scope ZohoDirectory.Users.READ (+ Orgs.READ to discover org_id)


### Directory: server-based app works (no Self Client needed) — validated end to end

Confirmed the server-based CALENDAR app (single client) can access the Directory
API when authorized with the directory scopes alongside the calendar scopes:
GET /orgs and /orgs/{id}/users both returned 200. So a separate Self Client is
NOT required — one Zoho app + one admin-authorized directory refresh token.

End-to-end: ran AutoSync against the live directory + a real Postgres. It fetched
12 users and created 12 employee records with correct mapping (full_name, not the
job-title display_name; primary_email; user_status). created=12.

Simplified production config (single app):
  ZOHO_DIRECTORY_USERS_URL=https://www.zohoapis.com/directory/api/v2/orgs/936948838/users?page=1&per_page=500&include=emails
  ZOHO_DIRECTORY_REFRESH_TOKEN=<admin token from authorizing the CALENDAR app with ZohoDirectory.Users.READ>
  ZOHO_DIRECTORY_CLIENT_ID / _SECRET = leave BLANK (worker falls back to the main app to refresh)
