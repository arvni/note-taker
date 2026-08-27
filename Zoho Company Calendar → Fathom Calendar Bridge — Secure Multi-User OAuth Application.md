# Zoho Company Calendar → Fathom Calendar Bridge

## 1. Project Objective

Build a secure, production-ready multi-user calendar synchronization platform for a company using Zoho Mail and Zoho Calendar.

The company has many employees.

Each employee has a company email address such as:

```text
employee1@company.com
employee2@company.com
employee3@company.com
```

Their meetings are automatically represented in their Zoho Calendar according to the company's Zoho Calendar/Mail configuration.

The application must:

1. Obtain the company's employee/user list through an approved Zoho organization/directory mechanism.
2. Send each employee an email explaining the integration.
3. Provide each employee with a unique, secure consent link.
4. Allow the employee to authorize the application through official Zoho OAuth 2.0.
5. Obtain the employee's OAuth authorization code.
6. Exchange the authorization code server-side for an access token and refresh token.
7. Encrypt and securely store the refresh token.
8. Access only the Zoho Calendar resources permitted by the employee.
9. Discover the employee's calendars.
10. Monitor selected calendars.
11. Detect qualifying meetings.
12. Synchronize qualifying meetings into the configured Google Calendar used by Fathom.
13. Track source/destination mappings.
14. Update destination events when source events change.
15. Remove/cancel destination events when source events are cancelled.
16. Prevent duplicates.
17. Provide administrators with onboarding and synchronization status.
18. Allow employees to revoke access.
19. Automatically detect revoked/invalid OAuth credentials.
20. Minimize stored calendar data.

The application must use **official APIs only**.

Do not scrape Zoho Mail, Zoho Calendar, Zoho Directory, or Fathom.

---

# 2. Critical Authorization Principle

The company administrator must NOT receive or impersonate employee OAuth credentials.

The application must use delegated OAuth.

The correct model is:

```text
Company admin
      │
      │ identifies employee
      ▼
Application sends consent invitation
      │
      ▼
Employee clicks link
      │
      ▼
Zoho OAuth
      │
      ▼
Employee authenticates
      │
      ▼
Employee explicitly grants requested scopes
      │
      ▼
Authorization code
      │
      ▼
Backend exchanges code
      │
      ▼
Employee-specific refresh token
```

Never implement:

```text
Admin password → employee calendar
Admin access token → employee calendar
```

unless Zoho explicitly documents an organization-level authorization mechanism for the required Calendar scopes and the organization's security policy approves its use.

Zoho Calendar's documented OAuth flow is based on user consent and user-scoped tokens.

---

# 3. Employee Discovery

The application needs an employee directory synchronization component.

Preferred source:

```text
Zoho Directory / approved Zoho organization API
```

The application should retrieve:

```text
Zoho user ID
email
display name
department
status
active/inactive
```

Do not use employee passwords.

Do not scrape the Zoho admin interface.

Zoho Directory provides centralized organization user management, including user administration.

If an approved API cannot provide the required employee directory data, implement a secure CSV import fallback rather than attempting to scrape the admin interface.

Supported fallback:

```text
CSV:
email,name,department,status
```

The administrator can upload the employee list.

---

# 4. Employee Onboarding

Create an onboarding system.

Each employee has:

```text
not_invited
invited
opened
authorization_started
authorized
authorization_failed
revoked
disabled
```

Example:

```text
employee@company.com

Status:
INVITED

Invitation:
Sent: 2026-08-25 09:00
Expires: 2026-09-01 09:00
```

---

# 5. Consent Email

The application sends an email to the employee.

The email must clearly explain:

### What the application does

Example:

```text
This application connects to your company Zoho Calendar
to identify meetings that meet the company's recording policy
and synchronize qualifying meetings with the company's
Fathom recording workflow.
```

### What it can access

Explicitly list the requested permissions.

For the initial implementation, request the minimum required permissions.

Prefer:

```text
ZohoCalendar.calendar.READ
ZohoCalendar.event.READ
```

if the application only needs to read calendars/events.

Zoho documents these granular Calendar scopes.

Do not request:

```text
ZohoCalendar.event.ALL
```

unless the application genuinely needs full event access.

### What it cannot access

Clearly state that:

```text
The application does not request access to your Zoho Mail
messages or password.
```

Do not claim that it cannot access other data unless the OAuth scopes actually guarantee that.

---

# 6. Consent Link Security

