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

	"github.com/arvinizadi/fathom/internal/alerts"
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
	"github.com/arvinizadi/fathom/internal/synccontrol"
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
	// Onboarding (invite e-mails) + admin alerts. When an employee's Zoho token
	// becomes permanently invalid, they are re-sent a connect link and the
	// admins are e-mailed (see internal/alerts).
	tmpl, err := web.Templates()
	if err != nil {
		log.Fatalf("worker: templates: %v", err)
	}
	onbResolve := wire.OnboardingResolver(appSettings, cfg)
	onbSvc := onboarding.NewService(onboarding.NewRepo(pool), empStore{empRepo}, onbResolve, auditLog, tmpl,
		cfg.PublicBaseURL, cfg.OnboardingTokenBytes, cfg.OnboardingTokenTTL)
	notifier := alerts.New(onbResolve, cfg.AdminEmails, cfg.PublicBaseURL)
	var runDisconnected []string // employees disconnected during the current sync run
	reconnector := &alerts.Reconnector{
		Invite:   onbSvc,
		Notifier: notifier,
		Lookup: func(ctx context.Context, org db.OrgID, id int64) (string, string, error) {
			e, err := empRepo.GetByID(ctx, org, id)
			if err != nil {
				return "", "", err
			}
			return e.Name, e.Email, nil
		},
		OnDisconnect: func(email string) { runDisconnected = append(runDisconnected, email) },
	}
	tokenMgr := tokens.NewManager(credStore, refresherFor, tokens.NewRedisLocker(redisClient), auditLog, reconnector)

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
	// restart; falls back to DIRECTORY_SYNC_INTERVAL. Runs immediately, then
	// waits — but an admin can trigger "Sync now" / "Full resync" from the app,
	// which is picked up within a few seconds (internal/synccontrol).
	log.Printf("worker: calendar sync loop started (interval=%s, overridable in Settings)", syncInterval(ctx, appSettings, cfg))
	destCheck := func(ctx context.Context, org db.OrgID) (string, error) {
		d, err := destStore.Get(ctx, org)
		if err != nil {
			return "", nil // no admin-connected calendar (env fallback or none): nothing to check
		}
		oc := googleOAuthFor(ctx, org)
		if oc == nil {
			return d.ConnectedEmail, nil
		}
		return d.ConnectedEmail, google.NewClient(cfg.GoogleCalendarBase, d.CalendarID, google.NewRefreshTokenSource(oc, d.RefreshToken)).CheckAuth(ctx)
	}
	trigger, full := "schedule", false
	for {
		runDisconnected = nil
		rep := syncAllEmployees(ctx, empRepo, calRepo, engine, redisClient, notifier, destCheck, trigger, full)
		rep.Disconnected = runDisconnected
		if err := synccontrol.SaveLast(ctx, redisClient.Client, rep); err != nil {
			log.Printf("sync run: save report: %v", err)
		}
		trigger, full = "schedule", false
		deadline := time.Now().Add(syncInterval(ctx, appSettings, cfg))
	wait:
		for time.Now().Before(deadline) {
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				break wait
			}
			if ok, f, err := synccontrol.Take(ctx, redisClient.Client); err == nil && ok {
				trigger, full = "manual", f
				break wait
			}
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
// Before syncing an org it checks that the org's Fathom (Google) calendar is
// still connected; if not, the org is skipped and the admins are e-mailed once
// per outage. A full resync first forces every upcoming meeting to be rewritten.
func syncAllEmployees(ctx context.Context, empRepo *directory.Repo, calRepo *calendar.Repo, engine *sync.Engine,
	rdb *redisx.Client, notifier *alerts.Notifier,
	destCheck func(ctx context.Context, org db.OrgID) (string, error), trigger string, full bool) synccontrol.RunReport {
	rep := synccontrol.RunReport{StartedAt: time.Now(), Trigger: trigger, Full: full}

	if full {
		n, err := calRepo.ForceResync(ctx)
		if err != nil {
			log.Printf("sync run: full resync: %v", err)
		}
		rep.Requeued = n
		log.Printf("sync run: full resync requested — %d upcoming meeting(s) will be rewritten", n)
	}

	emps, err := empRepo.ListAuthorized(ctx)
	if err != nil {
		log.Printf("sync run: %v", err)
		rep.FinishedAt = time.Now()
		return rep
	}

	// Destination health, once per org per run.
	destOK := map[db.OrgID]bool{}
	for _, e := range emps {
		if _, seen := destOK[e.Org]; seen {
			continue
		}
		account, err := destCheck(ctx, e.Org)
		if err != nil {
			destOK[e.Org] = false
			rep.SkippedOrgs++
			log.Printf("sync run: org=%d Fathom calendar disconnected: %v", e.Org, err)
			if isNew, serr := synccontrol.SetDestError(ctx, rdb.Client, int64(e.Org), err.Error()); serr == nil && isNew {
				notifier.DestinationDisconnected(ctx, e.Org, account, err.Error())
			}
			continue
		}
		destOK[e.Org] = true
		if wasFailing, _ := synccontrol.ClearDestError(ctx, rdb.Client, int64(e.Org)); wasFailing {
			notifier.DestinationRestored(ctx, e.Org)
		}
	}

	for _, e := range emps {
		if !destOK[e.Org] {
			continue
		}
		rep.Employees++
		r, err := engine.SyncEmployee(ctx, e.Org, e.EmployeeID)
		if err != nil {
			rep.Failed++
			log.Printf("sync employee=%d org=%d: %v", e.EmployeeID, e.Org, err)
			continue
		}
		rep.Created += r.Created
		rep.Updated += r.Updated
		rep.Cancelled += r.Cancelled
	}
	log.Printf("sync run (%s%s): employees=%d created=%d updated=%d cancelled=%d failed=%d skipped_orgs=%d",
		trigger, map[bool]string{true: ", full", false: ""}[full],
		rep.Employees, rep.Created, rep.Updated, rep.Cancelled, rep.Failed, rep.SkippedOrgs)
	rep.FinishedAt = time.Now()
	return rep
}
