package tokens

import (
	"context"
	"errors"

	"github.com/arvinizadi/fathom/internal/crypto"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/jackc/pgx/v5"
)

// GoogleDest holds a decrypted destination-calendar credential.
type GoogleDest struct {
	RefreshToken   string
	CalendarID     string
	ConnectedEmail string
}

// GoogleDestStore persists the destination Google Calendar credential per org,
// with the refresh token encrypted at rest (spec §13).
type GoogleDestStore struct {
	pool   *db.Pool
	cipher *crypto.Cipher
}

func NewGoogleDestStore(pool *db.Pool, cipher *crypto.Cipher) *GoogleDestStore {
	return &GoogleDestStore{pool: pool, cipher: cipher}
}

// ErrNoDest is returned when no destination credential is connected.
var ErrNoDest = errors.New("tokens: no google destination connected")

// Save stores (or replaces) the org's destination credential.
func (s *GoogleDestStore) Save(ctx context.Context, org db.OrgID, refreshToken, calendarID, email string) error {
	ct, err := s.cipher.EncryptString(refreshToken)
	if err != nil {
		return err
	}
	if calendarID == "" {
		calendarID = "primary"
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO google_destination (org_id, refresh_token_ciphertext, calendar_id, connected_email, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (org_id) DO UPDATE SET
			refresh_token_ciphertext = EXCLUDED.refresh_token_ciphertext,
			calendar_id = EXCLUDED.calendar_id, connected_email = EXCLUDED.connected_email, updated_at = now()`,
		int64(org), ct, calendarID, email)
	return err
}

// Get returns the org's decrypted destination credential.
func (s *GoogleDestStore) Get(ctx context.Context, org db.OrgID) (*GoogleDest, error) {
	var ct string
	var d GoogleDest
	err := s.pool.QueryRow(ctx, `
		SELECT refresh_token_ciphertext, calendar_id, coalesce(connected_email,'')
		FROM google_destination WHERE org_id = $1`, int64(org)).
		Scan(&ct, &d.CalendarID, &d.ConnectedEmail)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoDest
	}
	if err != nil {
		return nil, err
	}
	if d.RefreshToken, err = s.cipher.DecryptString(ct); err != nil {
		return nil, err
	}
	return &d, nil
}
