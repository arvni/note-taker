package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func newTestCipher(t *testing.T) *Cipher {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	c, err := NewFromBase64Key(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRoundTrip(t *testing.T) {
	c := newTestCipher(t)
	secret := "1000.refresh-token-value.deadbeef"
	ct, err := c.EncryptString(secret)
	if err != nil {
		t.Fatal(err)
	}
	if ct == secret {
		t.Fatal("ciphertext equals plaintext")
	}
	got, err := c.DecryptString(ct)
	if err != nil {
		t.Fatal(err)
	}
	if got != secret {
		t.Fatalf("round-trip mismatch: got %q want %q", got, secret)
	}
}

func TestNonceIsRandom(t *testing.T) {
	c := newTestCipher(t)
	a, _ := c.EncryptString("same")
	b, _ := c.EncryptString("same")
	if a == b {
		t.Fatal("two encryptions of same plaintext produced identical ciphertext")
	}
}

func TestRejectsBadKey(t *testing.T) {
	if _, err := NewFromBase64Key(base64.StdEncoding.EncodeToString(make([]byte, 16))); err == nil {
		t.Fatal("expected error for 16-byte key")
	}
}

func TestTamperDetected(t *testing.T) {
	c := newTestCipher(t)
	ct, _ := c.EncryptString("secret")
	raw, _ := base64.StdEncoding.DecodeString(ct)
	raw[len(raw)-1] ^= 0xFF // flip a tag bit
	if _, err := c.DecryptString(base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("expected authentication failure on tampered ciphertext")
	}
}
