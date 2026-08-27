package tokens

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/arvinizadi/fathom/internal/audit"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/oauth"
)

// Default refresh timing (spec §15: access tokens live ~1h).
const (
	DefaultRefreshSkew = 5 * time.Minute
	DefaultLockTTL     = 30 * time.Second
)

// ErrRevoked is returned when a credential is no longer usable and the employee
// must reconnect (spec §18).
var ErrRevoked = errors.New("tokens: credential revoked or invalid")

// CredStore is the persistence the Manager needs (implemented by *Store).
type CredStore interface {
	Get(ctx context.Context, employeeID int64) (*Credential, error)
	Upsert(ctx context.Context, c Credential) error
	SetStatus(ctx context.Context, employeeID int64, status string) error
}

// Refresher exchanges a refresh token for a new access token (implemented by
// *oauth.Client). It must NOT mint a new refresh token (spec §15).
type Refresher interface {
	Refresh(ctx context.Context, refreshToken string) (*oauth.TokenResponse, error)
}

// ErrLockBusy signals another worker holds the refresh lock (spec §16).
var ErrLockBusy = errors.New("tokens: refresh lock busy")

// Locker serializes refreshes across workers. WithLock runs fn while holding the
// named lock, returning ErrLockBusy if it is already held.
type Locker interface {
	WithLock(ctx context.Context, key string, ttl time.Duration, fn func() error) error
}

// Reconnector is invoked when a credential becomes permanently invalid, to ask
// the employee to reconnect (spec §18). Wired to onboarding invite in Phase 5.
type Reconnector interface {
	Reconnect(ctx context.Context, org db.OrgID, employeeID int64) error
}

// Manager provides valid access tokens, refreshing under a distributed lock and
// detecting revocation (spec §15-18).
type Manager struct {
	store       CredStore
	refresher   Refresher
	locker      Locker
	audit       *audit.Logger
	reconnect   Reconnector
	refreshSkew time.Duration
	lockTTL     time.Duration
}

func NewManager(store CredStore, refresher Refresher, locker Locker, auditLog *audit.Logger, reconnect Reconnector) *Manager {
	return &Manager{
		store: store, refresher: refresher, locker: locker, audit: auditLog, reconnect: reconnect,
		refreshSkew: DefaultRefreshSkew, lockTTL: DefaultLockTTL,
	}
}

// AccessToken returns a currently-valid access token for the employee,
// refreshing it under a per-employee lock if it is near expiry (spec §15-16).
// A permanently invalid refresh token marks the credential revoked and triggers
// a reconnect request (spec §18); the caller receives ErrRevoked.
func (m *Manager) AccessToken(ctx context.Context, org db.OrgID, employeeID int64) (string, error) {
	cred, err := m.store.Get(ctx, employeeID)
	if err != nil {
		return "", err
	}
	if cred.Status != StatusActive {
		return "", ErrRevoked
	}
	if time.Until(cred.AccessExpiresAt) > m.refreshSkew {
		return cred.AccessToken, nil // still fresh, no refresh (spec §15)
	}

	var newToken string
	lockErr := m.locker.WithLock(ctx, RefreshKey(employeeID), m.lockTTL, func() error {
		// Re-read under the lock: a peer may have refreshed while we waited.
		latest, err := m.store.Get(ctx, employeeID)
		if err != nil {
			return err
		}
		if latest.Status != StatusActive {
			return ErrRevoked
		}
		if time.Until(latest.AccessExpiresAt) > m.refreshSkew {
			newToken = latest.AccessToken
			return nil
		}
		return m.refresh(ctx, org, latest, &newToken)
	})

	if errors.Is(lockErr, ErrLockBusy) {
		// Another worker is refreshing; briefly wait then read their result.
		time.Sleep(300 * time.Millisecond)
		latest, err := m.store.Get(ctx, employeeID)
		if err != nil {
			return "", err
		}
		if latest.Status != StatusActive {
			return "", ErrRevoked
		}
		return latest.AccessToken, nil
	}
	if lockErr != nil {
		return "", lockErr
	}
	return newToken, nil
}

// refresh performs the actual token refresh and persists the result, or marks
// the credential revoked on a permanent failure (spec §15, §18).
func (m *Manager) refresh(ctx context.Context, org db.OrgID, cred *Credential, out *string) error {
	tr, err := m.refresher.Refresh(ctx, cred.RefreshToken)
	if err != nil {
		var te *oauth.TokenError
		if errors.As(err, &te) && te.IsInvalidGrant() {
			// Permanent: the refresh token is dead (spec §18).
			_ = m.store.SetStatus(ctx, cred.EmployeeID, StatusRevoked)
			m.log(ctx, org, cred.EmployeeID, audit.TokenRefreshFailed, map[string]any{"permanent": true})
			if m.reconnect != nil {
				if rerr := m.reconnect.Reconnect(ctx, org, cred.EmployeeID); rerr != nil {
					log.Printf("tokens: reconnect trigger failed: %v", rerr)
				}
			}
			return ErrRevoked
		}
		// Transient: leave the credential active and let the caller retry later.
		m.log(ctx, org, cred.EmployeeID, audit.TokenRefreshFailed, map[string]any{"permanent": false})
		return err
	}

	cred.AccessToken = tr.AccessToken
	cred.AccessExpiresAt = tr.ExpiresAt()
	if tr.APIDomain != "" {
		cred.APIDomain = tr.APIDomain
	}
	if err := m.store.Upsert(ctx, *cred); err != nil {
		return err
	}
	*out = tr.AccessToken
	m.log(ctx, org, cred.EmployeeID, audit.TokenRefreshed, nil)
	return nil
}

func (m *Manager) log(ctx context.Context, org db.OrgID, empID int64, action audit.Action, md map[string]any) {
	if m.audit == nil {
		return
	}
	_ = m.audit.Log(ctx, audit.Entry{OrganizationID: int64(org), EmployeeID: empID, Action: action, Metadata: md})
}

// RefreshKey is the per-employee refresh lock key (spec §16).
func RefreshKey(employeeID int64) string {
	return "zoho-token-refresh:" + itoa(employeeID)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
