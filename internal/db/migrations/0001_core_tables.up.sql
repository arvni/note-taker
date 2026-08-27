-- Core data model (spec §34). Every tenant-scoped table carries organization_id
-- and queries MUST filter on it (spec §48 privilege-escalation mitigation).

CREATE TABLE organizations (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Employees discovered from the company directory (spec §3, §34).
CREATE TABLE employees (
    id                BIGSERIAL PRIMARY KEY,
    organization_id   BIGINT NOT NULL REFERENCES organizations(id),
    zoho_user_id      TEXT,                 -- authoritative account binding (spec §26)
    email             TEXT NOT NULL,
    name              TEXT,
    department        TEXT,
    status            TEXT NOT NULL DEFAULT 'active',  -- active|inactive
    onboarding_status TEXT NOT NULL DEFAULT 'not_invited',
        -- not_invited|invited|opened|authorization_started|authorized|
        -- authorization_failed|revoked|disabled (spec §4)
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, email)
);
CREATE INDEX idx_employees_org ON employees(organization_id);
CREATE INDEX idx_employees_zoho ON employees(organization_id, zoho_user_id);

-- Onboarding tokens: store HASH ONLY, single-use, short expiry (spec §6-7).
CREATE TABLE onboarding_tokens (
    id          BIGSERIAL PRIMARY KEY,
    employee_id BIGINT NOT NULL REFERENCES employees(id),
    token_hash  BYTEA NOT NULL,          -- never store raw token (spec §6)
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,             -- single-use (spec §7)
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (token_hash)
);
CREATE INDEX idx_onboarding_tokens_emp ON onboarding_tokens(employee_id);

-- OAuth CSRF state: hash only, single-use, no PII embedded in value (spec §9).
CREATE TABLE oauth_states (
    id          BIGSERIAL PRIMARY KEY,
    employee_id BIGINT NOT NULL REFERENCES employees(id),
    provider    TEXT NOT NULL DEFAULT 'zoho',
    state_hash  BYTEA NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (state_hash)
);
CREATE INDEX idx_oauth_states_emp ON oauth_states(employee_id);

-- OAuth credentials: access + refresh tokens encrypted at rest (spec §13).
CREATE TABLE oauth_credentials (
    id                          BIGSERIAL PRIMARY KEY,
    employee_id                 BIGINT NOT NULL REFERENCES employees(id),
    provider                    TEXT NOT NULL DEFAULT 'zoho',
    provider_account_id         TEXT,            -- Zoho account id (binding, spec §25)
    access_token_ciphertext     TEXT NOT NULL,   -- AES-256-GCM (spec §13)
    refresh_token_ciphertext    TEXT NOT NULL,
    access_token_expires_at     TIMESTAMPTZ,
    scopes                      TEXT[],
    api_domain                  TEXT,
    status                      TEXT NOT NULL DEFAULT 'active', -- active|revoked|invalid
    last_refresh_at             TIMESTAMPTZ,
    last_successful_api_call_at TIMESTAMPTZ,
    revoked_at                  TIMESTAMPTZ,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (employee_id, provider)
);

-- Discovered calendars; personal calendars disabled by default (spec §27-28).
CREATE TABLE calendars (
    id             BIGSERIAL PRIMARY KEY,
    employee_id    BIGINT NOT NULL REFERENCES employees(id),
    calendar_uid   TEXT NOT NULL,
    name           TEXT,
    calendar_type  TEXT,
    timezone       TEXT,
    owner          TEXT,
    enabled        BOOLEAN NOT NULL DEFAULT false,  -- opt-in only (spec §28)
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (employee_id, calendar_uid)
);

-- Source→destination event mappings; data-minimized (spec §32), idempotent (spec §48).
CREATE TABLE event_mappings (
    id                    BIGSERIAL PRIMARY KEY,
    employee_id           BIGINT NOT NULL REFERENCES employees(id),
    calendar_uid          TEXT NOT NULL,
    source_event_id       TEXT NOT NULL,
    title                 TEXT,
    starts_at             TIMESTAMPTZ,
    ends_at               TIMESTAMPTZ,
    meeting_provider      TEXT,
    meeting_url           TEXT,
    source_updated_at     TIMESTAMPTZ,
    destination_event_id  TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (employee_id, source_event_id)   -- prevents duplicates (spec §48)
);

-- Audit log; MUST NEVER contain token values (spec §35).
CREATE TABLE audit_log (
    id              BIGSERIAL PRIMARY KEY,
    organization_id BIGINT REFERENCES organizations(id),
    employee_id     BIGINT REFERENCES employees(id),
    actor           TEXT,
    action          TEXT NOT NULL,
    metadata        JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_audit_org_time ON audit_log(organization_id, created_at);
