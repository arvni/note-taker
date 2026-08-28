-- Google OAuth client (for "Connect Google Calendar") enterable in the app.
-- Secret encrypted at rest (spec §13).
ALTER TABLE org_app_settings ADD COLUMN google_oauth_client_id TEXT;
ALTER TABLE org_app_settings ADD COLUMN google_oauth_secret_ciphertext TEXT;
