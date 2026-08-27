// Package rbac defines roles, the authenticated principal, and permission checks
// (spec §41). Authorization is always enforced server-side and scoped to the
// principal's organization (spec §48).
package rbac

// Role is an authorization role (spec §41).
type Role string

const (
	OrgAdmin        Role = "org_admin"        // manage org, employees, calendars, policies
	CalendarManager Role = "calendar_manager" // manage synchronization rules
	Employee        Role = "employee"         // view own status, connect/reconnect/revoke self
	Auditor         Role = "auditor"          // read-only
)

// Permission is a discrete capability.
type Permission string

const (
	ManageEmployees   Permission = "manage_employees"
	ManageCalendars   Permission = "manage_calendars"
	ManagePolicies    Permission = "manage_policies"
	ViewOrgStatus     Permission = "view_org_status"
	RevokeAnyEmployee Permission = "revoke_any_employee"
	ManageSyncRules   Permission = "manage_sync_rules"
	ViewOwnStatus     Permission = "view_own_status"
	ConnectSelf       Permission = "connect_self"
	RevokeSelf        Permission = "revoke_self"
)

var rolePerms = map[Role]map[Permission]bool{
	OrgAdmin: {
		ManageEmployees: true, ManageCalendars: true, ManagePolicies: true,
		ViewOrgStatus: true, RevokeAnyEmployee: true, ManageSyncRules: true,
	},
	CalendarManager: {
		ManageSyncRules: true, ViewOrgStatus: true, ManageCalendars: true,
	},
	Employee: {
		ViewOwnStatus: true, ConnectSelf: true, RevokeSelf: true,
	},
	Auditor: {
		ViewOrgStatus: true,
	},
}

// Principal is the authenticated actor on a request.
type Principal struct {
	OrgID      int64
	Role       Role
	EmployeeID int64  // set for Employee principals; 0 for staff roles
	Subject    string // IdP subject (OIDC sub) for admins
	Email      string
}

// Can reports whether the principal's role grants a permission (spec §41).
func (p Principal) Can(perm Permission) bool {
	return rolePerms[p.Role][perm]
}

// IsStaff reports whether the principal is an admin-type role (not an employee).
func (p Principal) IsStaff() bool {
	return p.Role == OrgAdmin || p.Role == CalendarManager || p.Role == Auditor
}
