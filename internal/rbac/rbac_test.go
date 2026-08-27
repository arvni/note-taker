package rbac

import "testing"

func TestPermissions(t *testing.T) {
	admin := Principal{Role: OrgAdmin}
	if !admin.Can(ManageEmployees) || !admin.Can(RevokeAnyEmployee) {
		t.Error("admin should manage employees and revoke")
	}
	emp := Principal{Role: Employee}
	if emp.Can(ManageEmployees) {
		t.Error("employee must not manage employees (spec §41)")
	}
	if !emp.Can(RevokeSelf) || !emp.Can(ViewOwnStatus) {
		t.Error("employee should revoke self and view own status")
	}
	auditor := Principal{Role: Auditor}
	if auditor.Can(ManageEmployees) || !auditor.Can(ViewOrgStatus) {
		t.Error("auditor is read-only org status")
	}
	if !admin.IsStaff() || emp.IsStaff() {
		t.Error("IsStaff classification wrong")
	}
}
