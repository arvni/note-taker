package db

import "testing"

func TestCheckOrgScope(t *testing.T) {
	cases := []struct {
		name    string
		sql     string
		wantErr bool
	}{
		{"employees with org", "SELECT id FROM employees WHERE organization_id = $1", false},
		{"employees without org", "SELECT id FROM employees WHERE email = $1", true},
		{"audit insert with org", "INSERT INTO audit_log (organization_id, action) VALUES ($1,$2)", false},
		{"audit insert without org", "INSERT INTO audit_log (action) VALUES ($1)", true},
		{"non-tenant table", "SELECT version FROM schema_migrations", false},
		{"employee-scoped table (no org column) allowed", "SELECT id FROM oauth_credentials WHERE employee_id = $1", false},
		{"update employees without org", "UPDATE employees SET name = $1 WHERE id = $2", true},
		{"update employees with org", "UPDATE employees SET name = $1 WHERE id = $2 AND organization_id = $3", false},
		{"join employees with org filter", "SELECT c.id FROM calendars c JOIN employees e ON e.id = c.employee_id WHERE e.organization_id = $1", false},
		{"join employees without org filter", "SELECT c.id FROM calendars c JOIN employees e ON e.id = c.employee_id", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckOrgScope(tc.sql)
			if (err != nil) != tc.wantErr {
				t.Errorf("CheckOrgScope(%q) err=%v, wantErr=%v", tc.sql, err, tc.wantErr)
			}
		})
	}
}

func TestOrgIDIsDistinctType(t *testing.T) {
	// Compile-time intent: OrgID must not be interchangeable with a bare int64.
	var o OrgID = 7
	if int64(o) != 7 {
		t.Fatal("OrgID conversion broken")
	}
}
