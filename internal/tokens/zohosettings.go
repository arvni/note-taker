package tokens

import (
	"context"
	"errors"

	"github.com/arvinizadi/fathom/internal/crypto"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/oauth"
	"github.com/jackc/pgx/v5"
)

// ZohoSettings is a decrypted per-org Zoho configuration.
type ZohoSettings struct {
	ClientID              string
	ClientSecret          string
	AccountsBase          string
	CalendarBase          string
	Scopes                string
	DirectoryUsersURL     string
	DirectoryRefreshToken string
}

// ZohoSettingsStore persists per-org Zoho config with secrets encrypted at rest.
type ZohoSettingsStore struct {
	pool   *db.Pool
	cipher *crypto.Cipher
}

func NewZohoSettingsStore(pool *db.Pool, cipher *crypto.Cipher) *ZohoSettingsStore {
	return &ZohoSettingsStore{pool: pool, cipher: cipher}
}

// ErrNoZohoSettings is returned when an org has no stored Zoho config.
var ErrNoZohoSettings = errors.New("tokens: no zoho settings for org")

// Save stores/updates an org's Zoho config (client secret + directory refresh
// token encrypted).
func (s *ZohoSettingsStore) Save(ctx context.Context, org db.OrgID, in ZohoSettings) error {
	secretCT, err := s.cipher.EncryptString(in.ClientSecret)
	if err != nil {
		return err
	}
	var dirCT any
	if in.DirectoryRefreshToken != "" {
		ct, err := s.cipher.EncryptString(in.DirectoryRefreshToken)
		if err != nil {
			return err
		}
		dirCT = ct
	}
	accounts := in.AccountsBase
	if accounts == "" {
		accounts = "https://accounts.zoho.com"
	}
	calBase := in.CalendarBase
	if calBase == "" {
		calBase = "https://calendar.zoho.com/api/v1"
	}
	scopes := in.Scopes
	if scopes == "" {
		scopes = "ZohoCalendar.calendar.READ,ZohoCalendar.event.READ,aaaserver.profile.READ"
	}
	var dirURL any
	if in.DirectoryUsersURL != "" {
		dirURL = in.DirectoryUsersURL
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO org_zoho_settings
			(org_id, client_id, client_secret_ciphertext, accounts_base, calendar_base, scopes,
			 directory_users_url, directory_refresh_ciphertext, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8, now())
		ON CONFLICT (org_id) DO UPDATE SET
			client_id = EXCLUDED.client_id, client_secret_ciphertext = EXCLUDED.client_secret_ciphertext,
			accounts_base = EXCLUDED.accounts_base, calendar_base = EXCLUDED.calendar_base,
			scopes = EXCLUDED.scopes, directory_users_url = EXCLUDED.directory_users_url,
			directory_refresh_ciphertext = COALESCE(EXCLUDED.directory_refresh_ciphertext, org_zoho_settings.directory_refresh_ciphertext),
			updated_at = now()`,
		int64(org), in.ClientID, secretCT, accounts, calBase, scopes, dirURL, dirCT)
	return err
}

// Get returns the org's decrypted Zoho config.
func (s *ZohoSettingsStore) Get(ctx context.Context, org db.OrgID) (*ZohoSettings, error) {
	var (
		out      ZohoSettings
		secretCT string
		dirURL   *string
		dirCT    *string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT client_id, client_secret_ciphertext, accounts_base, calendar_base, scopes,
		       directory_users_url, directory_refresh_ciphertext
		FROM org_zoho_settings WHERE org_id = $1`, int64(org)).
		Scan(&out.ClientID, &secretCT, &out.AccountsBase, &out.CalendarBase, &out.Scopes, &dirURL, &dirCT)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoZohoSettings
	}
	if err != nil {
		return nil, err
	}
	if out.ClientSecret, err = s.cipher.DecryptString(secretCT); err != nil {
		return nil, err
	}
	if dirURL != nil {
		out.DirectoryUsersURL = *dirURL
	}
	if dirCT != nil && *dirCT != "" {
		if out.DirectoryRefreshToken, err = s.cipher.DecryptString(*dirCT); err != nil {
			return nil, err
		}
	}
	return &out, nil
}

// OrgConfig resolves the org's oauth.OrgConfig with the given redirect URI.
func (s *ZohoSettingsStore) OrgConfig(ctx context.Context, org db.OrgID, redirectURI string) (oauth.OrgConfig, error) {
	zs, err := s.Get(ctx, org)
	if err != nil {
		return oauth.OrgConfig{}, err
	}
	return oauth.OrgConfig{
		ClientID: zs.ClientID, ClientSecret: zs.ClientSecret,
		AccountsBase: zs.AccountsBase, CalendarBase: zs.CalendarBase,
		RedirectURI: redirectURI, Scopes: oauth.SplitScopes(zs.Scopes),
	}, nil
}
