// Command dirprobe validates the Zoho Directory API using the documented Self
// Client flow (spec §3): exchange an admin-generated grant token, discover the
// real org_id via GET /orgs, then list users with emails. No browser consent.
//
// Env: ZOHO_CLIENT_ID, ZOHO_CLIENT_SECRET (Self Client), ZOHO_ACCOUNTS_BASE,
// DIRPROBE_GRANT (the temporary grant token from the Developer Console).
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
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	grant := os.Getenv("DIRPROBE_GRANT")
	if grant == "" {
		log.Fatal("set DIRPROBE_GRANT to the Self Client grant token (scopes: ZohoDirectory.Orgs.READ,ZohoDirectory.Users.READ)")
	}
	hc := &http.Client{Timeout: 20 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1) Exchange the grant token (Self Client: no redirect_uri).
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", cfg.Zoho.ClientID)
	form.Set("client_secret", cfg.Zoho.ClientSecret)
	form.Set("code", grant)
	tokBody := post(ctx, hc, cfg.Zoho.AccountsBase+"/oauth/v2/token", form)
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		APIDomain    string `json:"api_domain"`
		Error        string `json:"error"`
	}
	json.Unmarshal(tokBody, &tok)
	if tok.AccessToken == "" {
		log.Fatalf("grant exchange failed: %s", string(tokBody))
	}
	if tok.RefreshToken != "" {
		fmt.Printf("REFRESH TOKEN (store as ZOHO_DIRECTORY_REFRESH_TOKEN): %s\n", tok.RefreshToken)
	}
	api := tok.APIDomain
	if api == "" {
		api = "https://www.zohoapis.com"
	}
	fmt.Println("✓ grant exchanged for access token")

	// 2) GET /orgs -> real org_id.
	orgsBody := get(ctx, hc, api+"/directory/api/v2/orgs", tok.AccessToken)
	fmt.Printf("\n── GET /orgs ──\n%s\n", string(orgsBody))
	var orgs struct {
		Orgs []struct {
			OrgID       json.Number `json:"org_id"`
			DisplayName string      `json:"display_name"`
		} `json:"orgs"`
	}
	json.Unmarshal(orgsBody, &orgs)
	if len(orgs.Orgs) == 0 {
		log.Fatal("\nNo orgs returned — the token's account is not a Directory admin/owner. Grant Directory Super Admin or use an owner account.")
	}
	orgID := orgs.Orgs[0].OrgID.String()
	if v := os.Getenv("DIRPROBE_ORGID"); v != "" {
		orgID = v
	}
	fmt.Printf("\n✓ org_id = %s (%s)\n", orgID, orgs.Orgs[0].DisplayName)

	// 3) GET /orgs/{org_id}/users?include=emails
	usersURL := api + "/directory/api/v2/orgs/" + orgID + "/users?page=1&per_page=500&include=emails"
	usersBody := get(ctx, hc, usersURL, tok.AccessToken)
	fmt.Printf("\n── GET /orgs/%s/users ──\n%s\n", orgID, head(usersBody, 3000))
	fmt.Printf("\nUsers URL for config:\n  %s\n", usersURL)
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

func head(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
