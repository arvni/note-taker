// Package oauth implements the Zoho server-side Authorization Code flow:
// CSRF state, authorize-URL construction, token exchange, and identity
// verification (spec §8-13, §25-26).
package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// GenerateState returns a cryptographically random state value and its hash.
// Only the hash is stored (spec §9); no employee ID/email is embedded in the
// value itself.
func GenerateState() (raw string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(raw))
	return raw, h[:], nil
}

// HashState computes the lookup hash for a presented state value.
func HashState(raw string) []byte {
	h := sha256.Sum256([]byte(raw))
	return h[:]
}
