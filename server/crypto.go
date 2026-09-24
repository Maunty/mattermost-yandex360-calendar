package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Tokens are encrypted at rest so that read access to the database is not by
// itself read access to every connected person's calendar.
//
// The key comes from a generated plugin setting. It is a text setting rather
// than raw bytes, so it is hashed to a fixed-length key: this accepts whatever
// the System Console generated, and an administrator pasting something of
// their own, without either producing a key of the wrong size.

var errNoEncryptionKey = errors.New("no at-rest encryption key is configured")

func aeadFor(key string) (cipher.AEAD, error) {
	if key == "" {
		return nil, errNoEncryptionKey
	}
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// encrypt seals plaintext with the configured key. The nonce is prepended to
// the ciphertext, which is then encoded so it can live in the key-value store
// beside ordinary JSON.
func encrypt(key string, plaintext []byte) (string, error) {
	aead, err := aeadFor(key)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	sealed := aead.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// decrypt opens what encrypt sealed. A key that has been regenerated since the
// value was written fails here, which is the intended outcome: the Connection
// is unreadable and the person reconnects.
func decrypt(key string, encoded string) ([]byte, error) {
	aead, err := aeadFor(key)
	if err != nil {
		return nil, err
	}

	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("stored value is not readable: %w", err)
	}
	if len(sealed) < aead.NonceSize() {
		return nil, errors.New("stored value is too short to be encrypted")
	}

	nonce, ciphertext := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("stored value could not be decrypted, most likely because the encryption key changed: %w", err)
	}
	return plaintext, nil
}
