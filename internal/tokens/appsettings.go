package tokens

import (
	"context"
	"errors"

	"github.com/arvinizadi/fathom/internal/crypto"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/jackc/pgx/v5"
)

// AppSettings is per-org application config entered in the UI (email, branding,
// Fathom). Secret fields are decrypted on read.
type AppSettings struct {
	SMTPHost, SMTPPort, SMTPUser, SMTPPass, EmailFrom       string
	CompanyName, AppName, SupportAddr, PrivacyURL, TermsURL string
	FathomAPIKey                                            string
	FirefliesAPIKey                                         string
	GoogleOAuthClientID, GoogleOAuthClientSecret            string
	SyncInterval                                            string
}

// AppSettingsStore persists per-org app settings with secrets encrypted.
type AppSettingsStore struct {
	pool   *db.Pool
	cipher *crypto.Cipher
}

func NewAppSettingsStore(pool *db.Pool, cipher *crypto.Cipher) *AppSettingsStore {
	return &AppSettingsStore{pool: pool, cipher: cipher}
}

// ErrNoAppSettings is returned when an org has no stored app settings.
var ErrNoAppSettings = errors.New("tokens: no app settings for org")

// Get returns the org's decrypted settings.
func (s *AppSettingsStore) Get(ctx context.Context, org db.OrgID) (*AppSettings, error) {
	var (
		a                                           AppSettings
		smtpPassCT, fathomKeyCT, ffKeyCT, gSecretCT *string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT coalesce(smtp_host,''), coalesce(smtp_port,''), coalesce(smtp_user,''), smtp_pass_ciphertext,
		       coalesce(email_from,''), coalesce(company_name,''), coalesce(app_name,''), coalesce(support_addr,''),
		       coalesce(privacy_url,''), coalesce(terms_url,''), fathom_api_key_ciphertext, fireflies_api_key_ciphertext,
		       coalesce(google_oauth_client_id,''), google_oauth_secret_ciphertext,
		       coalesce(sync_interval,'')
		FROM org_app_settings WHERE org_id = $1`, int64(org)).
		Scan(&a.SMTPHost, &a.SMTPPort, &a.SMTPUser, &smtpPassCT, &a.EmailFrom, &a.CompanyName, &a.AppName,
			&a.SupportAddr, &a.PrivacyURL, &a.TermsURL, &fathomKeyCT, &ffKeyCT, &a.GoogleOAuthClientID, &gSecretCT, &a.SyncInterval)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoAppSettings
	}
	if err != nil {
		return nil, err
	}
	if smtpPassCT != nil && *smtpPassCT != "" {
		if a.SMTPPass, err = s.cipher.DecryptString(*smtpPassCT); err != nil {
			return nil, err
		}
	}
	if fathomKeyCT != nil && *fathomKeyCT != "" {
		if a.FathomAPIKey, err = s.cipher.DecryptString(*fathomKeyCT); err != nil {
			return nil, err
		}
	}
	if gSecretCT != nil && *gSecretCT != "" {
		if a.GoogleOAuthClientSecret, err = s.cipher.DecryptString(*gSecretCT); err != nil {
			return nil, err
		}
	}
	if ffKeyCT != nil && *ffKeyCT != "" {
		if a.FirefliesAPIKey, err = s.cipher.DecryptString(*ffKeyCT); err != nil {
			return nil, err
		}
	}
	return &a, nil
}

// Save stores/updates the org's settings; blank secrets keep the current value.
func (s *AppSettingsStore) Save(ctx context.Context, org db.OrgID, in AppSettings) error {
	enc := func(v string) (any, error) {
		if v == "" {
			return nil, nil
		}
		ct, err := s.cipher.EncryptString(v)
		return ct, err
	}
	smtpPass, err := enc(in.SMTPPass)
	if err != nil {
		return err
	}
	fathomKey, err := enc(in.FathomAPIKey)
	if err != nil {
		return err
	}
	gSecret, err := enc(in.GoogleOAuthClientSecret)
	if err != nil {
		return err
	}
	ffKey, err := enc(in.FirefliesAPIKey)
	if err != nil {
		return err
	}
	nz := func(v string) any {
		if v == "" {
			return nil
		}
		return v
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO org_app_settings
			(org_id, smtp_host, smtp_port, smtp_user, smtp_pass_ciphertext, email_from,
			 company_name, app_name, support_addr, privacy_url, terms_url, fathom_api_key_ciphertext, fireflies_api_key_ciphertext,
			 google_oauth_client_id, google_oauth_secret_ciphertext, sync_interval, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16, now())
		ON CONFLICT (org_id) DO UPDATE SET
			smtp_host = EXCLUDED.smtp_host, smtp_port = EXCLUDED.smtp_port, smtp_user = EXCLUDED.smtp_user,
			smtp_pass_ciphertext = COALESCE(EXCLUDED.smtp_pass_ciphertext, org_app_settings.smtp_pass_ciphertext),
			email_from = EXCLUDED.email_from, company_name = EXCLUDED.company_name, app_name = EXCLUDED.app_name,
			support_addr = EXCLUDED.support_addr, privacy_url = EXCLUDED.privacy_url, terms_url = EXCLUDED.terms_url,
			fathom_api_key_ciphertext = COALESCE(EXCLUDED.fathom_api_key_ciphertext, org_app_settings.fathom_api_key_ciphertext),
			fireflies_api_key_ciphertext = COALESCE(EXCLUDED.fireflies_api_key_ciphertext, org_app_settings.fireflies_api_key_ciphertext),
			google_oauth_client_id = EXCLUDED.google_oauth_client_id,
			google_oauth_secret_ciphertext = COALESCE(EXCLUDED.google_oauth_secret_ciphertext, org_app_settings.google_oauth_secret_ciphertext),
			sync_interval = EXCLUDED.sync_interval,
			updated_at = now()`,
		int64(org), nz(in.SMTPHost), nz(in.SMTPPort), nz(in.SMTPUser), smtpPass, nz(in.EmailFrom),
		nz(in.CompanyName), nz(in.AppName), nz(in.SupportAddr), nz(in.PrivacyURL), nz(in.TermsURL), fathomKey, ffKey,
		nz(in.GoogleOAuthClientID), gSecret, nz(in.SyncInterval))
	return err
}
