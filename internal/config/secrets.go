package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	secretsFile = "secrets.enc"
	secretsKey  = "secrets.key"
)

// Secrets stores per-server SSH passwords encrypted at rest.
//
// The encryption key lives beside the ciphertext with 0600 permissions, so this
// protects against casual disclosure (backups, grepping a config directory,
// pasting config output) but not against an attacker who can read the whole
// config directory. Passwords are never written to config.yaml or logs.
type Secrets struct {
	path      string
	keyPath   string
	Passwords map[string]string
}

// LoadSecrets loads or creates the secrets store for the given config dir.
func LoadSecrets(dir string) (*Secrets, error) {
	s := &Secrets{
		path:      filepath.Join(dir, secretsFile),
		keyPath:   filepath.Join(dir, secretsKey),
		Passwords: map[string]string{},
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read secrets: %w", err)
	}
	key, err := os.ReadFile(s.keyPath)
	if err != nil {
		return nil, fmt.Errorf("read secrets key: %w", err)
	}
	plain, err := decrypt(key, data)
	if err != nil {
		return nil, fmt.Errorf("decrypt secrets: %w", err)
	}
	if err := json.Unmarshal(plain, &s.Passwords); err != nil {
		return nil, fmt.Errorf("parse secrets: %w", err)
	}
	if s.Passwords == nil {
		s.Passwords = map[string]string{}
	}
	return s, nil
}

// Get returns the stored password for a server ID.
func (s *Secrets) Get(id string) (string, bool) {
	if s == nil {
		return "", false
	}
	pw, ok := s.Passwords[id]
	return pw, ok && pw != ""
}

// Set stores a password for a server ID in memory. Call Save to persist.
func (s *Secrets) Set(id, password string) {
	if s.Passwords == nil {
		s.Passwords = map[string]string{}
	}
	if password == "" {
		delete(s.Passwords, id)
		return
	}
	s.Passwords[id] = password
}

// Delete removes a stored password for a server ID.
func (s *Secrets) Delete(id string) { s.Set(id, "") }

// Save encrypts and atomically persists the secrets store.
func (s *Secrets) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	key, err := loadOrCreateKey(s.keyPath)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(s.Passwords)
	if err != nil {
		return err
	}
	enc, err := encrypt(key, plain)
	if err != nil {
		return err
	}
	return WriteFileAtomic(s.path, enc, 0o600)
}

func loadOrCreateKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != 32 {
			return nil, fmt.Errorf("secrets key %s has invalid length", path)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key = make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err := WriteFileAtomic(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func encrypt(key, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nonce, nonce, plain, nil)
	out := make([]byte, base64.StdEncoding.EncodedLen(len(sealed)))
	base64.StdEncoding.Encode(out, sealed)
	return out, nil
}

func decrypt(key, data []byte) ([]byte, error) {
	raw := make([]byte, base64.StdEncoding.DecodedLen(len(data)))
	n, err := base64.StdEncoding.Decode(raw, data)
	if err != nil {
		return nil, err
	}
	raw = raw[:n]
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, errors.New("ciphertext is truncated")
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}
