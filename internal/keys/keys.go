// Package keys manages Bare Eye's dedicated SSH key pair.
package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// ErrExists is returned when a key already exists and overwrite is false.
var ErrExists = errors.New("key already exists")

// Generate creates an Ed25519 key pair at path using ssh-keygen when available.
// A non-empty passphrase encrypts the private key. Existing files are never
// overwritten unless force is true.
func Generate(path, passphrase, comment string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%w: %s", ErrExists, path)
		}
		if _, err := os.Stat(path + ".pub"); err == nil {
			return fmt.Errorf("%w: %s.pub", ErrExists, path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create key directory: %w", err)
	}
	if force {
		// ssh-keygen prompts interactively when files exist; remove explicitly.
		_ = os.Remove(path)
		_ = os.Remove(path + ".pub")
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("set key directory permissions: %w", err)
	}
	if _, err := exec.LookPath("ssh-keygen"); err == nil {
		if err := generateWithSSHKeygen(path, passphrase, comment); err != nil {
			return err
		}
	} else {
		if passphrase != "" {
			return errors.New("ssh-keygen is required to create a passphrase-protected key")
		}
		if err := generateInGo(path, comment); err != nil {
			return err
		}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(path+".pub", 0o600); err != nil {
		return err
	}
	return nil
}

func generateWithSSHKeygen(path, passphrase, comment string) error {
	if comment == "" {
		comment = "bare-eye"
	}
	args := []string{"-q", "-t", "ed25519", "-f", path, "-N", passphrase, "-C", comment}
	cmd := exec.Command("ssh-keygen", args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ssh-keygen failed: %w", err)
	}
	return nil
}

func generateInGo(path, comment string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return err
	}
	privPEM := pem.EncodeToMemory(block)
	if err := os.WriteFile(path, privPEM, 0o600); err != nil {
		return err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	if comment != "" {
		line += " " + comment
	}
	return os.WriteFile(path+".pub", []byte(line+"\n"), 0o600)
}

// PublicKey returns the authorized-keys line for the private key at path.
func PublicKey(path string) (string, error) {
	pubPath := path + ".pub"
	if data, err := os.ReadFile(pubPath); err == nil {
		line := strings.TrimSpace(string(data))
		if line == "" {
			return "", fmt.Errorf("public key %s is empty", pubPath)
		}
		return line, nil
	}
	signer, err := LoadSigner(path, "")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), nil
}

// Fingerprint returns the SHA256 fingerprint for the key at path.
func Fingerprint(path string) (string, error) {
	pub, err := ParsePublicKey(path)
	if err != nil {
		return "", err
	}
	return ssh.FingerprintSHA256(pub), nil
}

// ParsePublicKey parses the public key belonging to the private key at path.
func ParsePublicKey(path string) (ssh.PublicKey, error) {
	line, err := PublicKey(path)
	if err != nil {
		return nil, err
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	return pub, nil
}

// LoadSigner loads a private key, optionally decrypting it with passphrase.
func LoadSigner(path, passphrase string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key %s: %w", path, err)
	}
	if passphrase != "" {
		signer, err := ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
		if err != nil {
			return nil, fmt.Errorf("decrypt private key: %w", err)
		}
		return signer, nil
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			return nil, ErrPassphraseRequired
		}
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	return signer, nil
}

// ErrPassphraseRequired indicates an encrypted private key needs a passphrase.
var ErrPassphraseRequired = errors.New("private key is passphrase-protected")

// Exists reports whether a key pair exists at path.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ReadPublicFile reads a raw public key file (not necessarily Bare Eye-managed).
func ReadPublicFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
