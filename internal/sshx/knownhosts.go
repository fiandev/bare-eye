package sshx

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// EnsureKnownHosts creates an empty known-hosts file with 0600 permissions.
func EnsureKnownHosts(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("known_hosts path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, nil, 0o600)
}

// AppendKnownHost records a host key after explicit user trust.
func AppendKnownHost(path, addr string, key ssh.PublicKey) error {
	if err := EnsureKnownHosts(path); err != nil {
		return err
	}
	line := knownhosts.Line([]string{knownhosts.Normalize(addr)}, key)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

// hostKeyCallback builds a strict callback that reports unknown and changed
// keys as typed errors instead of silently trusting them.
func hostKeyCallback(path, addr string) (ssh.HostKeyCallback, error) {
	if err := EnsureKnownHosts(path); err != nil {
		return nil, err
	}
	check, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("load known_hosts: %w", err)
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := check(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) {
			fp := ssh.FingerprintSHA256(key)
			if len(keyErr.Want) == 0 {
				return &UnknownHostKeyError{Host: addr, Fingerprint: fp, Key: key}
			}
			previous := make([]string, 0, len(keyErr.Want))
			for _, want := range keyErr.Want {
				previous = append(previous, ssh.FingerprintSHA256(want.Key))
			}
			return &ChangedHostKeyError{Host: addr, Fingerprint: fp, Previous: previous, Key: key}
		}
		return err
	}, nil
}
