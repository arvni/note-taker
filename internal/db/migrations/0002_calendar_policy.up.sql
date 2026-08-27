-- Employee-level and org-level policy settings (spec §28, §31).

-- Employee recording preference (spec §31).
ALTER TABLE employees ADD COLUMN recording_enabled BOOLEAN NOT NULL DEFAULT true;

-- Organization-level recording default that may override employee preference
-- where organizationally appropriate (spec §31).
ALTER TABLE organizations ADD COLUMN recording_default BOOLEAN NOT NULL DEFAULT true;
-- When true, org policy overrides the employee preference (spec §31).
ALTER TABLE organizations ADD COLUMN recording_policy_overrides_employee BOOLEAN NOT NULL DEFAULT false;

-- Classify discovered calendars so personal ones stay off by default (spec §28).
ALTER TABLE calendars ADD COLUMN is_personal BOOLEAN NOT NULL DEFAULT false;
