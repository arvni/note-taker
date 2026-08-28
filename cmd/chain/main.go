// Command chain runs the full outbound pipeline against REAL services end-to-end:
// authorize Zoho, read the employee's calendar, detect qualifying meetings, and
// CREATE the corresponding events in a real Google Calendar (spec §42). This is a
// manual verification tool, not part of the production server.
//
// Google auth: set GOOGLE_CREDENTIALS_FILE (service account) OR GOOGLE_ACCESS_TOKEN
// (e.g. from the OAuth Playground), plus GOOGLE_CALENDAR_ID (or "primary").
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/arvinizadi/fathom/internal/calendar"
	"github.com/arvinizadi/fathom/internal/config"
	"github.com/arvinizadi/fathom/internal/google"
	"github.com/arvinizadi/fathom/internal/oauth"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if cfg.GoogleCalendarID == "" {
		log.Fatal("set GOOGLE_CALENDAR_ID (destination calendar id, or 'primary')")
	}

	// Google destination auth: service account preferred, else static token.
	var gts google.TokenSource = google.StaticToken(cfg.GoogleAccessToken)
	if cfg.GoogleCredentialsFile != "" {
		key, err := os.ReadFile(cfg.GoogleCredentialsFile)
		if err != nil {
			log.Fatalf("read google credentials: %v", err)
		}
		if gts, err = google.NewServiceAccountTokenSource(key, google.CalendarScope, cfg.GoogleSubject); err != nil {
			log.Fatalf("google service account: %v", err)
		}
		log.Print("using Google service-account credentials")
	} else if cfg.GoogleAccessToken == "" {
		log.Fatal("set GOOGLE_CREDENTIALS_FILE or GOOGLE_ACCESS_TOKEN for the destination")
	}
	gcal := google.NewClient(cfg.GoogleCalendarBase, cfg.GoogleCalendarID, gts)

	// --- Zoho authorization (same flow as the POC) ---
	client := oauth.NewClient(cfg.Zoho.ClientID, cfg.Zoho.ClientSecret, cfg.Zoho.RedirectURI, cfg.Zoho.AccountsBase, cfg.Zoho.Scopes)
	state, _, _ := oauth.GenerateState()
	fmt.Println("Open this URL and authorize with the TEST account:")
	fmt.Println("\n  " + client.AuthorizeURL(state) + "\n")
	code := waitForCallback(state)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	tok, err := client.ExchangeCode(ctx, code)
	if err != nil {
		log.Fatalf("token exchange: %v", err)
	}
	fmt.Println("✓ Zoho authorized")

	// --- Read real calendar + detect meetings ---
	cal := calendar.NewAPIClient(cfg.Zoho.CalendarBase)
	cals, err := cal.ListCalendars(ctx, tok.AccessToken)
	if err != nil || len(cals) == 0 {
		log.Fatalf("list calendars: %v (found %d)", err, len(cals))
	}
	from, to := time.Now(), time.Now().Add(30*24*time.Hour)

	created := 0
	for _, c := range cals {
		evs, err := cal.ListEvents(ctx, tok.AccessToken, c.UID, from, to)
		if err != nil {
			log.Printf("list events (%s): %v", c.Name, err)
			continue
		}
		for _, e := range evs {
			det := calendar.DetectMeeting(e.Location, e.Description, "")
			if !det.HasMeeting() {
				fmt.Printf("  skip %q (no online meeting)\n", e.Title)
				continue
			}
			start := parseTime(e.Start)
			end := parseTime(e.End)
			if start.IsZero() || end.IsZero() {
				fmt.Printf("  skip %q (unparseable time %q/%q)\n", e.Title, e.Start, e.End)
				continue
			}
			id, err := gcal.CreateEvent(ctx, google.Event{
				Summary:     "[Fathom] " + e.Title,
				Location:    det.MeetingURL,
				Description: "Synced from Zoho by the Calendar Bridge chain test.\nJoin: " + det.MeetingURL,
				Start:       start,
				End:         end,
			})
			if err != nil {
				log.Printf("  create google event for %q: %v", e.Title, err)
				continue
			}
			created++
			fmt.Printf("  ✓ created Google event %s  <- %q (%s)\n", id, e.Title, det.Provider)
		}
	}
	fmt.Printf("\nDone. Created %d Google Calendar event(s) in %q.\n", created, cfg.GoogleCalendarID)
	if created > 0 {
		fmt.Println("Check the calendar; if Fathom watches it, it should now detect the meeting(s).")
	}
}

func parseTime(s string) time.Time {
	for _, layout := range []string{"20060102T150405Z0700", "20060102T150405Z", "20060102"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func waitForCallback(expectedState string) string {
	codeCh := make(chan string, 1)
	srv := &http.Server{Addr: ":8080"}
	http.HandleFunc("/oauth/zoho/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != expectedState {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		fmt.Fprintln(w, "Authorized. Return to the terminal.")
		codeCh <- code
	})
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("callback server: %v", err)
		}
	}()
	fmt.Println("Waiting for callback on http://localhost:8080/oauth/zoho/callback ...")
	code := <-codeCh
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	return code
}
