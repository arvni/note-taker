// Package onboarding manages the employee onboarding state machine and the
// high-entropy, single-use onboarding tokens used in consent links (spec §6-7).
package onboarding

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
)

// MinTokenBytes is the minimum entropy for onboarding tokens (spec §6).
const MinTokenBytes = 32

// GenerateToken returns a URL-safe raw token and its SHA-256 hash. Only the hash
// is ever persisted (spec §6); the raw value goes into the emailed link once.
func GenerateToken(nBytes int) (raw string, hash []byte, err error) {
	if nBytes < MinTokenBytes {
		return "", nil, fmt.Errorf("onboarding: token must be >= %d bytes (spec §6)", MinTokenBytes)
	}
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(raw))
	return raw, h[:], nil
}

// HashToken computes the lookup hash for a presented raw token.
func HashToken(raw string) []byte {
	h := sha256.Sum256([]byte(raw))
	return h[:]
}

// ConstantTimeEqual compares two hashes without leaking timing.
func ConstantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

var ErrTokenTooShort = errors.New("onboarding: token below minimum entropy")