Never put an OAuth token inside the email.

Never put a refresh token inside the email.

Never use:

```text
/oauth?email=employee@company.com
```

as the only authorization mechanism.

Instead create a random, high-entropy onboarding token.

Example:

```text
https://calendar-sync.company.com/connect/7c2b...random...
```

Database:

```text
onboarding_tokens
-----------------
id
employee_id
token_hash
expires_at
used_at
created_at
```

Store only a hash of the onboarding token.

Never store the raw token.

Use a cryptographically secure random generator.

Recommended minimum:

```text
32 bytes
```

preferably:

```text
48 bytes
```

or more.

---

# 7. Token Lifecycle

The onboarding token and OAuth token are completely different.

```text
Onboarding token
    ↓
used only to identify pending employee
    ↓
Zoho OAuth
    ↓
authorization code
    ↓
access token
refresh token
```

The onboarding token must expire after a short period.

Recommended:

```text
7 days
```

Make it configurable.

After successful authorization:

```text
used_at != NULL
```

and it cannot be reused.

---

# 8. OAuth Flow

Use Zoho's server-based Authorization Code flow.

Endpoints:

```text
GET /connect/:token
GET /oauth/zoho/start
GET /oauth/zoho/callback
```

The application must use:

```text
state
```

for CSRF protection.

The OAuth flow must contain:

```text
state
nonce / equivalent correlation mechanism where applicable
PKCE where supported and appropriate
```

The implementation must follow Zoho's current documentation rather than assuming PKCE is required for server-based applications.

Zoho's current Calendar documentation specifies Authorization Code for server-based applications.

---

# 9. OAuth State

Create:

```text
oauth_states
```

Fields:

```text
id
employee_id
state_hash
provider
expires_at
used_at
created_at
```

Generate state with a cryptographically secure random generator.

Never put employee IDs or email addresses directly into the state value.

The callback must verify:

```text
state exists
state hash matches
state has not expired
state has not been used
provider == zoho
employee association is valid
```

Then immediately invalidate it.

---

# 10. OAuth Redirect

Example:

```text
https://calendar-sync.company.com/oauth/zoho/callback
```

The redirect URI must be registered in the Zoho API Console.

Use HTTPS in production.

Never accept arbitrary redirect URLs from query parameters.

---

# 11. OAuth Scopes

Start with minimum privilege.

For read-only calendar monitoring:

```text
ZohoCalendar.calendar.READ
ZohoCalendar.event.READ
```

If the application later needs to create/update events in Zoho:

```text
ZohoCalendar.event.CREATE
ZohoCalendar.event.UPDATE
```

Do not request:

```text
ZohoCalendar.event.ALL
```

unless necessary.

Zoho's API documentation confirms that Calendar event permissions can be granted granularly using READ, CREATE, etc.

---

# 12. Token Exchange

After callback:

```text
authorization_code
       ↓
server
       ↓
Zoho token endpoint
       ↓
access_token
refresh_token
```

The token exchange must occur server-side.

Never exchange the authorization code in frontend JavaScript.

Never send:

```text
client_secret
```

to the browser.

Zoho explicitly states that the client secret must remain confidential and that access/refresh tokens must be protected.

---

# 13. Token Storage

Create:

```text
oauth_credentials
```

Fields:

```text
id
employee_id
provider
provider_account_id
access_token_ciphertext
refresh_token_ciphertext
access_token_expires_at
scopes
api_domain
status
last_refresh_at
last_successful_api_call_at
revoked_at
created_at
updated_at
```

Encrypt:

```text
access_token
refresh_token
```

at rest.

Use authenticated encryption such as:

```text
AES-256-GCM
```

or a managed KMS/secret-management system.

Do not store tokens as plaintext.

---

# 14. Encryption Key

Do not store the encryption key in PostgreSQL.

Use:

```text
AWS KMS
Azure Key Vault
Google Cloud KMS
HashiCorp Vault
```

or, for a smaller deployment:

```text
environment variable
```

with proper server secret management.

The preferred production architecture is a KMS/Vault.

---

# 15. Refresh Tokens

Zoho Calendar access tokens are valid for approximately one hour.

Refresh tokens are long-lived and remain valid until revoked under the documented conditions.

The application must:

```text
if access_token expires soon:
    refresh access token
```

Do not request a new refresh token unnecessarily.

Zoho specifically recommends not repeatedly generating refresh tokens with repeated consent because refresh tokens have long lifetimes.

