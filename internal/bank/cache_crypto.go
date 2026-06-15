package bank

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// cipherCodec seals/opens cache values with AES-256-GCM. Used by the valkey
// backend so sensitive financial data is encrypted at rest in the external
// store; the key never leaves this process.
type cipherCodec struct {
	aead cipher.AEAD
}

// newCipherCodec builds a codec from a base64-encoded 32-byte (AES-256) key.
func newCipherCodec(b64Key string) (*cipherCodec, error) {
	if b64Key == "" {
		return nil, errors.New("cache encryption is enabled but no encryption key is configured (set mcp.cache_encryption_key)")
	}
	key, err := base64.StdEncoding.DecodeString(b64Key)
	if err != nil {
		return nil, fmt.Errorf("decode cache encryption key (expected base64): %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("cache encryption key must be 32 bytes for AES-256, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &cipherCodec{aead: aead}, nil
}

// seal encrypts plaintext, returning nonce||ciphertext.
func (c *cipherCodec) seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// open decrypts nonce||ciphertext.
func (c *cipherCodec) open(data []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(data) < ns {
		return nil, errors.New("ciphertext shorter than nonce")
	}
	return c.aead.Open(nil, data[:ns], data[ns:], nil)
}

// NewEncryptionKey returns a fresh base64-encoded 32-byte AES-256 key, suitable
// for mcp.cache_encryption_key. Generated during onboarding.
func NewEncryptionKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}
