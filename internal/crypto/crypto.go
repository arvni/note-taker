// Package crypto provides authenticated encryption (AES-256-GCM) for tokens at
// rest (spec §13). The master key is supplied by the caller from a secret
// manager / KMS / env secret and MUST NOT be stored in the database (spec §14).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Cipher encrypts and decrypts short secrets such as OAuth access/refresh
// tokens using AES-256-GCM. Ciphertext is returned base64-encoded with the
// nonce prepended.
type Cipher struct {
	aead cipher.AEAD
}

// NewFromBase64Key builds a Cipher from a base64-encoded 32-byte key.
func NewFromBase64Key(b64 string) (*Cipher, error) {
	if b64 == "" {
		return nil, errors.New("crypto: empty master key")
	}
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("crypto: decode key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: key must be 32 bytes for AES-256, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt returns base64(nonce || ciphertext || tag).
func (c *Cipher) Encrypt(plaintext []byte) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt.
func (c *Cipher) Decrypt(b64 string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("crypto: decode ciphertext: %w", err)
	}
	ns := c.aead.NonceSize()
	if len(raw) < ns {
		return nil, errors.New("crypto: ciphertext too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	return c.aead.Open(nil, nonce, ct, nil)
}

// EncryptString is a convenience wrapper over Encrypt.
func (c *Cipher) EncryptString(s string) (string, error) { return c.Encrypt([]byte(s)) }

// DecryptString is a convenience wrapper over Decrypt.
func (c *Cipher) DecryptString(b64 string) (string, error) {
	b, err := c.Decrypt(b64)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
