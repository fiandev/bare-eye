package sshx

import (
	"fmt"
	"time"

	"golang.org/x/crypto/ssh"
)

// UnknownHostKeyError means the host is not present in known_hosts.
type UnknownHostKeyError struct {
	Host        string
	Fingerprint string
	Key         ssh.PublicKey
}

func (e *UnknownHostKeyError) Error() string {
	return fmt.Sprintf("unknown host key for %s (%s)", e.Host, e.Fingerprint)
}

// ChangedHostKeyError means a stored key differs from the presented key.
type ChangedHostKeyError struct {
	Host        string
	Fingerprint string
	Previous    []string
	Key         ssh.PublicKey
}

func (e *ChangedHostKeyError) Error() string {
	return fmt.Sprintf("HOST KEY CHANGED for %s: presented %s, stored %v", e.Host, e.Fingerprint, e.Previous)
}

// AuthError wraps authentication failures with a stable message.
type AuthError struct {
	User string
	Host string
	Err  error
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("authentication failed for %s@%s: %v", e.User, e.Host, e.Err)
}

func (e *AuthError) Unwrap() error { return e.Err }

// PassphraseRequiredError means an encrypted key needs a passphrase.
type PassphraseRequiredError struct{ Path string }

func (e *PassphraseRequiredError) Error() string {
	return fmt.Sprintf("passphrase required for %s", e.Path)
}

// TimeoutError marks a bounded operation that exceeded its deadline.
type TimeoutError struct {
	Op      string
	Timeout time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("%s timed out after %s", e.Op, e.Timeout)
}
