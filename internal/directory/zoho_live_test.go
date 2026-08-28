package directory

import "testing"

// Real Zoho Directory v2 response captured from a live org (2026-08-28).
func TestParseDirectoryUsers_LiveV2Shape(t *testing.T) {
	body := []byte(`{"status_code":200,"resource_name":"users","users":[
	  {"zuid":"839460663","first_name":"Saif","last_name":"Albusaidi","full_name":"Saif Albusaidi",
	   "display_name":"Chief Purchasing Officer","user_status":"active","user_type":"confirmed",
	   "primary_email":"saif.albusaidi@biongenetic.com",
	   "emails":[{"email_id":"saif.albusaidi@biongenetic.com","is_primary":true,"is_verified":true}]},
	  {"zuid":"859662510","first_name":"PGT","last_name":"","full_name":"PGT","display_name":"PGT",
	   "user_status":"active","primary_email":"pgt@biongenetic.com",
	   "emails":[{"email_id":"pgt@biongenetic.com","is_primary":true}]},
	  {"zuid":"111","first_name":"Former","last_name":"Employee","full_name":"Former Employee",
	   "user_status":"inactive","primary_email":"gone@biongenetic.com",
	   "emails":[{"email_id":"gone@biongenetic.com","is_primary":true}]}
	]}`)
	users, err := parseDirectoryUsers(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 3 {
		t.Fatalf("got %d users, want 3", len(users))
	}
	u := users[0]
	if u.ZohoUserID != "839460663" {
		t.Errorf("zuid: %q", u.ZohoUserID)
	}
	if u.Email != "saif.albusaidi@biongenetic.com" {
		t.Errorf("email: %q", u.Email)
	}
	// full_name is the person's name; display_name is a job title and must NOT win.
	if u.Name != "Saif Albusaidi" {
		t.Errorf("name: %q, want 'Saif Albusaidi' (not the job title)", u.Name)
	}
	if u.Status != Active {
		t.Errorf("status: %q, want active", u.Status)
	}
	// user_status:"inactive" must map to Inactive (was the is_active-bool bug).
	if users[2].Status != Inactive {
		t.Errorf("inactive user status: %q, want inactive", users[2].Status)
	}
}
