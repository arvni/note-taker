# Deployment (Docker on a single VM)

Runs the app + worker + Postgres + Redis + Caddy (automatic HTTPS) with
`docker compose`. Caddy terminates TLS and reverse-proxies to the server.

## Prerequisites
- A Linux VM with Docker + Docker Compose.
- A domain name with a DNS **A record** pointing at the VM's public IP.
- Ports **80** and **443** open to the internet (Caddy needs 80 for ACME).
- Registered OAuth apps (see below) and a Google service-account key.

## 1. Configure DNS + Caddy
- Point `calendar-sync.yourco.com` at the VM.
- Edit `Caddyfile`, replacing `calendar-sync.example.com` with your domain.

## 2. Register OAuth apps with the production redirect URIs
- **Zoho** (api-console.zoho.com): redirect `https://<domain>/oauth/zoho/callback`.
- **Google OIDC** (Google Cloud console → OAuth client, type Web):
  redirect `https://<domain>/auth/callback`. Note client id/secret.
- **Google service account**: create a key, enable the Calendar API, and share
  the destination (Fathom-watched) calendar with the service-account email as
  "Make changes to events". Save the JSON to `./secrets/google-sa.json`.

## 3. Secrets
```bash
cp .env.prod.example .env.prod
# fill every value; generate keys:
openssl rand -base64 32   # CRYPTO_MASTER_KEY
openssl rand -base64 32   # SESSION_KEY
mkdir -p secrets && cp /path/to/google-sa.json secrets/google-sa.json
```
Mount the key by adding to the server+worker services in compose:
```yaml
    volumes:
      - ./secrets/google-sa.json:/app/secrets/google-sa.json:ro
```
(Or use Docker/host secret management instead of a file.)

## 4. Bring it up
```bash
docker compose -f docker-compose.prod.yml run --rm migrate   # apply schema
docker compose -f docker-compose.prod.yml up -d               # start everything
docker compose -f docker-compose.prod.yml logs -f server      # watch startup
```
Verify: `https://<domain>/healthz` returns ok, `/readyz` returns ready.
Admin login: visit `https://<domain>/login` (must be an allowlisted admin email).

## 5. Onboard employees
- Upload the employee CSV (email,name,department,status) via the admin flow, or
  configure `ZOHO_DIRECTORY_USERS_URL` if your org's Directory API is available.
- Employees receive the consent email and connect their Zoho Calendar.


## Employee source: Zoho Directory auto-sync vs CSV

Two ways to populate employees (spec §3). Both are implemented and validated.

### Option A — Zoho Directory auto-sync (validated end to end)

The worker pulls the org's users hourly and reconciles them (invite new active,
offboard inactive). It uses the **server-based calendar app** — no separate Self
Client is needed. One-time admin setup:

1. **Enroll the org in Zoho Directory** (directory.zoho.com) and confirm the admin
   account is a Directory admin. (If GET /orgs returns no orgs, this step is missing.)
2. **Find the Directory org_id** (a number from the API, NOT the console ZOID):
   authorize the calendar app once with `ZohoDirectory.Orgs.READ,ZohoDirectory.Users.READ`
   and call `GET https://www.zohoapis.com/directory/api/v2/orgs`. The `org_id` is in
   the response. (`cmd/dirprobe` automates this and prints the refresh token.)
3. **Get a directory refresh token**: authorize the calendar app (as the Directory
   admin) with scope `ZohoDirectory.Users.READ` (a one-time consent, separate from
   employee calendar consent) and keep the returned refresh token.
4. **Configure** (in `.env.prod`):
   ```
   ZOHO_DIRECTORY_USERS_URL=https://www.zohoapis.com/directory/api/v2/orgs/<ORG_ID>/users?page=1&per_page=500&include=emails
   ZOHO_DIRECTORY_REFRESH_TOKEN=<the directory refresh token>
   # ZOHO_DIRECTORY_CLIENT_ID / _SECRET: leave blank (refreshed with the main app)
   ADMIN_ORG_ID=<the local organizations.id row this maps to>
   ```
   Ensure an `organizations` row exists with id = ADMIN_ORG_ID.

When both the URL and refresh token are set, the worker logs
"Zoho Directory auto-sync enabled". Response fields are mapped from the live v2
shape: `zuid`, `primary_email`/`emails[].email_id`, `full_name` (name),
`user_status` (active/inactive). There is no department field in Directory v2.

### Option B — CSV import (always works)

Leave the directory vars blank. An admin uploads `email,name,department,status`
CSV at `/admin/import` (behind OIDC login + CSRF); new active employees are
invited, inactive ones offboarded. Use this if the org is not on Zoho Directory.

## Upgrades
```bash
git pull
docker compose -f docker-compose.prod.yml build
docker compose -f docker-compose.prod.yml run --rm migrate
docker compose -f docker-compose.prod.yml up -d
```

## Backups
- Back up the `pgdata` volume (or use managed Postgres with automated backups).
  Losing the DB loses encrypted credentials; losing `CRYPTO_MASTER_KEY` makes
  stored tokens unrecoverable — store the key in a secret manager with its own
  backup/rotation.

## Hardening checklist
- [ ] `APP_ENV=production` (enables Secure cookies).
- [ ] Strong `POSTGRES_PASSWORD`; consider managed Postgres/Redis with TLS.
- [ ] `CRYPTO_MASTER_KEY` in a KMS/secret manager, not the DB or the repo.
- [ ] Restrict SSH; firewall everything except 80/443.
- [ ] Review retention values and privacy docs with legal (docs/security).
- [ ] Point security alerts (internal/security) at a real SIEM/pager.
