-- Per-organization Zoho OAuth configuration, entered by an admin in the app so a
-- single deployment can serve any Zoho org (spec §8-12). The client secret is
-- encrypted at rest (spec §13).
CREATE TABLE org_zoho_settings (
    org_id                   BIGINT PRIMARY KEY REFERENCES organizations(id),
    client_id                TEXT NOT NULL,
    client_secret_ciphertext TEXT NOT NULL,
    accounts_base            TEXT NOT NULL DEFAULT 'https://accounts.zoho.com',
    calendar_base            TEXT NOT NULL DEFAULT 'https://calendar.zoho.com/api/v1',
    scopes                   TEXT NOT NULL DEFAULT 'ZohoCalendar.calendar.READ,ZohoCalendar.event.READ,aaaserver.profile.READ',
    directory_users_url      TEXT,
    directory_refresh_ciphertext TEXT,
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);
