-- key_version marks which event-identity scheme a mapping row was written under.
--   1 = legacy: keyed by Zoho uid only, so every occurrence of a recurring series
--       collapsed onto one row (and one Fathom event on a single, stale date).
--   2 = current: one-off events keyed by uid, recurring occurrences by
--       uid#<start UTC>. Every row the scanner (re)writes becomes version 2.
-- Legacy rows that are no longer produced by a scan (old recurring rows) stay at
-- 1; co-attendee dedup ignores them so new occurrences never reuse their
-- stale destination event, and the normal cancel pass removes them.
ALTER TABLE event_mappings ADD COLUMN key_version SMALLINT NOT NULL DEFAULT 1;
