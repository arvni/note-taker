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
	"time"

	"github.com/arvinizadi/fathom/internal/audit"
	"github.com/arvinizadi/fathom/internal/calendar"
	"github.com/arvinizadi/fathom/internal/config"
	"github.com/arvinizadi/fathom/internal/crypto"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/directory"
	"github.com/arvinizadi/fathom/internal/email"
	"github.com/arvinizadi/fathom/internal/httpx"
	"github.com/arvinizadi/fathom/internal/oauth"
	"github.com/arvinizadi/fathom/internal/oidc"
	"github.com/arvinizadi/fathom/internal/onboarding"
	"github.com/arvinizadi/fathom/internal/rbac"
	"github.com/arvinizadi/fathom/internal/redisx"
	"github.com/arvinizadi/fathom/internal/security"
	"github.com/arvinizadi/fathom/internal/tokens"
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
	zohoClient := oauth.NewClient(cfg.Zoho.ClientID, cfg.Zoho.ClientSecret,
		cfg.Zoho.RedirectURI, cfg.Zoho.AccountsBase, cfg.Zoho.Scopes)

	// Email sender: real SMTP relay when configured, else a dev logger.
	var sender email.Sender = email.LogSender{}
	if cfg.SMTPHost != "" {
		sender = email.NewSMTPSender(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.EmailFrom)
		log.Print("email: using SMTP relay " + cfg.SMTPHost)
	}

	// Onboarding service doubles as the reconnect trigger when a credential goes
	// invalid (spec §18, §45).
	onboardingSvc := onboarding.NewService(onbRepo, empStore{empRepo}, sender, auditLog, tmpl, onboarding.Config{
		BaseURL:     cfg.PublicBaseURL,
		CompanyName: cfg.CompanyName,
		AppName:     cfg.AppName,
		Permissions: cfg.ReadablePermissions(),
		SupportAddr: cfg.SupportAddr,
		PrivacyURL:  cfg.PrivacyURL,
		FromAddr:    cfg.EmailFrom,
		TokenBytes:  cfg.OnboardingTokenBytes,
		TTL:         cfg.OnboardingTokenTTL,
	})

	// Token lifecycle: refresh under a Redis lock, revoke, and detect revocation.
	tokenMgr := tokens.NewManager(credStore, zohoClient, tokens.NewRedisLocker(redisClient), auditLog, onboardingSvc)
	revoker := tokens.NewRevoker(credStore, zohoClient, empRepo, auditLog)
	offboarder := tokens.NewOffboarder(credStore, zohoClient, empRepo, auditLog)

	// Directory reconciler: new employees are invited, inactive ones offboarded
	// (spec §20, §19). The directory source that drives it is wired once the
	// org-level auth model is resolved (spec §54).
	reconciler := directory.NewReconciler(empRepo, onboardingSvc, offboarder)

	// Calendar discovery + scan (spec §27-32), driven per authorized employee by
	// the sync loop in Phase 8.
	calRepo := calendar.NewRepo(pool)
	calSvc := calendar.NewService(tokenMgr, calendar.NewAPIClient(cfg.Zoho.CalendarBase), calRepo, empRepo, auditLog)
	_ = calSvc // consumed by the worker sync loops (Phase 8)

	oauthHandler := httpx.NewOAuthHandler(httpx.OAuthDeps{
		Onboarding:   onbRepo,
		States:       stateRepo,
		Employees:    empRepo,
		Creds:        credStore,
		Client:       zohoClient,
		Audit:        auditLog,
		Templates:    tmpl,
		AccountsBase: cfg.Zoho.AccountsBase,
		CompanyName:  cfg.CompanyName,
		AppName:      cfg.AppName,
		Permissions:  cfg.ReadablePermissions(),
		SupportAddr:  cfg.SupportAddr,
		PrivacyURL:   cfg.PrivacyURL,
		TermsURL:     cfg.TermsURL,
		Security:     monitor,
		ConnectRL:    connectRL.Wrap,
		OAuthRL:      oauthRL.Wrap,
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
	httpx.NewFathomHandler(calRepo, auditLog).Register(mux)

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
	httpx.NewLoginHandler(loginFlow, mapper, sessions, sessionKey, cfg.IsProduction()).Register(mux)

	log.Printf("server listening on %s (env=%s)", cfg.HTTPAddr, cfg.AppEnv)
	log.Fatal(http.ListenAndServe(cfg.HTTPAddr, mux))
}