---

# 16. Token Refresh Lock

Multiple workers may attempt to refresh the same employee token simultaneously.

Implement a distributed lock:

```text
zoho-token-refresh:{employee_id}
```

using Redis.

Only one worker may refresh a credential at a time.

---

# 17. Token Revocation

Support:

```text
POST /api/employees/:id/revoke
```

When access is revoked:

1. Mark credential as revoked.
2. Revoke the Zoho refresh token if appropriate.
3. Stop synchronization jobs.
4. Keep audit records.
5. Delete encrypted tokens after the organization's retention policy permits.

Zoho documents a token revocation endpoint and recommends revoking refresh tokens when no longer required or when compromised.

---

# 18. Detecting Revocation

If Zoho returns an authentication error indicating the refresh token is invalid:

```text
status = revoked
```

Do not continuously retry.

Create an onboarding action:

```text
Reconnect Zoho Calendar
```

and send the employee an email asking them to reconnect.

---

# 19. Employee Offboarding

This is mandatory.

When the employee becomes inactive in the company directory:

```text
employee → inactive
```

the application must:

1. Stop calendar synchronization.
2. Cancel queued jobs.
3. Revoke OAuth access if appropriate.
4. Remove stored tokens.
5. Mark employee disconnected.
6. Preserve minimal audit information.
7. Do not continue accessing the employee's calendar.

---

# 20. Employee Directory Synchronization

Run periodically:

```text
every 1 hour
```

or use an official directory webhook if available.

Process:

```text
Zoho Directory
      ↓
compare users
      ↓
new user?
      ↓
create employee record
      ↓
send onboarding email
```

For existing users:

```text
active → continue
inactive → disable
email changed → update carefully
```

Never automatically reassign an OAuth credential to another email address.

The OAuth credential must remain bound to the original Zoho account identifier.

---

# 21. Email Sending

Create an email service.

Prefer the company's authorized Zoho Mail sending mechanism or another approved transactional email provider.

Do not use employee credentials.

The application should send from a dedicated address:

```text
calendar-integration@company.com
```

or similar.

Do not send onboarding emails from arbitrary employee accounts.

---

# 22. Email Security

Every onboarding email must contain:

```text
purpose
requested permissions
privacy notice
authorization button
expiration
support contact
```

Do not put sensitive information in the URL.

Do not put:

```text
access_token
refresh_token
authorization_code
```

in the email.

---

# 23. Anti-Phishing Design

The email must clearly identify the application.

Use:

```text
https://calendar-sync.company.com
```

with the company's verified TLS certificate.

Do not use:

```text
tinyurl
bit.ly
IP-address links
random subdomains
```

The consent page must display:

```text
Company name
Application name
Employee email
Requested permissions
Privacy policy
Terms
```

before redirecting to Zoho.

---

# 24. Authorization Confirmation

After OAuth succeeds:

```text
Zoho authorization successful
```

Display:

```text
Account:
employee@company.com

Permissions:
✓ Calendar metadata
✓ Calendar events

Status:
Connected
```

Never display access or refresh tokens.

---

# 25. Account Binding

Do not trust the email address supplied by the browser.

After OAuth:

1. Obtain the authenticated Zoho user's identity from an official Zoho endpoint/API.
2. Verify the identity.
3. Compare the returned Zoho user/account identity against the employee record.
4. Only then attach the OAuth credential.

This prevents:

```text
employee A onboarding token
        +
employee B Zoho account
        =
incorrect credential association
```

If the identities do not match:

```text
authorization_failed
```

and require administrator review.

---

# 26. Email Domain Validation

Do not rely solely on:

```text
email.endsWith("@company.com")
```

Use the employee record created from the company's authoritative directory.

For example:

```text
employee_id = 123
expected_zoho_account_id = abc
```

The OAuth identity must match the expected account.

---

# 27. Calendar Discovery

After successful OAuth:

```text
GET /calendars
```

Discover the calendars available to that user.

Zoho Calendar provides APIs for listing calendars and retrieving individual calendar details.

Store:

```text
calendar_id
calendar_name
calendar_type
timezone
owner
```

Do not automatically synchronize every calendar.

---

# 28. Default Calendar Policy

Initial policy:

```text
Monitor only company/work calendars.
```

The employee/admin should be able to select calendars.

Default:

```text
personal calendars = disabled
```

This is a major privacy requirement.

---

# 29. Meeting Detection

