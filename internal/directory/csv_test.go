package directory

import (
	"strings"
	"testing"
)

func TestParseCSV(t *testing.T) {
	in := `email,name,department,status
Alice@Company.com , Alice A , Sales , active
bob@company.com,Bob B,Engineering,inactive
not-an-email,Carol,HR,active
dave@company.com,Dave D,,`
	users, rowErrs, err := ParseCSV(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 3 {
		t.Fatalf("got %d users, want 3", len(users))
	}
	if len(rowErrs) != 1 {
		t.Fatalf("got %d row errors, want 1 (the bad email)", len(rowErrs))
	}
	if users[0].Email != "alice@company.com" || users[0].Name != "Alice A" {
		t.Errorf("row 0 not normalized: %+v", users[0])
	}
	if users[0].Status != Active {
		t.Errorf("row 0 status = %q, want active", users[0].Status)
	}
	if users[1].Status != Inactive {
		t.Errorf("row 1 status = %q, want inactive", users[1].Status)
	}
	if users[2].Status != Active { // empty status defaults to active
		t.Errorf("row dave status = %q, want active (default)", users[2].Status)
	}
}

func TestParseCSV_ColumnOrderIndependent(t *testing.T) {
	in := "status,department,name,email\nactive,Ops,Eve,eve@company.com\n"
	users, _, err := ParseCSV(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Email != "eve@company.com" {
		t.Fatalf("column-order-independent parse failed: %+v", users)
	}
}

func TestParseCSV_MissingEmailColumn(t *testing.T) {
	in := "name,department\nAlice,Sales\n"
	if _, _, err := ParseCSV(strings.NewReader(in)); err == nil {
		t.Fatal("expected error for missing email column")
	}
}
