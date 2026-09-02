-- Carry the source event's description and location so synced destination
-- events include the full body (opt-in via SYNC_ALL_EVENTS / richer sync).
ALTER TABLE event_mappings ADD COLUMN description TEXT;
ALTER TABLE event_mappings ADD COLUMN location TEXT;
