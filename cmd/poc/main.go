// Command poc is the Zoho integration proof-of-concept (spec §58). It performs,
// against a REAL Zoho test account and nothing more:
//
//  1. OAuth (server-side Authorization Code)
//  2. Obtain authenticated user identity
//  3. Obtain Calendar READ permission
//  4. List calendars
//  5. List events
//  6. Detect meeting URL
//  7. Refresh access token
//  8. Revoke token
//
// Do not build the full sync engine until this succeeds (spec §58).
//
// Run: copy .env.example to .env, fill Zoho values, then `make poc`.
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
	"github.com/arvinizadi/fathom/internal/oauth"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if cfg.Zoho.ClientID == "" || cfg.Zoho.ClientSecret == "" || cfg.Zoho.RedirectURI == "" {
		log.Fatal("POC requires ZOHO_CLIENT_ID, ZOHO_CLIENT_SECRET, ZOHO_REDIRECT_URI in the environment")
	}

	client := oauth.NewClient(cfg.Zoho.ClientID, cfg.Zoho.ClientSecret, cfg.Zoho.RedirectURI, cfg.Zoho.AccountsBase, cfg.Zoho.Scopes)

	// Step 1: OAuth. Spin up a one-shot local callback listener and print the URL.
	state, _, err := oauth.GenerateState()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("── Step 1: OAuth ──")
	fmt.Println("Open this URL in your browser and authorize with the TEST account:")
	fmt.Println()
	fmt.Println("  " + client.AuthorizeURL(state))
	fmt.Println()

	code := waitForCallback(state)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Step 1 (cont.): exchange code server-side (spec §12).
	tok, err := client.ExchangeCode(ctx, code)
	if err != nil {
		log.Fatalf("token exchange failed: %v", err)
	}
	fmt.Printf("✓ Access token acquired (expires in %ds), refresh token acquired.\n\n", tok.ExpiresIn)

	// Step 2: identity (spec §25).
	fmt.Println("── Step 2: Identity ──")
	id, err := oauth.FetchIdentity(ctx, cfg.Zoho.AccountsBase, tok.AccessToken)
	if err != nil {
		log.Fatalf("identity fetch failed: %v", err)
	}
	fmt.Printf("✓ Authenticated as %s <%s> (ZUID %s)\n\n", id.Name, id.Email, id.ZohoUserID)

	// Steps 3-4: calendar READ + list (spec §27).
	fmt.Println("── Steps 3-4: List calendars ──")
	cal := calendar.NewAPIClient(cfg.Zoho.CalendarBase)
	cals, err := cal.ListCalendars(ctx, tok.AccessToken)
	if err != nil {
		log.Fatalf("list calendars failed: %v", err)
	}
	fmt.Printf("✓ %d calendar(s):\n", len(cals))
	for _, c := range cals {
		fmt.Printf("   - %s (%s, uid=%s)\n", c.Name, c.Type, c.UID)
	}
	fmt.Println()

	// Steps 5-6: list events + detect meeting (spec §29).
	if len(cals) > 0 {
		fmt.Println("── Steps 5-6: List events + detect meetings ──")
		// Zoho requires a range and caps it at 31 days; scan the next 30.
		from := time.Now()
		to := from.Add(30 * 24 * time.Hour)
		evs, err := cal.ListEvents(ctx, tok.AccessToken, cals[0].UID, from, to)
		if err != nil {
			log.Printf("list events failed (non-fatal for POC): %v", err)
		} else {
			fmt.Printf("✓ %d event(s) in %q:\n", len(evs), cals[0].Name)
			for _, e := range evs {
				d := calendar.DetectMeeting(e.Location, e.Description, "")
				if d.HasMeeting() {
					fmt.Printf("   - %s → %s: %s\n", e.Title, d.Provider, d.MeetingURL)
				} else {
					fmt.Printf("   - %s → (no online meeting)\n", e.Title)
				}
			}
		}
		fmt.Println()
	}

	// Step 7: refresh (spec §15).
	fmt.Println("── Step 7: Refresh access token ──")
	refreshed, err := client.Refresh(ctx, tok.RefreshToken)
	if err != nil {
		log.Fatalf("refresh failed: %v", err)
	}
	fmt.Printf("✓ New access token acquired (expires in %ds), no new refresh minted.\n\n", refreshed.ExpiresIn)

	// Step 8: revoke (spec §17).
	fmt.Println("── Step 8: Revoke refresh token ──")
	if err := client.Revoke(ctx, tok.RefreshToken); err != nil {
		log.Fatalf("revoke failed: %v", err)
	}
	fmt.Println("✓ Refresh token revoked.")
	fmt.Println()
	fmt.Println("POC complete. Record the §54 org-level-vs-per-user conclusion in docs/security/oauth-security.md.")
}

// waitForCallback runs a one-shot HTTP server to capture the OAuth callback,
// validating the returned state (spec §9). Returns the authorization code.
func waitForCallback(expectedState string) string {
	codeCh := make(chan string, 1)
	srv := &http.Server{Addr: ":8080"}
	http.HandleFunc("/oauth/zoho/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != expectedState {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			log.Println("callback: state mismatch — possible CSRF")
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code: "+r.URL.Query().Get("error"), http.StatusBadRequest)
			return
		}
		fmt.Fprintln(w, "Authorization received. You may close this tab and return to the terminal.")
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
	_ = os.Stdout.Sync()
	return code
}
