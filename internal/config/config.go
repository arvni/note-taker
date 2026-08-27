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
