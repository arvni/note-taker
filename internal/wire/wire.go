// Package wire holds small dependency-assembly helpers shared by the server and
// worker binaries (per-org settings resolution with env fallback).
package wire

import (
	"context"

	"github.com/arvinizadi/fathom/internal/config"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/email"
	"github.com/arvinizadi/fathom/internal/onboarding"
	"github.com/arvinizadi/fathom/internal/tokens"
)

func pick(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// OnboardingResolver builds the per-org email sender + branding, preferring the
// org's stored app settings and falling back to env config (spec §21-23).
func OnboardingResolver(store *tokens.AppSettingsStore, cfg *config.Config) onboarding.Resolver {
	return func(ctx context.Context, org db.OrgID) (email.Sender, onboarding.Brand) {
		var as tokens.AppSettings
		if got, err := store.Get(ctx, org); err == nil {
			as = *got
		}
		host := pick(as.SMTPHost, cfg.SMTPHost)
		from := pick(as.EmailFrom, cfg.EmailFrom)
		var sender email.Sender = email.LogSender{}
		if host != "" {
			sender = email.NewSMTPSender(host, pick(as.SMTPPort, cfg.SMTPPort),
				pick(as.SMTPUser, cfg.SMTPUser), pick(as.SMTPPass, cfg.SMTPPass), from)
		}
		return sender, onboarding.Brand{
			CompanyName: pick(as.CompanyName, cfg.CompanyName),
			AppName:     pick(as.AppName, cfg.AppName),
			SupportAddr: pick(as.SupportAddr, cfg.SupportAddr),
			PrivacyURL:  pick(as.PrivacyURL, cfg.PrivacyURL),
			FromAddr:    from,
			Permissions: cfg.ReadablePermissions(),
		}
	}
}

// FathomAPIKey returns the org's Fathom API key (stored settings, env fallback).
func FathomAPIKey(ctx context.Context, store *tokens.AppSettingsStore, org db.OrgID, envKey string) string {
	if got, err := store.Get(ctx, org); err == nil && got.FathomAPIKey != "" {
		return got.FathomAPIKey
	}
	return envKey
}

// FirefliesAPIKey resolves the org's Fireflies API key (stored settings first,
// env fallback).
func FirefliesAPIKey(ctx context.Context, store *tokens.AppSettingsStore, org db.OrgID, envKey string) string {
	if got, err := store.Get(ctx, org); err == nil && got.FirefliesAPIKey != "" {
		return got.FirefliesAPIKey
	}
	return envKey
}
