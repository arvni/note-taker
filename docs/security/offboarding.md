# Offboarding

When an employee becomes inactive in the directory (spec §19), the reconciler
invokes `tokens.Offboarder.Offboard`, which:

1. Stops calendar sync (implicit — a disabled employee is not synced).
2. Cancels queued jobs (implicit — no job runs for a disabled employee).
3. Revokes OAuth access at Zoho (best-effort).
4. **Removes the stored (encrypted) tokens** — deletes the `oauth_credentials` row.
5. Marks the employee `disabled`.
6. Preserves minimal audit information (`employee_disabled`).
7. Stops accessing the calendar (no credential remains).

The operation is idempotent. This differs from **revocation** (spec §17), which
retains encrypted tokens until the retention window permits deletion; offboarding
deletes immediately.
