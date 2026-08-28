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

func TestListEvents_LiveShape(t *testing.T) {
	// Exact event JSON from a live Zoho account: start/end nested in dateandtime.
	body := `{"events":[{"title":"test notetaker","uid":"63nu4ucdte8r3kn33ebe74dntk@google.com","dateandtime":{"timezone":"Asia/Dubai","start":"20260830T110000+0400","end":"20260830T120000+0400"},"lastmodifiedtime":"20260828T065238Z","isprivate":false,"location":"https://meet.google.com/qyi-scvm-mai"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	evs, err := NewAPIClient(srv.URL).ListEvents(context.Background(), "tok", "cal", time.Now(), time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
	e := evs[0]
	if e.Title != "test notetaker" || e.Location != "https://meet.google.com/qyi-scvm-mai" {
		t.Fatalf("event basic fields wrong: %+v", e)
	}
	// The critical fix: start/end must be populated from dateandtime.
	if e.Start != "20260830T110000+0400" || e.End != "20260830T120000+0400" {
		t.Fatalf("start/end not extracted from dateandtime: start=%q end=%q", e.Start, e.End)
	}
	// And they must parse to a non-nil time (offset format).
	if parseZohoTime(e.Start) == nil || parseZohoTime(e.End) == nil {
		t.Fatalf("parseZohoTime failed on offset format: %q / %q", e.Start, e.End)
	}
	// Detection picks up the Meet link.
	if d := DetectMeeting(e.Location, e.Description, ""); d.Provider != ProviderMeet {
		t.Fatalf("meeting not detected: %+v", d)
	}
}
