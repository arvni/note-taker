// Command worker runs background jobs: directory sync (spec §20), token refresh
// (spec §15-16), and calendar synchronization to Google/Fathom (spec §42). It
// shuts down gracefully on SIGINT/SIGTERM.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/arvinizadi/fathom/internal/audit"
	"github.com/arvinizadi/fathom/internal/calendar"
	"github.com/arvinizadi/fathom/internal/config"
	"github.com/arvinizadi/fathom/internal/crypto"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/directory"
	"github.com/arvinizadi/fathom/internal/email"
	"github.com/arvinizadi/fathom/internal/google"
	"github.com/arvinizadi/fathom/internal/oauth"
	"github.com/arvinizadi/fathom/internal/onboarding"
	"github.com/arvinizadi/fathom/internal/redisx"
	"github.com/arvinizadi/fathom/internal/retention"
	"github.com/arvinizadi/fathom/internal/sync"
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("worker: db: %v", err)
	}
	defer pool.Close()

	cipher, err := crypto.NewFromBase64Key(cfg.CryptoMasterKey)
	if err != nil {
		log.Fatalf("worker: crypto key: %v", err)
	}
	redisClient, err := redisx.Connect(ctx, cfg.RedisURL)
	if err != nil {
		log.Fatalf("worker: redis: %v", err)
	}
	defer redisClient.Close()

	auditLog := audit.New(audit.NewPostgresSink(pool))
	empRepo := directory.NewRepo(pool)
	credStore := tokens.NewStore(pool, cipher)
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
	tokenMgr := tokens.NewManager(credStore, refresherFor, tokens.NewRedisLocker(redisClient), auditLog, nil)

	calRepo := calendar.NewRepo(pool)
	calSvc := calendar.NewService(tokenMgr, calendarBaseFor, calRepo, empRepo, auditLog)

	// Destination: the Google Calendar that Fathom watches (spec §42). Prefer a
	// service-account key (JWT-bearer exchange); fall back to a static token.
	var gts google.TokenSource = google.StaticToken(cfg.GoogleAccessToken)
	if cfg.GoogleCredentialsFile != "" {
		keyJSON, err := os.ReadFile(cfg.GoogleCredentialsFile)
		if err != nil {
			log.Fatalf("worker: read Google credentials: %v", err)
		}
		gts, err = google.NewServiceAccountTokenSource(keyJSON, google.CalendarScope, cfg.GoogleSubject)
		if err != nil {
			log.Fatalf("worker: Google service account: %v", err)
		}
		log.Print("worker: using Google service-account credentials")
	}
	gcal := google.NewClient(cfg.GoogleCalendarBase, cfg.GoogleCalendarID, gts)
	engine := sync.NewEngine(calSvc, gcal, calRepo, auditLog)

	log.Printf("worker started (env=%s); sync interval=%s; purge interval=%s",
		cfg.AppEnv, cfg.DirectorySyncInterval, cfg.RetentionPurgeInterval)

	// Retention purge loop (spec §50) runs alongside the sync loop.
	purger := retention.NewPurger(pool, retention.Policy{
		AuditLog:      cfg.RetentionAudit,
		EventMappings: cfg.RetentionMappings,
		ExpiredTokens: 7 * 24 * time.Hour,
	})
	go directory.RunPeriodic(ctx, cfg.RetentionPurgeInterval, func(ctx context.Context) error {
		_, err := purger.Purge(ctx)
		return err
	})

	// Directory auto-sync (spec §20): pull org users from Zoho Directory and
	// reconcile (invite new, offboard inactive). Requires a directory users URL +
	// an org-level refresh token; otherwise employees onboard via CSV import.
	if cfg.ZohoDirectoryUsersURL != "" && cfg.ZohoDirectoryRefreshToken != "" {
		tmpl, err := web.Templates()
		if err != nil {
			log.Fatalf("worker: templates: %v", err)
		}
		var sender email.Sender = email.LogSender{}
		if cfg.SMTPHost != "" {
			sender = email.NewSMTPSender(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.EmailFrom)
		}
		onbSvc := onboarding.NewService(onboarding.NewRepo(pool), empStore{empRepo}, sender, auditLog, tmpl, onboarding.Config{
			BaseURL: cfg.PublicBaseURL, CompanyName: cfg.CompanyName, AppName: cfg.AppName,
			Permissions: cfg.ReadablePermissions(), SupportAddr: cfg.SupportAddr, PrivacyURL: cfg.PrivacyURL,
			FromAddr: cfg.EmailFrom, TokenBytes: cfg.OnboardingTokenBytes, TTL: cfg.OnboardingTokenTTL,
		})
		offboarder := tokens.NewOffboarder(credStore, zohoRevokerFor, empRepo, auditLog)
		reconciler := directory.NewReconciler(empRepo, onbSvc, offboarder)
		dirClient := directory.NewZohoClient(cfg.ZohoDirectoryUsersURL)
		// Refresh the directory token with the org's Zoho client (per-org config,
		// env fallback). Optional dedicated Self-Client creds override.
		tokenFn := func(ctx context.Context) (string, error) {
			var dirOAuth *oauth.Client
			if cfg.ZohoDirectoryClientID != "" {
				dirOAuth = oauth.NewClient(cfg.ZohoDirectoryClientID, cfg.ZohoDirectoryClientSecret, zohoRedirect, cfg.Zoho.AccountsBase, nil)
			} else if oc, err := zohoResolve(ctx, db.OrgID(cfg.AdminOrgID)); err == nil {
				dirOAuth = oc.Client()
			} else {
				return "", err
			}
			tr, err := dirOAuth.Refresh(ctx, cfg.ZohoDirectoryRefreshToken)
			if err != nil {
				return "", err
			}
			return tr.AccessToken, nil
		}
		autoSync := directory.NewAutoSync(dirClient, reconciler, tokenFn, db.OrgID(cfg.AdminOrgID))
		go directory.RunPeriodic(ctx, cfg.DirectorySyncInterval, func(ctx context.Context) error {
			_, err := autoSync.SyncOnce(ctx)
			return err
		})
		log.Print("worker: Zoho Directory auto-sync enabled")
	} else {
		log.Print("worker: directory auto-sync disabled (CSV import mode); set ZOHO_DIRECTORY_USERS_URL + ZOHO_DIRECTORY_REFRESH_TOKEN to enable")
	}

	directory.RunPeriodic(ctx, cfg.DirectorySyncInterval, func(ctx context.Context) error {
		return syncAllEmployees(ctx, empRepo, engine)
	})

	log.Print("worker stopped")
}

// syncAllEmployees runs the sync engine for every authorized employee, isolating
// per-employee failures so one bad account cannot stall the whole run (spec §42).
func syncAllEmployees(ctx context.Context, empRepo *directory.Repo, engine *sync.Engine) error {
	emps, err := empRepo.ListAuthorized(ctx)
	if err != nil {
		return err
	}
	var created, updated, cancelled int
	for _, e := range emps {
		rep, err := engine.SyncEmployee(ctx, e.Org, e.EmployeeID)
		if err != nil {
			log.Printf("sync employee=%d org=%d: %v", e.EmployeeID, e.Org, err)
			continue
		}
		created += rep.Created
		updated += rep.Updated
		cancelled += rep.Cancelled
	}
	if len(emps) > 0 {
		log.Printf("sync run: employees=%d created=%d updated=%d cancelled=%d", len(emps), created, updated, cancelled)
	}
	return nil
}
