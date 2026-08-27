// Command worker runs background jobs: directory sync (spec §20), token refresh
// (spec §15-16), and calendar synchronization to Google/Fathom (spec §42). It
// shuts down gracefully on SIGINT/SIGTERM.
package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/arvinizadi/fathom/internal/audit"
	"github.com/arvinizadi/fathom/internal/calendar"
	"github.com/arvinizadi/fathom/internal/config"
	"github.com/arvinizadi/fathom/internal/crypto"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/directory"
	"github.com/arvinizadi/fathom/internal/google"
	"github.com/arvinizadi/fathom/internal/oauth"
	"github.com/arvinizadi/fathom/internal/redisx"
	"github.com/arvinizadi/fathom/internal/sync"
	"github.com/arvinizadi/fathom/internal/tokens"
)

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

	// Destination: the Google Calendar that Fathom watches (spec §42). The
	// service-account TokenSource is wired from GOOGLE credentials; until those
	// are configured a static token placeholder is used and sync is skipped.
	gcal := google.NewClient(cfg.GoogleCalendarBase, cfg.GoogleCalendarID, google.StaticToken(cfg.GoogleAccessToken))
	engine := sync.NewEngine(calSvc, gcal, calRepo, auditLog)

	log.Printf("worker started (env=%s); sync interval=%s", cfg.AppEnv, cfg.DirectorySyncInterval)

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
