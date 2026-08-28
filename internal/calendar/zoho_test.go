package calendar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Real response shapes captured from a live Zoho account (2026-08-28).

func TestListCalendars_LiveShape(t *testing.T) {
	body := `{"calendars":[{"color":"#8cbf40","timezone":"Asia/Dubai","description":"","privilege":"owner","type":0,"uid":"1ce721e24c7e42c785a5b61f216d08c2","isdefault":true,"id":"7006011000000009003","owner":"839140129","name":"cio","category":"own","status":true,"caltype":"own"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	cals, err := NewAPIClient(srv.URL).ListCalendars(context.Background(), "tok")
	if err != nil {
		t.Fatalf("ListCalendars: %v", err) // must NOT fail on numeric `type`
	}
	if len(cals) != 1 {
		t.Fatalf("got %d calendars, want 1", len(cals))
	}
	c := cals[0]
	if c.UID != "1ce721e24c7e42c785a5b61f216d08c2" || c.Name != "cio" ||
		c.Type != "own" || c.Timezone != "Asia/Dubai" || c.Owner != "839140129" {
		t.Fatalf("calendar mapped wrong: %+v", c)
	}
}

func TestListEvents_NoEventsSentinel(t *testing.T) {
	// Empty ranges return a sentinel object, not an empty array.
	body := `{"events":[{"message":"No events found."}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	evs, err := NewAPIClient(srv.URL).ListEvents(context.Background(), "tok", "cal", time.Now(), time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 0 {
		t.Fatalf("sentinel should yield 0 events, got %d: %+v", len(evs), evs)
	}
}
