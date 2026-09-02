-- Calendar-sync cadence, configurable in the app Settings UI (e.g. "5m").
ALTER TABLE org_app_settings ADD COLUMN sync_interval TEXT;
