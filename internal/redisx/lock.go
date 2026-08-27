package redisx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrNotAcquired is returned when the lock is already held by someone else.
var ErrNotAcquired = errors.New("redisx: lock not acquired")

// releaseScript releases a lock only if the caller still owns it, preventing a
// slow holder from deleting a lock another worker has since acquired.
var releaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
else
	return 0
end`)

// Lock is a held distributed lock. Release it when done.
type Lock struct {
	client *Client
	key    string
	token  string
}

// Acquire attempts to take the lock at key for ttl using SET NX PX (spec §16).
// Only one holder succeeds; others get ErrNotAcquired.
func (c *Client) Acquire(ctx context.Context, key string, ttl time.Duration) (*Lock, error) {
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	ok, err := c.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotAcquired
	}
	return &Lock{client: c, key: key, token: token}, nil
}

// Release frees the lock if still owned by this holder (safe, ownership-checked).
func (l *Lock) Release(ctx context.Context) error {
	return releaseScript.Run(ctx, l.client, []string{l.key}, l.token).Err()
}

// TokenRefreshKey is the lock key for an employee's Zoho token refresh (spec §16).
func TokenRefreshKey(employeeID int64) string {
	return "zoho-token-refresh:" + itoa(employeeID)
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
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
