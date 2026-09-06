// Command server hosts the web UI, OAuth flow, dashboards, and API (spec §8, §46-47).
// It also runs DB migrations via subcommands:
//
//	server migrate up      apply all pending migrations
//	server migrate down    roll back the most recent migration
//
// With no subcommand it starts the HTTP server.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/arvinizadi/fathom/internal/audit"
	"github.com/arvinizadi/fathom/internal/calendar"
	"github.com/arvinizadi/fathom/internal/config"
	"github.com/arvinizadi/fathom/internal/crypto"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/directory"
	"github.com/arvinizadi/fathom/internal/google"
	"github.com/arvinizadi/fathom/internal/httpx"
	"github.com/arvinizadi/fathom/internal/oauth"
	"github.com/arvinizadi/fathom/internal/oidc"
	"github.com/arvinizadi/fathom/internal/onboarding"
	"github.com/arvinizadi/fathom/internal/rbac"
	"github.com/arvinizadi/fathom/internal/redisx"
	"github.com/arvinizadi/fathom/internal/security"
	"github.com/arvinizadi/fathom/internal/tokens"
	"github.com/arvinizadi/fathom/internal/wire"
	"github.com/arvinizadi/fathom/web"
)

// empStore adapts *directory.Repo to onboarding.EmployeeStore.
type empStore struct{ repo *directory.Repo }

func (a empStore) GetByID(ctx context.Context, org db.OrgID, id int64) (string, string, error) {
	e, err := a.repo.GetByID(ctx, org, id)
	if err != nil {
		return "", "", err
	}
	return e.Email, e.Name, nil
}

func (a empStore) SetOnboardingStatus(ctx context.Context, org db.OrgID, id int64, status string) error {
	return a.repo.SetOnboardingStatus(ctx, org, id, status)
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		runMigrate(cfg, os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "prune-orphans" {
		runPruneOrphans(cfg)
		return
	}
	serve(cfg)
}

func runMigrate(cfg *config.Config, args []string) {
	if len(args) == 0 {
		log.Fatal("usage: server migrate [up|down]")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("migrate: %v", err)
	}
	defer pool.Close()

	switch args[0] {
	case "up":
		n, err := db.MigrateUp(ctx, pool)
		if err != nil {
			log.Fatalf("migrate up: %v", err)
		}
		log.Printf("migrate up: applied %d migration(s)", n)
	case "down":
		v, err := db.MigrateDown(ctx, pool)
		if err != nil {
			log.Fatalf("migrate down: %v", err)
		}
		if v == 0 {
			log.Print("migrate down: nothing to roll back")
		} else {
			log.Printf("migrate down: rolled back version %d", v)
		}
	default:
		log.Fatalf("migrate: unknown subcommand %q (want up|down)", args[0])
	}
}

// runPruneOrphans deletes destination calendar events that no active mapping
// references any more (e.g. leftovers after de-duplicating co-attendee events).
func runPruneOrphans(cfg *config.Config) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("prune: db: %v", err)
	}
	defer pool.Close()
	cipher, err := crypto.NewFromBase64Key(cfg.CryptoMasterKey)
	if err != nil {
		log.Fatalf("prune: crypto: %v", err)
	}
	org := db.OrgID(cfg.AdminOrgID)
	appSettings := tokens.NewAppSettingsStore(pool, cipher)
	destStore := tokens.NewGoogleDestStore(pool, cipher)
	calRepo := calendar.NewRepo(pool)

	d, err := destStore.Get(ctx, org)
	if err != nil {
		log.Fatalf("prune: no google destination for org %d: %v", org, err)
	}
	redirect := cfg.GoogleOAuthRedirectURL
	if redirect == "" {
		redirect = cfg.PublicBaseURL + "/oauth/google/callback"
	}
	var oc *google.OAuthClient
	if a, err := appSettings.Get(ctx, org); err == nil && a.GoogleOAuthClientID != "" && a.GoogleOAuthClientSecret != "" {
		oc = google.NewOAuthClient(a.GoogleOAuthClientID, a.GoogleOAuthClientSecret, redirect)
	} else if cfg.GoogleOAuthClientID != "" {
		oc = google.NewOAuthClient(cfg.GoogleOAuthClientID, cfg.GoogleOAuthClientSecret, redirect)
	} else {
		log.Fatal("prune: no Google OAuth client configured")
	}
	gcal := google.NewClient(cfg.GoogleCalendarBase, d.CalendarID, google.NewRefreshTokenSource(oc, d.RefreshToken))

	active, err := calRepo.ActiveDestinationEventIDs(ctx, org)
	if err != nil {
		log.Fatalf("prune: active ids: %v", err)
	}
	events, err := gcal.ListEvents(ctx, time.Now().Add(-90*24*time.Hour), time.Now().Add(180*24*time.Hour))
	if err != nil {
		log.Fatalf("prune: list events: %v", err)
	}
	deleted := 0
	for _, ev := range events {
		if active[ev.ID] {
			continue
		}
		if err := gcal.DeleteEvent(ctx, ev.ID); err != nil {
			log.Printf("prune: delete %s (%s): %v", ev.ID, ev.Summary, err)
			continue
		}
		log.Printf("prune: deleted orphan %q (%s)", ev.Summary, ev.ID)
		deleted++
	}
	log.Printf("prune-orphans: scanned %d events, deleted %d orphan(s)", len(events), deleted)
}

