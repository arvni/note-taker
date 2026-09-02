// Package config loads typed application configuration from the environment.
// Secrets (client secret, crypto key) are read from env / secret manager and
// never persisted to the database (spec §12, §14).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppEnv        string
	HTTPAddr      string
	PublicBaseURL string

	DatabaseURL string
	RedisURL    string

	CryptoMasterKey string // base64 32-byte key; in prod sourced from KMS/Vault (§14)

	Zoho ZohoConfig

	OnboardingTokenTTL   time.Duration
	OnboardingTokenBytes int

	EmailFrom     string
	EmailProvider string

	DirectorySyncInterval time.Duration
	SyncAllEvents         bool

	CompanyName string
	AppName     string
	SupportAddr string
	PrivacyURL  string
	TermsURL    string

	GoogleCalendarBase      string
	GoogleCalendarID        string
	GoogleAccessToken       string
	GoogleCredentialsFile   string
	GoogleSubject           string
	GoogleOAuthClientID     string
	GoogleOAuthClientSecret string
	GoogleOAuthRedirectURL  string

	FathomAPIBase       string
	FathomAPIKey        string
	FathomWebhookSecret string
	FathomPollInterval  time.Duration

	SessionKey string
	SessionTTL time.Duration

	RateLimitOAuth   int
	RateLimitConnect int
	RateLimitAPI     int
	RateLimitWindow  time.Duration

	RetentionPurgeInterval time.Duration
	RetentionAudit         time.Duration
	RetentionMappings      time.Duration

	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCRedirectURL  string
	OIDCHostedDomain string
	AdminEmails      []string
	AdminOrgID       int64

	SMTPHost string
	SMTPPort string
	SMTPUser string
	SMTPPass string

	ZohoDirectoryUsersURL     string
	ZohoDirectoryRefreshToken string
	ZohoDirectoryClientID     string
	ZohoDirectoryClientSecret string
}

type ZohoConfig struct {
	ClientID     string
	ClientSecret string // confidential — never sent to browser (§12)
	RedirectURI  string // must be pre-registered (§10)
	AccountsBase string
	CalendarBase string
	Scopes       []string // minimum privilege (§11, §53)
}

