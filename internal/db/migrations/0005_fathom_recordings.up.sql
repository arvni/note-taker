-- Fathom recording info linked back to each synced meeting (spec §42-43), plus
-- the registered webhook per org.

ALTER TABLE event_mappings ADD COLUMN fathom_recording_id   TEXT;
ALTER TABLE event_mappings ADD COLUMN fathom_recording_url  TEXT;
ALTER TABLE event_mappings ADD COLUMN fathom_recorded_at    TIMESTAMPTZ;
ALTER TABLE event_mappings ADD COLUMN fathom_has_transcript BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE event_mappings ADD COLUMN fathom_has_summary    BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE fathom_webhook (
    org_id          BIGINT PRIMARY KEY REFERENCES organizations(id),
    webhook_id      TEXT NOT NULL,
    destination_url TEXT NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
