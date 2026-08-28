package tokens

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/oauth"
)

type fakeStore struct {
	mu       sync.Mutex
	cred     *Credential
	upserts  int
	statuses []string
}

func (f *fakeStore) Get(context.Context, int64) (*Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := *f.cred
	return &c, nil
}
func (f *fakeStore) Upsert(_ context.Context, c Credential) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts++
	f.cred.AccessToken = c.AccessToken
	f.cred.AccessExpiresAt = c.AccessExpiresAt
	return nil
}
func (f *fakeStore) SetStatus(_ context.Context, _ int64, s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses = append(f.statuses, s)
	f.cred.Status = s
	return nil
}

// setAccess mutates the credential under the lock (for concurrent test peers).
func (f *fakeStore) setAccess(tok string, exp time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cred.AccessToken = tok
	f.cred.AccessExpiresAt = exp
}

type fakeRefresher struct {
	resp *oauth.TokenResponse
	err  error
	n    int
}

func (f *fakeRefresher) Refresh(context.Context, string) (*oauth.TokenResponse, error) {
	f.n++
	return f.resp, f.err
}

// directLocker runs fn immediately (no contention).
type directLocker struct{ calls int }

func (l *directLocker) WithLock(_ context.Context, _ string, _ time.Duration, fn func() error) error {
	l.calls++
	return fn()
}

// busyLocker always reports the lock as held.
type busyLocker struct{}

func (busyLocker) WithLock(context.Context, string, time.Duration, func() error) error {
	return ErrLockBusy
}

type spyReconnect struct{ called int }

func (s *spyReconnect) Reconnect(context.Context, db.OrgID, int64) error { s.called++; return nil }

func refFor(r Refresher) RefresherFor {
	return func(context.Context, db.OrgID) (Refresher, error) { return r, nil }
}

func activeCred(expiresIn time.Duration) *Credential {
	return &Credential{EmployeeID: 1, AccessToken: "old-acc", RefreshToken: "ref", Status: StatusActive,
		AccessExpiresAt: time.Now().Add(expiresIn)}
}

func TestAccessToken_FreshNoRefresh(t *testing.T) {
	store := &fakeStore{cred: activeCred(30 * time.Minute)}
	ref := &fakeRefresher{}
	m := NewManager(store, refFor(ref), &directLocker{}, nil, nil)
	tok, err := m.AccessToken(context.Background(), 1, 1)
	if err != nil || tok != "old-acc" {
		t.Fatalf("got %q err=%v, want old-acc", tok, err)
	}
	if ref.n != 0 {
		t.Fatal("refresh called though token was fresh (spec §15)")
	}
}

func TestAccessToken_RefreshesNearExpiry(t *testing.T) {
	store := &fakeStore{cred: activeCred(1 * time.Minute)}
	ref := &fakeRefresher{resp: &oauth.TokenResponse{AccessToken: "new-acc", ExpiresIn: 3600}}
	m := NewManager(store, refFor(ref), &directLocker{}, nil, nil)
	tok, err := m.AccessToken(context.Background(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "new-acc" {
		t.Fatalf("got %q, want new-acc", tok)
	}
	if ref.n != 1 || store.upserts != 1 {
		t.Fatalf("expected 1 refresh + 1 upsert, got n=%d upserts=%d", ref.n, store.upserts)
	}
}

func TestAccessToken_InvalidRefreshMarksRevokedAndReconnects(t *testing.T) {
	store := &fakeStore{cred: activeCred(1 * time.Minute)}
	ref := &fakeRefresher{err: &oauth.TokenError{Code: "invalid_code"}}
	rc := &spyReconnect{}
	m := NewManager(store, refFor(ref), &directLocker{}, nil, rc)
	_, err := m.AccessToken(context.Background(), 1, 1)
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("got %v, want ErrRevoked", err)
	}
	if len(store.statuses) != 1 || store.statuses[0] != StatusRevoked {
		t.Fatalf("credential not marked revoked: %v", store.statuses)
	}
	if rc.called != 1 {
		t.Fatal("reconnect not triggered (spec §18)")
	}
}

func TestAccessToken_TransientErrorDoesNotRevoke(t *testing.T) {
	store := &fakeStore{cred: activeCred(1 * time.Minute)}
	ref := &fakeRefresher{err: fmt.Errorf("connection reset")}
	rc := &spyReconnect{}
	m := NewManager(store, refFor(ref), &directLocker{}, nil, rc)
	_, err := m.AccessToken(context.Background(), 1, 1)
	if err == nil {
		t.Fatal("expected transient error")
	}
	if errors.Is(err, ErrRevoked) {
		t.Fatal("transient error must not revoke (spec §18)")
	}
	if len(store.statuses) != 0 || rc.called != 0 {
		t.Fatalf("transient error wrongly revoked/reconnected: statuses=%v reconnect=%d", store.statuses, rc.called)
	}
}

func TestAccessToken_LockBusyReadsPeerResult(t *testing.T) {
	// Peer already refreshed: store returns a fresh token; our lock is busy.
	store := &fakeStore{cred: &Credential{EmployeeID: 1, AccessToken: "peer-acc", RefreshToken: "ref",
		Status: StatusActive, AccessExpiresAt: time.Now().Add(30 * time.Minute)}}
	// Force the refresh path by making expiry near, but lock busy.
	store.setAccess("peer-acc", time.Now().Add(1*time.Minute))
	ref := &fakeRefresher{}
	m := NewManager(store, refFor(ref), busyLocker{}, nil, nil)
	// After the short wait, simulate peer having refreshed by extending expiry.
	go func() {
		time.Sleep(50 * time.Millisecond)
		store.setAccess("peer-fresh", time.Now().Add(30*time.Minute))
	}()
	tok, err := m.AccessToken(context.Background(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "peer-fresh" {
		t.Fatalf("got %q, want peer-fresh (should read peer's refreshed token)", tok)
	}
	if ref.n != 0 {
		t.Fatal("should not refresh itself when lock is busy")
	}
}