For enabled calendars:

```text
GET /calendars/{calendar_uid}/events
```

Zoho Calendar supports retrieving event lists with the `ZohoCalendar.event.READ` scope.

Detect:

```text
Google Meet
Zoom
Microsoft Teams
```

from:

```text
location
description
conference information
```

Do not store unrelated event information unnecessarily.

---

# 30. Privacy Filtering

Before storing an event, apply:

```text
calendar policy
meeting policy
employee policy
organization policy
```

Example:

```text
Personal calendar → ignore

Internal HR meeting → ignore

Private event → ignore

Client Google Meet → synchronize
```

---

# 31. Employee-Level Settings

Allow:

```text
recording_enabled
```

and:

```text
allowed_calendar_ids
```

However, company policy may override employee settings where legally and organizationally appropriate.

The application must clearly distinguish:

```text
organization policy
employee preference
```

---

# 32. Data Minimization

For calendar synchronization, store only:

```text
source event ID
calendar ID
title
start
end
meeting provider
meeting URL
source updated timestamp
destination mapping
```

Avoid storing:

```text
full description
attachments
email body
private notes
unnecessary attendee information
```

unless required.

---

# 33. No Email Content Access

The application should NOT request Zoho Mail scopes.

The application does not need to read:

```text
Inbox
Sent
email body
attachments
mail history
```

The company employee directory should provide the employee email addresses.

Calendar access should remain separate from mail access.

---

# 34. Database Model

Add:

```text
employees
```

Fields:

```text
id
organization_id
zoho_user_id
email
name
department
status
onboarding_status
created_at
updated_at
```

Add:

```text
onboarding_tokens
```

Fields:

```text
id
employee_id
token_hash
expires_at
used_at
created_at
```

Add:

```text
oauth_states
```

Fields:

```text
id
employee_id
provider
state_hash
expires_at
used_at
created_at
```

Add:

```text
oauth_credentials
```

with encrypted token fields.

---

# 35. Audit Log

Every security-sensitive action must be audited.

Examples:

```text
employee_invitation_sent
employee_invitation_opened
oauth_started
oauth_completed
oauth_failed
token_refreshed
token_refresh_failed
calendar_connected
calendar_disconnected
calendar_enabled
calendar_disabled
meeting_synchronized
employee_revoked
employee_disabled
admin_reconnected
```

Never put token values into audit logs.

---

# 36. Security Events

Create security alerts for:

```text
multiple failed OAuth attempts
OAuth identity mismatch
unexpected account identity
repeated token refresh failures
suspicious onboarding activity
unusual API request volume
```

---

# 37. Rate Limiting

Rate-limit:

```text
/connect/*
/oauth/*
/api/*
```

Especially:

```text
OAuth start
OAuth callback
onboarding token validation
```

Use Redis.

Example:

```text
OAuth callback:
10 attempts / IP / 10 minutes
```

Use sensible configurable limits rather than hardcoded production values.

---

# 38. Session Security

Use:

```text
Secure cookies
HttpOnly cookies
SameSite=Lax or Strict where compatible
short session lifetime
session rotation
CSRF protection
```

Admin authentication should support MFA through the organization's identity provider if possible.

---

# 39. Admin Authentication

Do not create another password database unless necessary.

Prefer:

```text
OIDC / SSO
```

for administrators.

Potential providers:

```text
Zoho Directory SSO
Microsoft Entra ID
Google Workspace
```

The exact implementation should be chosen based on the company's existing identity provider.

---

# 40. Employee Authentication

Employees should authenticate directly through Zoho OAuth.

Do not create a second employee password.

---

# 41. Authorization

Separate:

```text
organization administrator
calendar administrator
employee
read-only auditor
```

Example permissions:

```text
Admin:
manage organization
manage employees
manage calendars
manage policies
view synchronization status

Calendar Manager:
manage synchronization rules

Employee:
view own integration status
connect/reconnect calendar
revoke own access
```

---

# 42. Fathom Destination

After the Zoho source event passes filtering:

```text
Zoho Calendar
      ↓
Synchronization engine
      ↓
Google Calendar destination
      ↓
Fathom-supported calendar detection
```

Do not assume that merely creating a Google Calendar event guarantees Fathom will record it.

The system must have an integration test confirming:

```text
destination event created
        ↓
Fathom detects meeting
        ↓
Fathom records meeting
```

---

# 43. Fathom API

Use only currently documented official Fathom APIs.