func serve(cfg *config.Config) {
	ctx := context.Background()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("server: db: %v", err)
	}
	defer pool.Close()

	cipher, err := crypto.NewFromBase64Key(cfg.CryptoMasterKey)
	if err != nil {
		log.Fatalf("server: crypto key (set CRYPTO_MASTER_KEY, e.g. openssl rand -base64 32): %v", err)
	}
	tmpl, err := web.Templates()
	if err != nil {
		log.Fatalf("server: templates: %v", err)
	}

	redisClient, err := redisx.Connect(ctx, cfg.RedisURL)
	if err != nil {
		log.Fatalf("server: redis: %v", err)
	}
	defer redisClient.Close()

	// Rate limiters (spec §37) and security monitor (spec §36).
	connectRL := httpx.NewRateLimit(redisClient, "connect", cfg.RateLimitConnect, cfg.RateLimitWindow)
	oauthRL := httpx.NewRateLimit(redisClient, "oauth", cfg.RateLimitOAuth, cfg.RateLimitWindow)
	apiRL := httpx.NewRateLimit(redisClient, "api", cfg.RateLimitAPI, cfg.RateLimitWindow)
	monitor := security.NewMonitor(redisClient, security.LogAlerter{}, nil)

	auditLog := audit.New(audit.NewPostgresSink(pool))
	empRepo := directory.NewRepo(pool)
	onbRepo := onboarding.NewRepo(pool)
	stateRepo := oauth.NewStateRepo(pool)
	credStore := tokens.NewStore(pool, cipher)
	// Per-org Zoho config: entered in the app (Settings) and stored encrypted, so
	// one deployment serves any Zoho org. Falls back to env config if unset.
	zohoSettings := tokens.NewZohoSettingsStore(pool, cipher)
	zohoRedirect := cfg.Zoho.RedirectURI
	if zohoRedirect == "" {
		zohoRedirect = cfg.PublicBaseURL + "/oauth/zoho/callback"
	}
	zohoResolve := func(ctx context.Context, org db.OrgID) (oauth.OrgConfig, error) {
		if oc, err := zohoSettings.OrgConfig(ctx, org, zohoRedirect); err == nil && oc.Configured() {
			return oc, nil
		}
		if cfg.Zoho.ClientID != "" {
			return oauth.OrgConfig{ClientID: cfg.Zoho.ClientID, ClientSecret: cfg.Zoho.ClientSecret,
				AccountsBase: cfg.Zoho.AccountsBase, CalendarBase: cfg.Zoho.CalendarBase,
				RedirectURI: zohoRedirect, Scopes: cfg.Zoho.Scopes}, nil
		}
		return oauth.OrgConfig{}, fmt.Errorf("zoho not configured for org %d", org)
	}
	refresherFor := func(ctx context.Context, org db.OrgID) (tokens.Refresher, error) {
		oc, err := zohoResolve(ctx, org)
		if err != nil {
			return nil, err
		}
		return oc.Client(), nil
	}
	zohoRevokerFor := func(ctx context.Context, org db.OrgID) (tokens.ZohoRevoker, error) {
		oc, err := zohoResolve(ctx, org)
		if err != nil {
			return nil, err
		}
		return oc.Client(), nil
	}
	calendarBaseFor := func(ctx context.Context, org db.OrgID) (string, error) {
		oc, err := zohoResolve(ctx, org)
		if err != nil {
			return "", err
		}
		return oc.CalendarBase, nil
	}

	// Per-org app settings (email, branding, Fathom) entered in the app, with env
	// fallback. Resolves the email sender + branding per org for invitations.
	appSettings := tokens.NewAppSettingsStore(pool, cipher)
	onboardingResolve := wire.OnboardingResolver(appSettings, cfg)

	// Onboarding service doubles as the reconnect trigger when a credential goes
	// invalid (spec §18, §45).
	onboardingSvc := onboarding.NewService(onbRepo, empStore{empRepo}, onboardingResolve, auditLog, tmpl,
		cfg.PublicBaseURL, cfg.OnboardingTokenBytes, cfg.OnboardingTokenTTL)

	// Token lifecycle: refresh under a Redis lock, revoke, and detect revocation.
	tokenMgr := tokens.NewManager(credStore, refresherFor, tokens.NewRedisLocker(redisClient), auditLog, onboardingSvc)
	revoker := tokens.NewRevoker(credStore, zohoRevokerFor, empRepo, auditLog)
	offboarder := tokens.NewOffboarder(credStore, zohoRevokerFor, empRepo, auditLog)

	// Directory reconciler: new employees are invited, inactive ones offboarded
	// (spec §20, §19). The directory source that drives it is wired once the
	// org-level auth model is resolved (spec §54).
	reconciler := directory.NewReconciler(empRepo, onboardingSvc, offboarder)

	// Calendar discovery + scan (spec §27-32), driven per authorized employee by
	// the sync loop in Phase 8.
	calRepo := calendar.NewRepo(pool)
	calSvc := calendar.NewService(tokenMgr, calendarBaseFor, calRepo, empRepo, auditLog, cfg.SyncAllEvents)
	_ = calSvc // consumed by the worker sync loops (Phase 8)

	oauthHandler := httpx.NewOAuthHandler(httpx.OAuthDeps{
		Onboarding:  onbRepo,
		States:      stateRepo,
		Employees:   empRepo,
		Creds:       credStore,
		Zoho:        zohoResolve,
		Audit:       auditLog,
		Templates:   tmpl,
		CompanyName: cfg.CompanyName,
		AppName:     cfg.AppName,
		Permissions: cfg.ReadablePermissions(),
		SupportAddr: cfg.SupportAddr,
		PrivacyURL:  cfg.PrivacyURL,
		TermsURL:    cfg.TermsURL,
		Security:    monitor,
		ConnectRL:   connectRL.Wrap,
		OAuthRL:     oauthRL.Wrap,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		pingCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := pool.Ping(pingCtx); err != nil {
			http.Error(w, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ready")
	})
	oauthHandler.Register(mux)
	httpx.NewAPIHandler(revoker, apiRL.Wrap).Register(mux)
	httpx.NewFathomHandler(calRepo, auditLog, cfg.FathomWebhookSecret).Register(mux)

	// Sessions, auth middleware, dashboards, and the OIDC login seam (spec §38-41,
	// §46-47). Admin SSO is wired via a company-specific IdentityProvider.
	sessionKey := []byte(cfg.SessionKey)
	if len(sessionKey) < 32 {
		log.Fatal("server: SESSION_KEY must be set (>=32 bytes) for session signing")
	}
	sessions := rbac.NewSessionManager(sessionKey, cfg.SessionTTL, cfg.IsProduction())
	auth := httpx.NewAuthMiddleware(sessions)
	httpx.NewDashboardHandler(httpx.DashboardDeps{
		Employees: empRepo, Calendars: calRepo, Revoker: revoker, Auth: auth,
		Templates: tmpl, CompanyName: cfg.CompanyName,
	}).Register(mux)
	httpx.NewImportHandler(reconciler, auth, tmpl).Register(mux)

	// Destination (Fathom-watched) Google Calendar (spec §42): connected by an
	// admin via OAuth ("Connect Google Calendar"), or a static/service-account
	// token from config. Resolved per request from the stored credential.
	destStore := tokens.NewGoogleDestStore(pool, cipher)
	googleRedirect := cfg.GoogleOAuthRedirectURL
	if googleRedirect == "" {
		googleRedirect = cfg.PublicBaseURL + "/oauth/google/callback"
	}
	// The Google OAuth client is resolved per org: stored app settings first
	// (entered in the UI), env fallback. Returns nil when unconfigured.
	googleOAuthFor := func(ctx context.Context, org db.OrgID) *google.OAuthClient {
		if a, err := appSettings.Get(ctx, org); err == nil && a.GoogleOAuthClientID != "" && a.GoogleOAuthClientSecret != "" {
			return google.NewOAuthClient(a.GoogleOAuthClientID, a.GoogleOAuthClientSecret, googleRedirect)
		}
		if cfg.GoogleOAuthClientID != "" && cfg.GoogleOAuthClientSecret != "" {
			return google.NewOAuthClient(cfg.GoogleOAuthClientID, cfg.GoogleOAuthClientSecret, googleRedirect)
		}
		return nil
	}
	resolveDest := func(ctx context.Context, org db.OrgID) httpx.Destination {
		// 1) Admin-connected OAuth credential (preferred).
		if d, err := destStore.Get(ctx, org); err == nil {
			if googleOAuth := googleOAuthFor(ctx, org); googleOAuth != nil {
				ts := google.NewRefreshTokenSource(googleOAuth, d.RefreshToken)
				return httpx.Destination{Cal: google.NewClient(cfg.GoogleCalendarBase, d.CalendarID, ts),
					CalendarID: d.CalendarID, ConnectedEmail: d.ConnectedEmail, Connected: true}
			}
		}
		// 2) Static / service-account token from config.
		if cfg.GoogleCalendarID != "" && (cfg.GoogleCredentialsFile != "" || cfg.GoogleAccessToken != "") {
			var gts google.TokenSource = google.StaticToken(cfg.GoogleAccessToken)
			if cfg.GoogleCredentialsFile != "" {
				if key, err := os.ReadFile(cfg.GoogleCredentialsFile); err == nil {
					if sa, err := google.NewServiceAccountTokenSource(key, google.CalendarScope, cfg.GoogleSubject); err == nil {
						gts = sa
					}
				}
			}
			return httpx.Destination{Cal: google.NewClient(cfg.GoogleCalendarBase, cfg.GoogleCalendarID, gts),
				CalendarID: cfg.GoogleCalendarID, Connected: true}
		}
		return httpx.Destination{Connected: false}
	}
	httpx.NewDestinationHandler(resolveDest, calRepo.SourceAttendees, auth).Register(mux)
	httpx.NewGoogleConnectHandler(googleOAuthFor, destStore, sessions, auth, cfg.GoogleCalendarID, sessionKey, cfg.IsProduction()).Register(mux)
	httpx.NewAPIv1(empRepo, calRepo, reconciler, revoker, onboardingSvc, auth).Register(mux)
	fathomKeyFor := func(ctx context.Context, org db.OrgID) string {
		return wire.FathomAPIKey(ctx, appSettings, org, cfg.FathomAPIKey)
	}
	httpx.NewFathomAdminHandler(cfg.FathomAPIBase, fathomKeyFor, tokens.NewFathomWebhookStore(pool), auth, cfg.PublicBaseURL).Register(mux)
	// Fireflies.ai notetaker: webhook receiver downloads each transcript+summary.
	firefliesKeyFor := func(ctx context.Context, org db.OrgID) string {
		return wire.FirefliesAPIKey(ctx, appSettings, org, cfg.FirefliesAPIKey)
	}
	httpx.NewFirefliesWebhookHandler(firefliesKeyFor, cfg.FirefliesDownloadDir, cfg.FirefliesWebhookSecret, db.OrgID(cfg.AdminOrgID)).
		WithAttendeeNotify(calRepo.MatchAttendees, onboardingResolve, cfg.FirefliesNotifyAttendees).
		Register(mux)
	httpx.NewRecordingsHandler(cfg.FirefliesDownloadDir, auth).
		WithSend(onboardingResolve, calRepo.MatchAttendees, db.OrgID(cfg.AdminOrgID)).
		WithImport(firefliesKeyFor).
		Register(mux)
	httpx.NewSettingsHandler(zohoSettings, appSettings, auth, zohoRedirect, googleRedirect).Register(mux)
	if spaFS, err := web.SPA(); err == nil {
		httpx.NewSPAHandler(spaFS).Register(mux)
	} else {
		log.Printf("server: admin SPA not embedded: %v", err)
	}
	// Admin OIDC login (spec §39). Enabled when OIDC_ISSUER is configured.
	var loginFlow httpx.OIDCFlow
	var mapper httpx.AdminMapper
	if cfg.OIDCIssuer != "" {
		loginFlow = oidc.New(oidc.Config{
			IssuerURL: cfg.OIDCIssuer, ClientID: cfg.OIDCClientID, ClientSecret: cfg.OIDCClientSecret,
			RedirectURL: cfg.OIDCRedirectURL, HostedDomain: cfg.OIDCHostedDomain,
		})
		mapper = httpx.NewAllowlistMapper(cfg.AdminOrgID, cfg.AdminEmails)
		log.Printf("admin SSO enabled (issuer=%s, %d admin(s))", cfg.OIDCIssuer, len(cfg.AdminEmails))
	}
	providerName := "SSO"
	switch {
	case strings.Contains(cfg.OIDCIssuer, "google"):
		providerName = "Google Workspace"
	case strings.Contains(cfg.OIDCIssuer, "microsoft") || strings.Contains(cfg.OIDCIssuer, "login.microsoftonline"):
		providerName = "Microsoft"
	case strings.Contains(cfg.OIDCIssuer, "zoho"):
		providerName = "Zoho"
	}
	httpx.NewLoginHandler(loginFlow, mapper, sessions, sessionKey, cfg.IsProduction(), tmpl, cfg.CompanyName, cfg.AppName, providerName).Register(mux)

	log.Printf("server listening on %s (env=%s)", cfg.HTTPAddr, cfg.AppEnv)
	log.Fatal(http.ListenAndServe(cfg.HTTPAddr, logRequests(mux)))
}

// statusRecorder captures the response status code for access logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// logRequests logs one line per request: method, path, status, duration, and
// the real client IP (behind Cloudflare, from CF-Connecting-IP). Health probes
// are skipped to avoid noise.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		ip := r.Header.Get("CF-Connecting-IP")
		if ip == "" {
			ip = r.RemoteAddr
		}
		log.Printf("%s %s -> %d (%s) ip=%s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond), ip)
	})
}
