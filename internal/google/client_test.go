package google

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGoogleClient_CRUD(t *testing.T) {
	var lastMethod, lastPath string
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastMethod, lastPath = r.Method, r.URL.Path
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		switch r.Method {
		case http.MethodPost:
			json.NewEncoder(w).Encode(map[string]any{"id": "gid-123"})
		case http.MethodPut:
			json.NewEncoder(w).Encode(map[string]any{"id": "gid-123"})
		case http.MethodDelete:
			deleted = true
			w.WriteHeader(204)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "cal@group.calendar.google.com", StaticToken("tok"))
	ev := Event{Summary: "Client call", Location: "https://meet.google.com/x", Start: time.Now(), End: time.Now().Add(time.Hour)}

	id, err := c.CreateEvent(context.Background(), ev)
	if err != nil || id != "gid-123" {
		t.Fatalf("create: id=%q err=%v", id, err)
	}
	if lastMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", lastMethod)
	}
	if err := c.UpdateEvent(context.Background(), id, ev); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := c.DeleteEvent(context.Background(), id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !deleted {
		t.Fatal("delete not performed")
	}
	_ = lastPath
}

func TestGoogleClient_DeleteIdempotent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound) // already gone
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "cal", StaticToken("tok"))
	if err := c.DeleteEvent(context.Background(), "missing"); err != nil {
		t.Fatalf("delete of missing event should be a no-op, got: %v", err)
	}
}
