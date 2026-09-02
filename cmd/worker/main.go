// Command worker runs background jobs: directory sync (spec §20), token refresh
// (spec §15-16), and calendar synchronization to Google/Fathom (spec §42). It
// shuts down gracefully on SIGINT/SIGTERM.
package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
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
	"github.com/arvinizadi/fathom/internal/fathom"
	"github.com/arvinizadi/fathom/internal/google"
	"github.com/arvinizadi/fathom/internal/oauth"
	"github.com/arvinizadi/fathom/internal/onboarding"
	"github.com/arvinizadi/fathom/internal/redisx"
	"github.com/arvinizadi/fathom/internal/retention"
	"github.com/arvinizadi/fathom/internal/sync"
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
	appSettings := tokens.NewAppSettingsStore(pool, cipher)
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
	calSvc := calendar.NewService(tokenMgr, calendarBaseFor, calRepo, empRepo, auditLog, cfg.SyncAllEvents)

	// Destination: the Google Calendar that Fathom watches (spec §42). Resolved
	// per org: prefer the admin-connected calendar (OAuth, stored per org, entered
	// in the UI); fall back to an env service-account/static token.
	destStore := tokens.NewGoogleDestStore(pool, cipher)
	googleRedirect := cfg.GoogleOAuthRedirectURL
	if googleRedirect == "" {
		googleRedirect = cfg.PublicBaseURL + "/oauth/google/callback"
	}
	googleOAuthFor := func(ctx context.Context, org db.OrgID) *google.OAuthClient {
		if a, err := appSettings.Get(ctx, org); err == nil && a.GoogleOAuthClientID != "" && a.GoogleOAuthClientSecret != "" {
			return google.NewOAuthClient(a.GoogleOAuthClientID, a.GoogleOAuthClientSecret, googleRedirect)
		}
		if cfg.GoogleOAuthClientID != "" && cfg.GoogleOAuthClientSecret != "" {
			return google.NewOAuthClient(cfg.GoogleOAuthClientID, cfg.GoogleOAuthClientSecret, googleRedirect)
		}
		return nil
	}
	var envTS google.TokenSource = google.StaticToken(cfg.GoogleAccessToken)
	if cfg.GoogleCredentialsFile != "" {
		keyJSON, err := os.ReadFile(cfg.GoogleCredentialsFile)
		if err != nil {
			log.Fatalf("worker: read Google credentials: %v", err)
		}
		envTS, err = google.NewServiceAccountTokenSource(keyJSON, google.CalendarScope, cfg.GoogleSubject)
		if err != nil {
			log.Fatalf("worker: Google service account: %v", err)
		}
		log.Print("worker: using Google service-account credentials")
	}
	destFor := func(ctx context.Context, org db.OrgID) (sync.Destination, error) {
		if d, err := destStore.Get(ctx, org); err == nil {
			if oc := googleOAuthFor(ctx, org); oc != nil {
				ts := google.NewRefreshTokenSource(oc, d.RefreshToken)
				return google.NewClient(cfg.GoogleCalendarBase, d.CalendarID, ts), nil
			}
		}
		if cfg.GoogleCalendarID != "" && (cfg.GoogleCredentialsFile != "" || cfg.GoogleAccessToken != "") {
			return google.NewClient(cfg.GoogleCalendarBase, cfg.GoogleCalendarID, envTS), nil
		}
		return nil, fmt.Errorf("no Google destination configured for org %d", org)
	}
	engine := sync.NewEngine(calSvc, destFor, calRepo, auditLog)

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
		resolve := wire.OnboardingResolver(appSettings, cfg)
		onbSvc := onboarding.NewService(onboarding.NewRepo(pool), empStore{empRepo}, resolve, auditLog, tmpl,
			cfg.PublicBaseURL, cfg.OnboardingTokenBytes, cfg.OnboardingTokenTTL)
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

	// Fathom polling backup (spec §42): the webhook is primary, but if one is
	// missed (delivery failure, downtime) this periodically pulls recorded
	// meetings from the Fathom API and links any that aren't linked yet. Keyed
	// per admin org (stored settings -> env fallback); disabled when no key or
	// interval <= 0.
	if cfg.FathomPollInterval > 0 {
		go directory.RunPeriodic(ctx, cfg.FathomPollInterval, func(ctx context.Context) error {
			return pollFathomRecordings(ctx, appSettings, cfg, calRepo)
		})
		log.Printf("worker: Fathom polling backup enabled (interval=%s)", cfg.FathomPollInterval)
	}

	// Calendar sync loop. The cadence is configurable in the app (Settings →
	// sync interval), re-read each cycle so a change takes effect without a
	// restart; falls back to DIRECTORY_SYNC_INTERVAL. Runs immediately, then waits.
	log.Printf("worker: calendar sync loop started (interval=%s, overridable in Settings)", syncInterval(ctx, appSettings, cfg))
	for {
		if err := syncAllEmployees(ctx, empRepo, engine); err != nil {
			log.Printf("sync run: %v", err)
		}
		select {
		case <-time.After(syncInterval(ctx, appSettings, cfg)):
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}

	log.Print("worker stopped")
}

// syncInterval resolves the calendar-sync cadence: the admin org's configured
// value from the app (Settings), else DIRECTORY_SYNC_INTERVAL. Values below 1m
// are ignored to avoid hammering the upstream APIs.
func syncInterval(ctx context.Context, appSettings *tokens.AppSettingsStore, cfg *config.Config) time.Duration {
	if a, err := appSettings.Get(ctx, db.OrgID(cfg.AdminOrgID)); err == nil && a.SyncInterval != "" {
		if d, err := time.ParseDuration(a.SyncInterval); err == nil && d >= time.Minute {
			return d
		}
	}
	return cfg.DirectorySyncInterval
}

// pollFathomRecordings pulls recent recorded meetings from the Fathom API and
// links any to their synced calendar meeting that the webhook didn't already
// link (idempotent via LinkRecordingIfAbsent). It is a backup, not the primary
// path — the webhook delivers in real time (spec §42).
func pollFathomRecordings(ctx context.Context, appSettings *tokens.AppSettingsStore, cfg *config.Config, calRepo *calendar.Repo) error {
	key := wire.FathomAPIKey(ctx, appSettings, db.OrgID(cfg.AdminOrgID), cfg.FathomAPIKey)
	if key == "" {
		return nil // no Fathom key configured yet
	}
	client := fathom.NewClient(cfg.FathomAPIBase, key)
	meetings, err := client.ListMeetings(ctx, url.Values{})
	if err != nil {
		return err
	}
	linked := 0
	for _, m := range meetings {
		if m.MeetingURL == "" {
			continue
		}
		// Presence in the list means Fathom recorded it; link if not already.
		n, err := calRepo.LinkRecordingIfAbsent(ctx, m.MeetingURL, m.ID, m.RecordingURL, false, false)
		if err != nil {
			log.Printf("fathom poll: link %s: %v", m.MeetingURL, err)
			continue
		}
		linked += int(n)
	}
	if linked > 0 {
		log.Printf("fathom poll: linked %d recording(s) the webhook missed (scanned %d)", linked, len(meetings))
	}
	return nil
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
