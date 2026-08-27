package directory

import (
	"context"
	"testing"

	"github.com/arvinizadi/fathom/internal/db"
)

// fakeStore is an in-memory Store for reconciler tests.
type fakeStore struct {
	next int64
	emps map[int64]*Employee
}

func newFakeStore(seed ...Employee) *fakeStore {
	f := &fakeStore{emps: map[int64]*Employee{}}
	for _, e := range seed {
		ec := e
		if ec.ID > f.next {
			f.next = ec.ID
		}
		f.emps[ec.ID] = &ec
	}
	return f
}

func (f *fakeStore) ListByOrg(_ context.Context, _ db.OrgID) ([]Employee, error) {
	out := make([]Employee, 0, len(f.emps))
	for _, e := range f.emps {
		out = append(out, *e)
	}
	return out, nil
}
func (f *fakeStore) Create(_ context.Context, org db.OrgID, u User) (*Employee, error) {
	f.next++
	e := &Employee{ID: f.next, OrgID: org, ZohoUserID: u.ZohoUserID, Email: u.Email,
		Name: u.Name, Department: u.Department, Status: u.Status, OnboardingStatus: "not_invited"}
	f.emps[e.ID] = e
	return e, nil
}
func (f *fakeStore) UpdateProfile(_ context.Context, _ db.OrgID, id int64, name, dept string, st Status) error {
	e := f.emps[id]
	e.Name, e.Department, e.Status = name, dept, st
	return nil
}
func (f *fakeStore) UpdateEmail(_ context.Context, _ db.OrgID, id int64, email string) error {
	f.emps[id].Email = email
	return nil
}
func (f *fakeStore) SetStatus(_ context.Context, _ db.OrgID, id int64, st Status) error {
	f.emps[id].Status = st
	return nil
}

// spies
type spyInviter struct{ invited []int64 }

func (s *spyInviter) Invite(_ context.Context, _ db.OrgID, id int64) error {
	s.invited = append(s.invited, id)
	return nil
}

type spyOffboarder struct{ offboarded []int64 }

func (s *spyOffboarder) Offboard(_ context.Context, _ db.OrgID, id int64) error {
	s.offboarded = append(s.offboarded, id)
	return nil
}

const org = db.OrgID(1)

func TestReconcile_NewEmployeeCreatedAndInvited(t *testing.T) {
	store := newFakeStore()
	inv := &spyInviter{}
	rc := NewReconciler(store, inv, nil)

	rep, err := rc.Reconcile(context.Background(), org, []User{
		{Email: "new@company.com", Name: "New Hire", Status: Active},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Created != 1 {
		t.Fatalf("Created=%d, want 1", rep.Created)
	}
	if len(inv.invited) != 1 {
		t.Fatalf("expected 1 invite, got %d", len(inv.invited))
	}
}

func TestReconcile_InactiveNewEmployeeNotInvited(t *testing.T) {
	store := newFakeStore()
	inv := &spyInviter{}
	rc := NewReconciler(store, inv, nil)
	_, err := rc.Reconcile(context.Background(), org, []User{
		{Email: "ghost@company.com", Status: Inactive},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.invited) != 0 {
		t.Fatal("inactive new employee should not be invited")
	}
}

func TestReconcile_DeactivationTriggersOffboard(t *testing.T) {
	store := newFakeStore(Employee{ID: 10, OrgID: org, ZohoUserID: "z1", Email: "a@company.com", Status: Active})
	off := &spyOffboarder{}
	rc := NewReconciler(store, nil, off)

	rep, err := rc.Reconcile(context.Background(), org, []User{
		{ZohoUserID: "z1", Email: "a@company.com", Status: Inactive},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Disabled != 1 || len(off.offboarded) != 1 || off.offboarded[0] != 10 {
		t.Fatalf("expected employee 10 offboarded; rep=%+v off=%v", rep, off.offboarded)
	}
}

func TestReconcile_EmailChangeByZohoIdUpdatesInPlace(t *testing.T) {
	store := newFakeStore(Employee{ID: 5, OrgID: org, ZohoUserID: "z9", Email: "old@company.com", Status: Active})
	rc := NewReconciler(store, nil, nil)

	rep, err := rc.Reconcile(context.Background(), org, []User{
		{ZohoUserID: "z9", Email: "new@company.com", Status: Active},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Updated == 0 {
		t.Fatal("expected an in-place update for the email change")
	}
	if store.emps[5].Email != "new@company.com" {
		t.Fatalf("email not updated in place: %q", store.emps[5].Email)
	}
	if len(store.emps) != 1 {
		t.Fatalf("email change created a duplicate; have %d employees", len(store.emps))
	}
}

func TestReconcile_EmailCollisionRefusesReassign(t *testing.T) {
	// Two distinct Zoho accounts; z1 tries to take z2's email — must be refused (spec §20).
	store := newFakeStore(
		Employee{ID: 1, OrgID: org, ZohoUserID: "z1", Email: "a@company.com", Status: Active},
		Employee{ID: 2, OrgID: org, ZohoUserID: "z2", Email: "b@company.com", Status: Active},
	)
	rc := NewReconciler(store, nil, nil)
	rep, err := rc.Reconcile(context.Background(), org, []User{
		{ZohoUserID: "z1", Email: "b@company.com", Status: Active}, // collide with z2
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Conflicts) == 0 {
		t.Fatal("expected a conflict for the email collision")
	}
	if store.emps[1].Email != "a@company.com" {
		t.Fatalf("employee 1 email wrongly reassigned to %q", store.emps[1].Email)
	}
	if store.emps[2].Email != "b@company.com" {
		t.Fatal("employee 2 email was clobbered")
	}
}

func TestReconcile_ReactivationFlipsStatus(t *testing.T) {
	store := newFakeStore(Employee{ID: 3, OrgID: org, ZohoUserID: "z3", Email: "c@company.com", Status: Inactive})
	rc := NewReconciler(store, nil, nil)
	rep, err := rc.Reconcile(context.Background(), org, []User{
		{ZohoUserID: "z3", Email: "c@company.com", Status: Active},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Reactivated != 1 || store.emps[3].Status != Active {
		t.Fatalf("expected reactivation; rep=%+v status=%s", rep, store.emps[3].Status)
	}
}
