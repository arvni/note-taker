// Command dirprobe validates the Zoho Directory API and prints the org + users.
// Two token modes:
//   - Self Client grant:   set DIRPROBE_GRANT to the grant token.
//   - Server-based app:     no DIRPROBE_GRANT -> browser authorization-code flow
//     using ZOHO_CLIENT_ID/SECRET + the directory scopes.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
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
	hc := &http.Client{Timeout: 20 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var accessToken, refreshToken, apiDomain string

	if grant := os.Getenv("DIRPROBE_GRANT"); grant != "" {
		// Self Client grant flow (no redirect_uri).
		form := url.Values{}
		form.Set("grant_type", "authorization_code")
		form.Set("client_id", cfg.Zoho.ClientID)
		form.Set("client_secret", cfg.Zoho.ClientSecret)
		form.Set("code", grant)
		accessToken, refreshToken, apiDomain = exchange(ctx, hc, cfg.Zoho.AccountsBase+"/oauth/v2/token", form)
		fmt.Println("✓ [Self Client grant] access token acquired")
	} else {
		// Server-based app: browser authorization-code flow with directory scopes.
		scope := os.Getenv("DIRPROBE_SCOPE")
		if scope == "" {
			scope = "ZohoDirectory.Orgs.READ,ZohoDirectory.Users.READ"
		}
		scopes := append(append([]string{}, cfg.Zoho.Scopes...), scope)
		client := oauth.NewClient(cfg.Zoho.ClientID, cfg.Zoho.ClientSecret, cfg.Zoho.RedirectURI, cfg.Zoho.AccountsBase, scopes)
		state, _, _ := oauth.GenerateState()
		fmt.Println("Authorize (as a Directory admin) with the SERVER-BASED app:\n\n  " + client.AuthorizeURL(state) + "\n")
		code := waitForCallback(state)
		form := url.Values{}
		form.Set("grant_type", "authorization_code")
		form.Set("client_id", cfg.Zoho.ClientID)
		form.Set("client_secret", cfg.Zoho.ClientSecret)
		form.Set("redirect_uri", cfg.Zoho.RedirectURI)
		form.Set("code", code)
		accessToken, refreshToken, apiDomain = exchange(ctx, hc, cfg.Zoho.AccountsBase+"/oauth/v2/token", form)
		fmt.Println("✓ [server-based app] access token acquired")
	}
	if accessToken == "" {
		log.Fatal("no access token")
	}
	if refreshToken != "" {
		fmt.Printf("REFRESH TOKEN (store as ZOHO_DIRECTORY_REFRESH_TOKEN): %s\n", refreshToken)
	}
	api := apiDomain
	if api == "" {
		api = "https://www.zohoapis.com"
	}

	orgsBody := get(ctx, hc, api+"/directory/api/v2/orgs", accessToken)
	fmt.Printf("\n── GET /orgs ──\n%s\n", string(orgsBody))
	var orgs struct {
		Orgs []struct {
			OrgID       json.Number `json:"org_id"`
			DisplayName string      `json:"display_name"`
		} `json:"orgs"`
	}
	json.Unmarshal(orgsBody, &orgs)
	if len(orgs.Orgs) == 0 {
		log.Fatal("\nNo orgs returned with this token.")
	}
	orgID := orgs.Orgs[0].OrgID.String()
	fmt.Printf("\n✓ org_id = %s (%s)\n", orgID, orgs.Orgs[0].DisplayName)

	usersURL := api + "/directory/api/v2/orgs/" + orgID + "/users?page=1&per_page=500&include=emails"
	usersBody := get(ctx, hc, usersURL, accessToken)
	fmt.Printf("\n── GET /orgs/%s/users ──\n%s\n", orgID, headStr(usersBody, 1500))
	fmt.Printf("\n✓ WORKS with this token type. Users URL:\n  %s\n", usersURL)
}

func exchange(ctx context.Context, hc *http.Client, u string, form url.Values) (string, string, string) {
	b := post(ctx, hc, u, form)
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		APIDomain    string `json:"api_domain"`
	}
	json.Unmarshal(b, &tok)
	if tok.AccessToken == "" {
		log.Fatalf("token exchange failed: %s", string(b))
	}
	return tok.AccessToken, tok.RefreshToken, tok.APIDomain
}

func post(ctx context.Context, hc *http.Client, u string, form url.Values) []byte {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b
}

func get(ctx context.Context, hc *http.Client, u, token string) []byte {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	resp, err := hc.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b
}

func headStr(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
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
