// Package seal encrypts the machine passwords and keys Remote keeps, with a
// key that lives beside the database (DATA_DIR/remote.key) and nowhere else.
// A database backup alone does not reveal them; keep the key with it.
package seal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const prefix = "v1:"

// Box seals and opens values with one key.
type Box struct{ aead cipher.AEAD }

// Load reads the key at path, creating a new random one (0600) when missing.
func Load(path string) (*Box, error) {
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
			return nil, fmt.Errorf("write key: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("read key: %w", err)
	} else {
		raw, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(key)))
		if derr != nil || len(raw) != 32 {
			return nil, fmt.Errorf("%s is not a Pocket Remote key", path)
		}
		key = raw
	}
	return New(key)
}

// New makes a box from a 32-byte key.
func New(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts a value ("" stays "").
func (b *Box) Seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.StdEncoding.EncodeToString(out), nil
}

// Open decrypts a sealed value ("" stays "").
func (b *Box) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	if !strings.HasPrefix(sealed, prefix) {
		return "", errors.New("not a sealed value")
	}
	raw, err := base64.StdEncoding.DecodeString(sealed[len(prefix):])
	if err != nil || len(raw) < b.aead.NonceSize() {
		return "", errors.New("broken sealed value")
	}
	n := b.aead.NonceSize()
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", errors.New("this value was sealed with another key (was remote.key replaced?)")
	}
	return string(plain), nil
}