// Load reads configuration from the environment, applying defaults and
// validating required fields.
func Load() (*Config, error) {
	c := &Config{
		AppEnv:        env("APP_ENV", "development"),
		HTTPAddr:      env("HTTP_ADDR", ":8080"),
		PublicBaseURL: env("PUBLIC_BASE_URL", "http://localhost:8080"),
		DatabaseURL:   env("DATABASE_URL", ""),
		RedisURL:      env("REDIS_URL", ""),

		CryptoMasterKey: env("CRYPTO_MASTER_KEY", ""),

		Zoho: ZohoConfig{
			ClientID:     env("ZOHO_CLIENT_ID", ""),
			ClientSecret: env("ZOHO_CLIENT_SECRET", ""),
			RedirectURI:  env("ZOHO_REDIRECT_URI", ""),
			AccountsBase: env("ZOHO_ACCOUNTS_BASE", "https://accounts.zoho.com"),
			CalendarBase: env("ZOHO_CALENDAR_BASE", "https://calendar.zoho.com/api/v1"),
			Scopes:       splitCSV(env("ZOHO_SCOPES", "ZohoCalendar.calendar.READ,ZohoCalendar.event.READ,aaaserver.profile.READ")),
		},

		OnboardingTokenTTL:   dur("ONBOARDING_TOKEN_TTL", 168*time.Hour),
		OnboardingTokenBytes: intEnv("ONBOARDING_TOKEN_BYTES", 48),

		EmailFrom:     env("EMAIL_FROM", ""),
		EmailProvider: env("EMAIL_PROVIDER", ""),

		DirectorySyncInterval: dur("DIRECTORY_SYNC_INTERVAL", time.Hour),
		SyncAllEvents:         env("SYNC_ALL_EVENTS", "") == "1",

		CompanyName: env("COMPANY_NAME", "Your Company"),
		AppName:     env("APP_NAME", "Calendar Bridge"),
		SupportAddr: env("SUPPORT_ADDR", ""),
		PrivacyURL:  env("PRIVACY_URL", ""),
		TermsURL:    env("TERMS_URL", ""),

		GoogleCalendarBase:      env("GOOGLE_CALENDAR_BASE", "https://www.googleapis.com/calendar/v3"),
		GoogleCalendarID:        env("GOOGLE_CALENDAR_ID", ""),
		GoogleAccessToken:       env("GOOGLE_ACCESS_TOKEN", ""),
		GoogleCredentialsFile:   env("GOOGLE_CREDENTIALS_FILE", ""),
		GoogleSubject:           env("GOOGLE_SUBJECT", ""),
		GoogleOAuthClientID:     env("GOOGLE_OAUTH_CLIENT_ID", ""),
		GoogleOAuthClientSecret: env("GOOGLE_OAUTH_CLIENT_SECRET", ""),
		GoogleOAuthRedirectURL:  env("GOOGLE_OAUTH_REDIRECT_URL", ""),

		FathomAPIBase:       env("FATHOM_API_BASE", "https://api.fathom.ai/external/v1"),
		FathomAPIKey:        env("FATHOM_API_KEY", ""),
		FathomWebhookSecret: env("FATHOM_WEBHOOK_SECRET", ""),
		FathomPollInterval:  dur("FATHOM_POLL_INTERVAL", 15*time.Minute),

		SessionKey: env("SESSION_KEY", ""),
		SessionTTL: dur("SESSION_TTL", 30*time.Minute),

		RateLimitOAuth:   intEnv("RATE_LIMIT_OAUTH", 10),
		RateLimitConnect: intEnv("RATE_LIMIT_CONNECT", 20),
		RateLimitAPI:     intEnv("RATE_LIMIT_API", 60),
		RateLimitWindow:  dur("RATE_LIMIT_WINDOW", 10*time.Minute),

		RetentionPurgeInterval: dur("RETENTION_PURGE_INTERVAL", 24*time.Hour),
		RetentionAudit:         dur("RETENTION_AUDIT", 365*24*time.Hour),
		RetentionMappings:      dur("RETENTION_MAPPINGS", 365*24*time.Hour),

		OIDCIssuer:       env("OIDC_ISSUER", ""),
		OIDCClientID:     env("OIDC_CLIENT_ID", ""),
		OIDCClientSecret: env("OIDC_CLIENT_SECRET", ""),
		OIDCRedirectURL:  env("OIDC_REDIRECT_URL", ""),
		OIDCHostedDomain: env("OIDC_HOSTED_DOMAIN", ""),
		AdminEmails:      splitCSV(env("ADMIN_EMAILS", "")),
		AdminOrgID:       int64(intEnv("ADMIN_ORG_ID", 1)),

		SMTPHost: env("SMTP_HOST", ""),
		SMTPPort: env("SMTP_PORT", "587"),
		SMTPUser: env("SMTP_USER", ""),
		SMTPPass: env("SMTP_PASS", ""),

		ZohoDirectoryUsersURL:     env("ZOHO_DIRECTORY_USERS_URL", ""),
		ZohoDirectoryRefreshToken: env("ZOHO_DIRECTORY_REFRESH_TOKEN", ""),
		ZohoDirectoryClientID:     env("ZOHO_DIRECTORY_CLIENT_ID", ""),
		ZohoDirectoryClientSecret: env("ZOHO_DIRECTORY_CLIENT_SECRET", ""),
	}

	// Guard: never allow Zoho Mail scopes to slip in (spec §33).
	for _, s := range c.Zoho.Scopes {
		if strings.HasPrefix(strings.ToLower(s), "zohomail") {
			return nil, fmt.Errorf("config: Zoho Mail scope %q is forbidden (spec §33)", s)
		}
		if strings.EqualFold(s, "ZohoCalendar.event.ALL") {
			return nil, fmt.Errorf("config: scope %q is over-broad; request granular scopes (spec §11)", s)
		}
	}

	return c, nil
}

func (c *Config) IsProduction() bool { return c.AppEnv == "production" }

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func intEnv(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func dur(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ReadablePermissions maps the requested Zoho scopes to human-readable
// descriptions for the consent email and page (spec §22-23).
func (c *Config) ReadablePermissions() []string {
	labels := map[string]string{
		"ZohoCalendar.calendar.READ": "Read your calendar list",
		"ZohoCalendar.event.READ":    "Read your calendar events",
		"ZohoCalendar.event.CREATE":  "Create calendar events",
		"ZohoCalendar.event.UPDATE":  "Update calendar events",
		"aaaserver.profile.READ":     "Read your basic Zoho profile (to verify your identity)",
	}
	out := make([]string, 0, len(c.Zoho.Scopes))
	for _, s := range c.Zoho.Scopes {
		if lbl, ok := labels[s]; ok {
			out = append(out, lbl)
		} else {
			out = append(out, s)
		}
	}
	return out
}
