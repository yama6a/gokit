// Package secretbox seals and opens small secrets with AES-256-GCM.
// A sealed value is the 12-byte random nonce followed by the ciphertext and tag.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

const keySize = 32

// Key is a raw AES-256 key.
type Key [keySize]byte

// ParseKey decodes a standard base64 string that holds exactly 32 bytes.
func ParseKey(b64 string) (Key, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return Key{}, fmt.Errorf("%w: invalid base64", ErrMalformedKey)
	}
	if len(raw) != keySize {
		return Key{}, fmt.Errorf("%w: want %d bytes, got %d", ErrMalformedKey, keySize, len(raw))
	}
	return Key(raw), nil
}

// Box seals and opens values with one key. It is safe for concurrent use.
type Box struct {
	gcm    cipher.AEAD
	random io.Reader
}

// New returns a Box that uses k.
func New(k Key) (*Box, error) {
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return &Box{gcm: gcm, random: rand.Reader}, nil
}

// Seal encrypts plaintext under a fresh random nonce and returns nonce || ciphertext.
func (b *Box) Seal(plaintext []byte) ([]byte, error) {
	n := b.gcm.NonceSize()
	out := make([]byte, n, n+len(plaintext)+b.gcm.Overhead())
	if _, err := io.ReadFull(b.random, out); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}
	return b.gcm.Seal(out, out, plaintext, nil), nil
}

// Open decrypts a value from Seal. It returns ErrDecrypt for a wrong key, a short input or tampered bytes.
func (b *Box) Open(sealed []byte) ([]byte, error) {
	n := b.gcm.NonceSize()
	if len(sealed) < n+b.gcm.Overhead() {
		return nil, ErrDecrypt
	}
	plaintext, err := b.gcm.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}
