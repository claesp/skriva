// Package secret encrypts small secrets for storage in the database. It
// uses AES-256-GCM.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// KeySize is the size of the key in bytes.
const KeySize = 32

// ErrDecrypt tells that the data or the key is not correct.
var ErrDecrypt = errors.New("cannot decrypt secret")

// Box encrypts and decrypts secrets with one key. It is safe for
// concurrent use.
type Box struct {
	aead cipher.AEAD
	id   string
}

// New makes a Box. The key must have KeySize bytes.
func New(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("key must have %d bytes, not %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// The ID is a part of a hash of the key. It identifies the key, but
	// it does not tell the key: the key is 32 random bytes.
	sum := sha256.Sum256(append([]byte("skriva-key-id:"), key...))
	return &Box{aead: aead, id: hex.EncodeToString(sum[:8])}, nil
}

// ID identifies the key of the Box.
func (b *Box) ID() string { return b.id }

// ParseKey decodes a base64 key.
func ParseKey(s string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("key is not valid base64: %w", err)
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("key must have %d bytes, not %d", KeySize, len(key))
	}
	return key, nil
}

// Seal encrypts plain. The result contains a random nonce. context is
// associated data: Open must get the same value. Use it to bind the
// secret to its record. Then a person who can write to the database
// cannot move a secret to a different record.
func (b *Box) Seal(plain []byte, context string) []byte {
	nonce := make([]byte, b.aead.NonceSize(), b.aead.NonceSize()+len(plain)+b.aead.Overhead())
	_, _ = rand.Read(nonce) // crypto/rand.Read does not return errors.
	return b.aead.Seal(nonce, nonce, plain, []byte(context))
}

// Open decrypts data from Seal.
func (b *Box) Open(data []byte, context string) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(data) < n+b.aead.Overhead() {
		return nil, ErrDecrypt
	}
	plain, err := b.aead.Open(nil, data[:n], data[n:], []byte(context))
	if err != nil {
		return nil, ErrDecrypt
	}
	return plain, nil
}
