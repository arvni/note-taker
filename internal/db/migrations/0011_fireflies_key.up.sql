-- Fireflies.ai API key (encrypted), for the Fireflies notetaker integration.
ALTER TABLE org_app_settings ADD COLUMN fireflies_api_key_ciphertext TEXT;
