// Package tokens stores and manages Zoho OAuth credentials. Access and refresh
// tokens are encrypted at rest with AES-256-GCM (spec §13); the plaintext never
// touches the database and is never logged (spec §35, §57).
package tokens

import (
	"context"
	"errors"
	"time"

	"github.com/arvinizadi/fathom/internal/crypto"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/jackc/pgx/v5"
)

// Status values for a stored credential.
const (
	StatusActive  = "active"
	StatusRevoked = "revoked"
	StatusInvalid = "invalid"
)

// ErrNotFound is returned when no credential exists for an employee.
var ErrNotFound = errors.New("tokens: credential not found")

// Credential is a decrypted view of a stored OAuth credential. Callers must not
// log the token fields (spec §57).
type Credential struct {
	EmployeeID        int64
	ProviderAccountID string
	AccessToken       string
	RefreshToken      string
	AccessExpiresAt   time.Time
	Scopes            []string
	APIDomain         string
	Status            string
}

// Store persists encrypted credentials.
type Store struct {
	pool   *db.Pool
	cipher *crypto.Cipher
}

func NewStore(pool *db.Pool, cipher *crypto.Cipher) *Store {
	return &Store{pool: pool, cipher: cipher}
}

// Upsert encrypts and stores a credential for an employee, replacing any prior
// Zoho credential (one active credential per employee, spec §13).
func (s *Store) Upsert(ctx context.Context, c Credential) error {
	accCT, err := s.cipher.EncryptString(c.AccessToken)
	if err != nil {
		return err
	}
	refCT, err := s.cipher.EncryptString(c.RefreshToken)
	if err != nil {
		return err
	}
	status := c.Status
	if status == "" {
		status = StatusActive
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO oauth_credentials
			(employee_id, provider, provider_account_id, access_token_ciphertext,
			 refresh_token_ciphertext, access_token_expires_at, scopes, api_domain, status, last_refresh_at)
		VALUES ($1, 'zoho', $2, $3, $4, $5, $6, $7, $8, now())
		ON CONFLICT (employee_id, provider) DO UPDATE SET
			provider_account_id      = EXCLUDED.provider_account_id,
			access_token_ciphertext  = EXCLUDED.access_token_ciphertext,
			refresh_token_ciphertext = EXCLUDED.refresh_token_ciphertext,
			access_token_expires_at  = EXCLUDED.access_token_expires_at,
			scopes                   = EXCLUDED.scopes,
			api_domain               = EXCLUDED.api_domain,
			status                   = EXCLUDED.status,
			last_refresh_at          = now(),
			revoked_at               = NULL,
			updated_at               = now()`,
		c.EmployeeID, c.ProviderAccountID, accCT, refCT, c.AccessExpiresAt,
		c.Scopes, c.APIDomain, status)
	return err
}

// Get returns and decrypts the credential for an employee.
func (s *Store) Get(ctx context.Context, employeeID int64) (*Credential, error) {
	var (
		accCT, refCT string
		c            Credential
	)
	err := s.pool.QueryRow(ctx, `
		SELECT employee_id, coalesce(provider_account_id,''), access_token_ciphertext,
		       refresh_token_ciphertext, coalesce(access_token_expires_at, now()),
		       scopes, coalesce(api_domain,''), status
		FROM oauth_credentials
		WHERE employee_id = $1 AND provider = 'zoho'`, employeeID).
		Scan(&c.EmployeeID, &c.ProviderAccountID, &accCT, &refCT,
			&c.AccessExpiresAt, &c.Scopes, &c.APIDomain, &c.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if c.AccessToken, err = s.cipher.DecryptString(accCT); err != nil {
		return nil, err
	}
	if c.RefreshToken, err = s.cipher.DecryptString(refCT); err != nil {
		return nil, err
	}
	return &c, nil
}

// SetStatus updates a credential's status (e.g. revoked/invalid) (spec §17-18).
func (s *Store) SetStatus(ctx context.Context, employeeID int64, status string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE oauth_credentials
		SET status = $1, revoked_at = CASE WHEN $1 = 'revoked' THEN now() ELSE revoked_at END,
		    updated_at = now()
		WHERE employee_id = $2 AND provider = 'zoho'`, status, employeeID)
	return err
}
