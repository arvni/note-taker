-- Sync-state tracking on event_mappings so the engine can decide create vs
-- update vs cancel (spec §14-16 objectives, §42).

-- The source_updated_at value that was last pushed to the destination. When the
-- source's source_updated_at is newer, the destination event needs updating.
ALTER TABLE event_mappings ADD COLUMN destination_synced_at TIMESTAMPTZ;

-- Set to now() every time a scan sees the source event. A mapping with a
-- destination whose last_seen_at predates the current scan was cancelled at the
-- source and its destination event must be removed (spec §15 objective).
ALTER TABLE event_mappings ADD COLUMN last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- Set when the destination event has been cancelled/removed.
ALTER TABLE event_mappings ADD COLUMN cancelled_at TIMESTAMPTZ;

CREATE INDEX idx_event_mappings_emp ON event_mappings(employee_id);
