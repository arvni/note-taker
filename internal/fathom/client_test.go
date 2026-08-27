package fathom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFathomClient(t *testing.T) {
	var createdSpec WebhookSpec
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key123" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/meetings":
			json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
				{"id": "m1", "title": "Client call", "meeting_url": "https://meet.google.com/x", "recording_url": "https://fathom/rec/1"},
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/recordings/transcript":
			json.NewEncoder(w).Encode(map[string]any{"recording_id": r.URL.Query().Get("recording_id"), "transcript": "hello"})
		case r.Method == http.MethodPost && r.URL.Path == "/webhooks":
			_ = json.NewDecoder(r.Body).Decode(&createdSpec)
			json.NewEncoder(w).Encode(map[string]any{"id": "wh1"})
		case r.Method == http.MethodDelete:
			w.WriteHeader(204)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key123")
	ctx := context.Background()

	meetings, err := c.ListMeetings(ctx, nil)
	if err != nil || len(meetings) != 1 || meetings[0].MeetingURL != "https://meet.google.com/x" {
		t.Fatalf("list meetings: %v %+v", err, meetings)
	}

	tr, err := c.GetTranscript(ctx, "m1")
	if err != nil || tr.Text != "hello" {
		t.Fatalf("transcript: %v %+v", err, tr)
	}

	id, err := c.CreateWebhook(ctx, WebhookSpec{DestinationURL: "https://app/webhooks/fathom", IncludeTranscript: true})
	if err != nil || id != "wh1" {
		t.Fatalf("create webhook: %v id=%s", err, id)
	}
	if createdSpec.DestinationURL == "" || !createdSpec.IncludeTranscript {
		t.Fatalf("webhook spec not sent correctly: %+v", createdSpec)
	}
	if err := c.DeleteWebhook(ctx, "wh1"); err != nil {
		t.Fatalf("delete webhook: %v", err)
	}
}

func TestCreateWebhook_RequiresInclude(t *testing.T) {
	c := NewClient("https://example", "k")
	if _, err := c.CreateWebhook(context.Background(), WebhookSpec{DestinationURL: "https://x"}); err == nil {
		t.Fatal("expected error when no include_* flag is set")
	}
}

func TestParseWebhook(t *testing.T) {
	p, err := ParseWebhook([]byte(`{"event":"meeting.processed","meeting_url":"https://meet.google.com/x","recording_id":"r1"}`))
	if err != nil || p.MeetingURL != "https://meet.google.com/x" {
		t.Fatalf("parse: %v %+v", err, p)
	}
	if _, err := ParseWebhook([]byte(`{"event":"x"}`)); err == nil {
		t.Fatal("expected error for payload without meeting_url/recording_id")
	}
}
