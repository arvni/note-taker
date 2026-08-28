package tokens

import (
	"context"
	"errors"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/jackc/pgx/v5"
)

// FathomWebhook is a registered Fathom webhook for an org.
type FathomWebhook struct {
	WebhookID      string
	DestinationURL string
}

// FathomWebhookStore persists the registered Fathom webhook per org.
type FathomWebhookStore struct{ pool *db.Pool }

func NewFathomWebhookStore(pool *db.Pool) *FathomWebhookStore { return &FathomWebhookStore{pool: pool} }

// ErrNoWebhook is returned when no webhook is registered.
var ErrNoWebhook = errors.New("tokens: no fathom webhook registered")

func (s *FathomWebhookStore) Save(ctx context.Context, org db.OrgID, webhookID, destURL string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO fathom_webhook (org_id, webhook_id, destination_url, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (org_id) DO UPDATE SET webhook_id = EXCLUDED.webhook_id,
			destination_url = EXCLUDED.destination_url, updated_at = now()`,
		int64(org), webhookID, destURL)
	return err
}

func (s *FathomWebhookStore) Get(ctx context.Context, org db.OrgID) (*FathomWebhook, error) {
	var wh FathomWebhook
	err := s.pool.QueryRow(ctx, `
		SELECT webhook_id, destination_url FROM fathom_webhook WHERE org_id = $1`, int64(org)).
		Scan(&wh.WebhookID, &wh.DestinationURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoWebhook
	}
	if err != nil {
		return nil, err
	}
	return &wh, nil
}
