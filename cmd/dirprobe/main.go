// Command dirprobe is a one-shot experiment to discover a working Zoho org-users
// endpoint for this account (spec §3/§55). It authorizes with the directory
// scope, then GETs a list of candidate endpoints and dumps status + raw body so
// we can identify which (if any) returns the org directory. Not part of the app.
//
// Prereq: add the DIRPROBE_SCOPE (default ZohoOne.Users.READ) to the Zoho app in
// the API Console, and authorize as a super-admin.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/arvinizadi/fathom/internal/config"
	"github.com/arvinizadi/fathom/internal/oauth"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	scope := os.Getenv("DIRPROBE_SCOPE")
	if scope == "" {
		scope = "ZohoOne.Users.READ"
	}
	// Request directory scope in addition to the existing ones (skip with
	// DIRPROBE_SCOPE=none for a zero-console-change existence probe).
	scopes := append([]string{}, cfg.Zoho.Scopes...)
	if scope != "none" && scope != "-" {
		scopes = append(scopes, scope)
	}
	client := oauth.NewClient(cfg.Zoho.ClientID, cfg.Zoho.ClientSecret, cfg.Zoho.RedirectURI, cfg.Zoho.AccountsBase, scopes)

	state, _, _ := oauth.GenerateState()
	fmt.Println("Authorize (as a super-admin) with directory scope:\n\n  " + client.AuthorizeURL(state) + "\n")
	code := waitForCallback(state)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	tok, err := client.ExchangeCode(ctx, code)
	if err != nil {
		log.Fatalf("token exchange (did you add %q to the app?): %v", scope, err)
	}
	fmt.Printf("✓ token acquired; granted scope: %s\n\n", tokenScope(tok))

	// api_domain is account-specific (e.g. https://www.zohoapis.com).
	api := tok.APIDomain
	if api == "" {
		api = "https://www.zohoapis.com"
	}
	candidates := []string{
		api + "/organization/v1/users",
		api + "/directory/v1/users",
		api + "/directory/api/v1/users",
		"https://directory.zoho.com/api/v1/users",
		strings.Replace(api, "zohoapis", "accounts", 1) + "/api/v1/users",
	}
	hc := &http.Client{Timeout: 15 * time.Second}
	for _, url := range candidates {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		req.Header.Set("Authorization", "Zoho-oauthtoken "+tok.AccessToken)
		resp, err := hc.Do(req)
		if err != nil {
			fmt.Printf("  %-55s ERROR %v\n", url, err)
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 600))
		resp.Body.Close()
		fmt.Printf("  %-55s -> %d\n     %s\n", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	fmt.Println("\nLook for a 200 with a users array. If all fail, CSV import is the correct path (spec §3).")
}

func tokenScope(t *oauth.TokenResponse) string { return "(granted; see consent)" }

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
			http.Error(w, "missing code: "+r.URL.Query().Get("error"), http.StatusBadRequest)
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
