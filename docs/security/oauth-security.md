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

Probed candidate Zoho org-users REST endpoints against a live account
(cio@biongenetic.com, DC .com) with `cmd/dirprobe`. All returned 404 / error
pages — none returned INVALID_OAUTHSCOPE (which would indicate a real endpoint
needing a scope):

- www.zohoapis.com/organization/v1/users -> 404 "API endpoint not found"
- www.zohoapis.com/directory/v1/users -> 404
- www.zohoapis.com/directory/api/v1/users -> 404
- directory.zoho.com/api/v1/users -> 404
- accounts.zoho.com/api/v1/users -> 404

Conclusion: the `ZohoOne.Users.READ` scope exists but its user-listing surface is
Deluge-only (`zoho.one.getUsers` / `zoho.directory.getUsers`), with no public REST
endpoint reachable by the app for this org. Clean alternatives require Zoho Mail
scopes (forbidden by §33) or are per-product user lists. Therefore **CSV import is
the validated employee-source path for this deployment** (spec §3). The AutoSync
driver remains available for any org whose Zoho setup does expose a users URL.
