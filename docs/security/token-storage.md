# Token Storage

## At rest (spec §13-14)

Access and refresh tokens are encrypted with **AES-256-GCM** (authenticated
encryption) before insertion into `oauth_credentials`. Plaintext never touches the
database and is never logged.

- Implementation: `internal/crypto` (envelope cipher), `internal/tokens/store.go`.
- The 32-byte master key is supplied from a secret manager / KMS / env secret and
  **must not** live in Postgres (spec §14). Production: AWS KMS, Azure Key Vault,
  Google Cloud KMS, or HashiCorp Vault.

## In transit

- All external calls use HTTPS. The client secret is sent only server-to-server to
  Zoho's token endpoint, never to the browser (spec §12).

## Never

- Plaintext refresh tokens at rest, tokens in logs (enforced by `internal/audit`
  redaction), tokens in emails or URLs, tokens exposed to frontend JS.

## Refresh & lifetime (spec §15-16)

- Access tokens (~1h) are refreshed only near expiry, under a per-employee Redis
  lock `zoho-token-refresh:{id}` so concurrent workers do not duplicate refreshes.
- Refresh tokens are long-lived; the app does not needlessly mint new ones.

## Deletion

- Revocation retains encrypted tokens until the retention window permits deletion
  (spec §17); offboarding deletes them immediately (spec §19).
