DROP TABLE IF EXISTS fathom_webhook;
ALTER TABLE event_mappings DROP COLUMN IF EXISTS fathom_has_summary;
ALTER TABLE event_mappings DROP COLUMN IF EXISTS fathom_has_transcript;
ALTER TABLE event_mappings DROP COLUMN IF EXISTS fathom_recorded_at;
ALTER TABLE event_mappings DROP COLUMN IF EXISTS fathom_recording_url;
ALTER TABLE event_mappings DROP COLUMN IF EXISTS fathom_recording_id;
