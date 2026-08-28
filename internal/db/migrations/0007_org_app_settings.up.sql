-- Per-org application settings entered in the UI (email/SMTP, branding, Fathom),
-- so almost everything is configured in the app rather than env. Secrets are
-- encrypted at rest (spec §13).
CREATE TABLE org_app_settings (
    org_id                    BIGINT PRIMARY KEY REFERENCES organizations(id),
    smtp_host                 TEXT,
    smtp_port                 TEXT,
    smtp_user                 TEXT,
    smtp_pass_ciphertext      TEXT,
    email_from                TEXT,
    company_name              TEXT,
    app_name                  TEXT,
    support_addr              TEXT,
    privacy_url               TEXT,
    terms_url                 TEXT,
    fathom_api_key_ciphertext TEXT,
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);
