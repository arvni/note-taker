-- Stored OAuth credential for the destination Google Calendar (connected by an
-- admin via the "Connect Google Calendar" flow). One per organization. The
-- refresh token is encrypted at rest (spec §13).
CREATE TABLE google_destination (
    org_id                    BIGINT PRIMARY KEY REFERENCES organizations(id),
    refresh_token_ciphertext  TEXT NOT NULL,
    calendar_id               TEXT NOT NULL DEFAULT 'primary',
    connected_email           TEXT,
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);
