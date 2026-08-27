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
	"github.com/arvinizadi/fathom/internal/config"
	"github.com/arvinizadi/fathom/internal/crypto"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/directory"
	"github.com/arvinizadi/fathom/internal/email"
	"github.com/arvinizadi/fathom/internal/httpx"
	"github.com/arvinizadi/fathom/internal/oauth"
	"github.com/arvinizadi/fathom/internal/onboarding"
	"github.com/arvinizadi/fathom/internal/redisx"
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

	auditLog := audit.New(audit.NewPostgresSink(pool))
	empRepo := directory.NewRepo(pool)
	onbRepo := onboarding.NewRepo(pool)
	stateRepo := oauth.NewStateRepo(pool)
	credStore := tokens.NewStore(pool, cipher)
	zohoClient := oauth.NewClient(cfg.Zoho.ClientID, cfg.Zoho.ClientSecret,
		cfg.Zoho.RedirectURI, cfg.Zoho.AccountsBase, cfg.Zoho.Scopes)

	// Onboarding service doubles as the reconnect trigger when a credential goes
	// invalid (spec §18, §45).
	onboardingSvc := onboarding.NewService(onbRepo, empStore{empRepo}, email.LogSender{}, auditLog, tmpl, onboarding.Config{
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
	_ = tokenMgr // used by sync jobs (Phase 6/8)

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
	httpx.NewAPIHandler(revoker).Register(mux)

	log.Printf("server listening on %s (env=%s)", cfg.HTTPAddr, cfg.AppEnv)
	log.Fatal(http.ListenAndServe(cfg.HTTPAddr, mux))
}
