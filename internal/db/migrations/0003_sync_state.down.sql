DROP INDEX IF EXISTS idx_event_mappings_emp;
ALTER TABLE event_mappings DROP COLUMN IF EXISTS cancelled_at;
ALTER TABLE event_mappings DROP COLUMN IF EXISTS last_seen_at;
ALTER TABLE event_mappings DROP COLUMN IF EXISTS destination_synced_at;
