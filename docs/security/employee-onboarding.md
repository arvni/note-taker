# Employee Onboarding

Flow (spec §44), implemented across `internal/directory`, `internal/onboarding`,
`internal/oauth`, `internal/httpx`.

1. Directory sync discovers employees (Zoho Directory or CSV fallback).
2. A new active employee is created and an onboarding invitation is queued.
3. A 48-byte onboarding token is generated; only its SHA-256 hash is stored
   (`onboarding_tokens`), with a 7-day (configurable) expiry, single-use.
4. A consent email is sent from the dedicated sender with a `/connect/<token>`
   link — no token/secret in a query string, no OAuth artifacts in the email.
5. `/connect/{token}` validates the token (without consuming) and renders the
   consent page: company, app, employee email, requested permissions, privacy.
6. `/oauth/zoho/start` consumes the single-use token, creates a CSRF `state`, and
   redirects to Zoho using only the registered redirect URI.
7. Zoho authenticates the employee and they grant the minimum scopes.
8. `/oauth/zoho/callback` validates `state`, exchanges the code server-side,
   fetches and **verifies the Zoho identity against the employee record**, then
   encrypts and stores the credential and marks the employee authorized.

## Security properties

- Onboarding token: high entropy, hash-only, single-use, short expiry.
- OAuth state: hash-only, single-use, short expiry, no PII in the value.
- Identity binding: mismatch → `authorization_failed`, no credential stored,
  security alert raised.
- Reauthorization (spec §45): a disconnected employee is re-invited (a fresh
  consent link); invalid credentials are never reused.
