package directory

import "testing"

func TestParseDirectoryUsers(t *testing.T) {
	// Shape follows Zoho Directory's documented user record: zuid, emails[]
	// with email_id/is_primary, is_active, display_name.
	body := []byte(`{
	  "users": [
	    {
	      "zuid": "100200300",
	      "display_name": "Alice Anderson",
	      "department": "Sales",
	      "is_active": true,
	      "emails": [
	        {"email_id": "alias@company.com", "is_primary": false},
	        {"email_id": "alice@company.com", "is_primary": true}
	      ]
	    },
	    {
	      "zuid": "400500600",
	      "first_name": "Bob",
	      "last_name": "Brown",
	      "is_active": false,
	      "emails": [{"email_id": "bob@company.com", "is_primary": true}]
	    },
	    {"zuid": "700", "is_active": true, "emails": []}
	  ]
	}`)
	users, err := parseDirectoryUsers(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 { // third has no valid email, dropped
		t.Fatalf("got %d users, want 2", len(users))
	}
	if users[0].Email != "alice@company.com" {
		t.Errorf("primary email not selected: %q", users[0].Email)
	}
	if users[0].ZohoUserID != "100200300" || users[0].Name != "Alice Anderson" {
		t.Errorf("user0 mapping wrong: %+v", users[0])
	}
	if users[0].Status != Active {
		t.Errorf("user0 status = %q, want active", users[0].Status)
	}
	if users[1].Status != Inactive {
		t.Errorf("user1 status = %q, want inactive", users[1].Status)
	}
	if users[1].Name != "Bob Brown" { // falls back to first+last
		t.Errorf("user1 name fallback wrong: %q", users[1].Name)
	}
}

func TestParseDirectoryUsers_DataEnvelope(t *testing.T) {
	body := []byte(`{"data":[{"zuid":"1","is_active":true,"email":"x@company.com"}]}`)
	users, err := parseDirectoryUsers(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Email != "x@company.com" {
		t.Fatalf("data-envelope parse failed: %+v", users)
	}
}
