ALTER TABLE calendars DROP COLUMN IF EXISTS is_personal;
ALTER TABLE organizations DROP COLUMN IF EXISTS recording_policy_overrides_employee;
ALTER TABLE organizations DROP COLUMN IF EXISTS recording_default;
ALTER TABLE employees DROP COLUMN IF EXISTS recording_enabled;
