// Package directory discovers company employees from Zoho Directory (or a CSV
// fallback) and reconciles them into tenant-scoped employee records
// (spec §3, §20, §55). Directory access is kept conceptually separate from
// calendar authorization (spec §55).
package directory

import "strings"

// Status is an employee's directory activity state (spec §3).
type Status string

const (
	Active   Status = "active"
	Inactive Status = "inactive"
)

// User is a normalized directory record from any source (Zoho or CSV). It
// carries only directory fields — never passwords (spec §3).
type User struct {
	ZohoUserID string // authoritative account identifier (spec §26); may be empty for CSV
	Email      string
	Name       string
	Department string
	Status     Status
}

// Normalize lowercases/trims the email and defaults an unknown status to active.
func (u User) Normalize() User {
	u.Email = strings.ToLower(strings.TrimSpace(u.Email))
	u.Name = strings.TrimSpace(u.Name)
	u.Department = strings.TrimSpace(u.Department)
	u.ZohoUserID = strings.TrimSpace(u.ZohoUserID)
	switch Status(strings.ToLower(string(u.Status))) {
	case Inactive:
		u.Status = Inactive
	default:
		u.Status = Active
	}
	return u
}

// Valid reports whether the record has the minimum required field (email).
func (u User) Valid() bool {
	return u.Email != "" && strings.Contains(u.Email, "@")
}