The Fathom integration must not depend on undocumented endpoints.

Support post-meeting processing where officially available:

```text
meeting
transcript
summary
action items
webhooks
```

Keep Fathom code isolated from Zoho code.

---

# 44. Complete Onboarding Flow

Implement:

```text
1. Admin opens dashboard

2. Application synchronizes employee directory

3. New employees detected

4. Application creates employee records

5. Application creates random onboarding token

6. Hash token

7. Store hash

8. Send email

9. Employee clicks email

10. Validate token

11. Display consent page

12. Employee clicks "Connect Zoho Calendar"

13. Generate OAuth state

14. Redirect to Zoho OAuth

15. Employee authenticates

16. Employee grants requested permissions

17. Zoho redirects to callback

18. Validate state

19. Exchange authorization code server-side

20. Retrieve authenticated Zoho identity

21. Verify identity against employee

22. Encrypt tokens

23. Store credentials

24. Mark employee authorized

25. Discover calendars

26. Apply organization policy

27. Enable selected calendars

28. Start synchronization
```

---

# 45. Reauthorization

If an employee disconnects:

```text
DISCONNECTED
```

Send:

```text
Reconnect your Zoho Calendar
```

The employee must repeat OAuth consent.

Do not attempt to reuse invalid credentials.

---

# 46. Company-Level Dashboard

Show:

```text
Employees
------------------------------
Total: 120

Connected: 98
Pending: 15
Revoked: 4
Failed: 3
```

Calendar status:

```text
Employee
Email
Zoho status
Calendars
Last sync
Meetings synchronized
Last error
```

---

# 47. Employee Dashboard

Employee sees only their own data:

```text
Zoho Calendar
Connected ✓

Calendars:
✓ Work Calendar
✗ Personal Calendar

Last synchronization:
2026-08-25 13:00

Status:
Healthy
```

Buttons:

```text
Reconnect
Disconnect
Manage calendars
```

---

# 48. Security Threat Model

Document and mitigate:

### Threat: OAuth CSRF

Mitigation:

```text
state
single-use state
short expiration
```

### Threat: Token theft

Mitigation:

```text
encryption at rest
KMS
no logging
no frontend exposure
```

### Threat: Employee identity substitution

Mitigation:

```text
OAuth identity verification
employee ID binding
Zoho account ID verification
```

### Threat: Onboarding link theft

Mitigation:

```text
high entropy token
short expiry
single use
hash in database
```

### Threat: Duplicate meetings

Mitigation:

```text
event mappings
idempotency
unique constraints
```

### Threat: Privilege escalation

Mitigation:

```text
RBAC
organization_id enforcement
server-side authorization
```

### Threat: Calendar privacy leakage

Mitigation:

```text
minimum scopes
calendar allowlist
data minimization
no Mail scopes
```

### Threat: OAuth credential exposure

Mitigation:

```text
HTTPS
secret manager
encryption
secure logs
rotation/revocation
```

### Threat: Compromised application server

Mitigation:

```text
KMS
least privilege
network isolation
short-lived access tokens
audit logging
host hardening
```

---

# 49. Required Security Documentation

Create:

```text
docs/security/
├── threat-model.md
├── oauth-security.md
├── token-storage.md
├── employee-onboarding.md
├── data-retention.md
├── access-control.md
├── incident-response.md
└── offboarding.md
```

---

# 50. Data Retention

Make retention configurable.

Example:

```text
OAuth audit logs:
365 days

Calendar event metadata:
90 days

Synchronization mappings:
365 days

Fathom transcript:
according to company policy
```

Do not assume these values are legally appropriate.

Make them configurable and document them.

---

# 51. GDPR / Privacy Considerations

The system may process:

```text
employee identity
meeting metadata
attendee information
meeting URLs
meeting transcripts
```

Therefore the documentation must include:

```text
purpose limitation
data minimization
retention
access control
revocation
deletion
auditability
```

If the company operates under GDPR or another privacy regime, have the company's legal/privacy team approve the processing model.

Do not claim that OAuth authorization alone makes the processing legally compliant.

---

# 52. Employee Consent vs Company Policy

The application must distinguish:

```text
OAuth technical authorization
```

from:

```text
company recording/monitoring policy
```

The employee's OAuth consent gives the application technical permission to access their calendar.

It does not by itself establish the company's legal basis for recording meetings.

The application should therefore provide:

```text
Privacy Notice
Recording Policy
Data Retention Policy
Contact/Support information
```

according to company requirements.

---

# 53. OAuth Scope Review

Before production:

1. List every requested scope.
2. Explain why it is needed.
3. Remove unused scopes.
4. Test with read-only scopes.
5. Request write permissions only if required.
6. Document the final scope set.

Initial target:

```text
ZohoCalendar.calendar.READ
ZohoCalendar.event.READ
```

---

# 54. Important Zoho Limitation

Do not assume that having an administrator account automatically gives this application access to every employee's personal calendar.

The implementation must verify whether the company's specific Zoho organization/account configuration supports an approved organization-level authorization model for Calendar.

If it does not:

```text
each employee must authorize the application
```

and the application should use the per-user OAuth architecture described above.

Zoho's standard Calendar OAuth documentation explicitly describes user consent and user-scoped access.

---

# 55. Directory Synchronization vs Calendar Authorization

These are two separate permissions.

### Directory access

Used to determine:

```text
Who works here?
What is their email?
Are they active?
```

### Calendar OAuth

Used to determine:

```text
What calendars can this specific employee authorize?
What events can the application access?
```

Never treat directory access as equivalent to calendar authorization.

---

# 56. Recommended Architecture

```text
                    ┌───────────────────────┐
                    │    Zoho Directory     │
                    │                       │
                    │ Employees / Status    │
                    └───────────┬───────────┘
                                │
                         User discovery
                                │
                                ▼
                    ┌───────────────────────┐
                    │   Calendar Bridge     │
                    │                       │
                    │ Go                    │
                    │ PostgreSQL            │
                    │ Redis                 │
                    └───────────┬───────────┘
                                │
                      Consent invitation
                                │
                                ▼
                    ┌───────────────────────┐
                    │      Employee         │
                    │                       │
                    │ Email → OAuth link    │
                    └───────────┬───────────┘
                                │
                         OAuth consent
                                │
                                ▼
                    ┌───────────────────────┐
                    │    Zoho Accounts      │
                    └───────────┬───────────┘
                                │
                     access + refresh token
                                │
                                ▼
                    ┌───────────────────────┐
                    │ Encrypted Credential  │
                    │       Storage         │
                    └───────────┬───────────┘
                                │
                                ▼
                    ┌───────────────────────┐
                    │   Zoho Calendar API   │
                    └───────────┬───────────┘
                                │
                           Events
                                │
                                ▼
                    ┌───────────────────────┐
                    │   Sync / Rule Engine  │
                    └───────────┬───────────┘
                                │
                                ▼
                    ┌───────────────────────┐
                    │ Google Calendar       │
                    │ Fathom destination    │
                    └───────────┬───────────┘
                                │
                                ▼
                             Fathom
```

---

# 57. Final Security Requirements

The coding agent MUST NOT:

- store passwords
- request Zoho Mail access unnecessarily
- expose OAuth tokens to frontend JavaScript
- place tokens in email links
- log tokens
- store plaintext refresh tokens
- trust email addresses supplied by the browser
- assume admin access equals employee calendar access
- automatically access personal calendars
- use undocumented Zoho APIs
- scrape Zoho
- use undocumented Fathom APIs
- bypass employee consent
- reuse onboarding tokens
- accept arbitrary OAuth redirect URIs
- allow cross-organization data access

The coding agent MUST:

- use OAuth 2.0
- use minimum scopes
- use HTTPS
- validate OAuth state
- verify the authenticated Zoho identity
- encrypt refresh tokens
- use secure secret management
- implement token refresh locking
- implement revocation
- implement employee offboarding
- maintain audit logs
- implement tenant isolation
- implement rate limiting
- implement idempotency
- implement data minimization
- document the threat model
- test authorization failures
- test token revocation
- test employee offboarding
- test identity mismatch
- test duplicate onboarding
- test duplicate event synchronization

---

# 58. Development First Step

Before writing the full application, create a **Zoho integration proof of concept**.

The POC must perform only:

```text
1. OAuth
2. Obtain user identity
3. Obtain calendar READ permission
4. List calendars
5. List events
6. Detect meeting URL
7. Refresh access token
8. Revoke token
```

Do not build the complete synchronization engine until this POC successfully works with a real employee test account.

After that, implement:

```text
Directory → Employee onboarding → OAuth → Calendar Sync → Google/Fathom
```

This prevents building the system around an incorrect assumption about Zoho's organization-level permissions.