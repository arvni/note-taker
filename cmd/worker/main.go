// Command worker runs background jobs: directory sync (spec §20), token refresh
// (spec §15-16), and calendar synchronization to Google/Fathom (spec §42). It
// shuts down gracefully on SIGINT/SIGTERM.
package main

import (
	"context"
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
	zohoClient := oauth.NewClient(cfg.Zoho.ClientID, cfg.Zoho.ClientSecret,
		cfg.Zoho.RedirectURI, cfg.Zoho.AccountsBase, cfg.Zoho.Scopes)
	tokenMgr := tokens.NewManager(credStore, zohoClient, tokens.NewRedisLocker(redisClient), auditLog, nil)

	calRepo := calendar.NewRepo(pool)
	calSvc := calendar.NewService(tokenMgr, calendar.NewAPIClient(cfg.Zoho.CalendarBase), calRepo, empRepo, auditLog)

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
		offboarder := tokens.NewOffboarder(credStore, zohoClient, empRepo, auditLog)
		reconciler := directory.NewReconciler(empRepo, onbSvc, offboarder)
		dirClient := directory.NewZohoClient(cfg.ZohoDirectoryUsersURL)
		// The directory refresh token is issued by the Self Client, so it must be
		// refreshed with the Self Client's credentials (falls back to the main app).
		dirCID, dirSecret := cfg.ZohoDirectoryClientID, cfg.ZohoDirectoryClientSecret
		if dirCID == "" {
			dirCID, dirSecret = cfg.Zoho.ClientID, cfg.Zoho.ClientSecret
		}
		dirOAuth := oauth.NewClient(dirCID, dirSecret, cfg.Zoho.RedirectURI, cfg.Zoho.AccountsBase, nil)
		tokenFn := func(ctx context.Context) (string, error) {
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
