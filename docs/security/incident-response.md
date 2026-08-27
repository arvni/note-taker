# Incident Response

## Detection (spec §36)

`internal/security` raises alerts on configurable thresholds for: repeated failed
OAuth attempts, OAuth identity mismatches, repeated token-refresh failures,
suspicious onboarding activity, and unusual request volume. Alerts go to the
configured `Alerter` (wire to a SIEM/pager in production). The audit log
(`audit_log`) is the forensic record; it never contains token values.

## Response playbook

1. **Suspected token compromise** — revoke the affected employee(s)
   (`POST /api/employees/:id/revoke`), which revokes at Zoho and marks the
   credential revoked; rotate the crypto master key if key compromise is suspected
   (re-encryption required).
2. **Identity mismatch alert** — investigate the employee/Zoho account pairing; the
   credential was not stored (fail-closed). Confirm directory data integrity.
3. **Server compromise** — revoke KMS access, rotate the master key and all
   secrets, force re-authorization of employees, review audit logs.

## Contacts & escalation

Populate with the company's security team, on-call rotation, and Zoho/Google/
Fathom support contacts.
