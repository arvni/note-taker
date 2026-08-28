package directory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/arvinizadi/fathom/internal/db"
)

func TestAutoSync_FetchesAndReconciles(t *testing.T) {
	// Fake Zoho Directory returning two users (documented shape).
	var gotAuth string
	dir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]any{"users": []map[string]any{
			{"zuid": "111", "display_name": "Alice", "is_active": true,
				"emails": []map[string]any{{"email_id": "alice@company.com", "is_primary": true}}},
			{"zuid": "222", "display_name": "Bob", "is_active": false,
				"emails": []map[string]any{{"email_id": "bob@company.com", "is_primary": true}}},
		}})
	}))
	defer dir.Close()

	store := newFakeStore()
	inv := &spyInviter{}
	rc := NewReconciler(store, inv, &spyOffboarder{})
	tokenCalled := false
	as := NewAutoSync(NewZohoClient(dir.URL), rc, func(context.Context) (string, error) {
		tokenCalled = true
		return "org-access-token", nil
	}, db.OrgID(1))

	rep, err := as.SyncOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !tokenCalled {
		t.Fatal("token func not called")
	}
	if gotAuth != "Zoho-oauthtoken org-access-token" {
		t.Fatalf("directory auth header wrong: %q", gotAuth)
	}
	// Both users created; only the active one (Alice) invited.
	if rep.Created != 2 {
		t.Fatalf("created=%d, want 2", rep.Created)
	}
	if len(inv.invited) != 1 {
		t.Fatalf("invited=%d, want 1 (active only)", len(inv.invited))
	}
	// A second run with the same snapshot creates nothing new (idempotent).
	rep2, _ := as.SyncOnce(context.Background())
	if rep2.Created != 0 {
		t.Fatalf("second run created=%d, want 0", rep2.Created)
	}
}

func TestAutoSync_TokenErrorPropagates(t *testing.T) {
	store := newFakeStore()
	rc := NewReconciler(store, nil, nil)
	as := NewAutoSync(NewZohoClient("http://unused"), rc, func(context.Context) (string, error) {
		return "", context.DeadlineExceeded
	}, db.OrgID(1))
	if _, err := as.SyncOnce(context.Background()); err == nil {
		t.Fatal("expected token error to propagate (no directory call)")
	}
}
