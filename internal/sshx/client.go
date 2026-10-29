// Package sshx provides SSH transport with strict host-key verification.
package sshx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/fiandev/bare-eye/internal/config"
	"github.com/fiandev/bare-eye/internal/keys"
)

// ErrPasswordRequired indicates password auth was selected without a password.
var ErrPasswordRequired = errors.New("password required")

// ErrNoAgent indicates agent auth was selected but no agent is available.
var ErrNoAgent = errors.New("no SSH agent available (SSH_AUTH_SOCK is not set)")

// MaxOutput bounds remote command output.
const MaxOutput = 512 * 1024

// Options carries per-connection credentials and defaults.
type Options struct {
	KnownHosts     string
	DefaultKey     string
	Password       string
	KeyPassphrase  string
	ConnectTimeout time.Duration
}

// Client is a live SSH connection to one server.
type Client struct {
	conn    *ssh.Client
	srv     config.ServerConfig
	closers []io.Closer
}

// Dial connects to the server, verifying the host key against known_hosts.
func Dial(ctx context.Context, srv config.ServerConfig, opts Options) (*Client, error) {
	timeout := opts.ConnectTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	auth, closers, err := authMethods(srv, opts)
	if err != nil {
		return nil, err
	}
	closeAll := func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}
	cb, err := hostKeyCallback(opts.KnownHosts, srv.Addr())
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            srv.User,
		Auth:            auth,
		HostKeyCallback: cb,
		Timeout:         timeout,
	}
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", srv.Addr())
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			return nil, &TimeoutError{Op: "connect", Timeout: timeout}
		}
		return nil, fmt.Errorf("connect to %s: %w", srv.Addr(), err)
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-stop:
		}
	}()
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, srv.Addr(), cfg)
	close(stop)
	_ = conn.SetDeadline(time.Time{})
	if err != nil {
		conn.Close()
		closeAll()
		var unknown *UnknownHostKeyError
		var changed *ChangedHostKeyError
		if errors.As(err, &unknown) || errors.As(err, &changed) {
			return nil, err
		}
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			return nil, &TimeoutError{Op: "handshake", Timeout: timeout}
		}
		return nil, &AuthError{User: srv.User, Host: srv.Addr(), Err: err}
	}
	return &Client{conn: ssh.NewClient(sshConn, chans, reqs), srv: srv, closers: closers}, nil
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func authMethods(srv config.ServerConfig, opts Options) ([]ssh.AuthMethod, []io.Closer, error) {
	switch srv.Auth.Method {
	case config.AuthAgent:
		sock := os.Getenv("SSH_AUTH_SOCK")
		if sock == "" {
			return nil, nil, ErrNoAgent
		}
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return nil, nil, fmt.Errorf("connect to SSH agent: %w", err)
		}
		ag := agent.NewClient(conn)
		signers, err := ag.Signers()
		if err != nil {
			conn.Close()
			return nil, nil, fmt.Errorf("list SSH agent keys: %w", err)
		}
		if len(signers) == 0 {
			conn.Close()
			return nil, nil, errors.New("SSH agent has no keys")
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signers...)}, []io.Closer{conn}, nil

	case config.AuthPassword:
		if opts.Password == "" {
			return nil, nil, ErrPasswordRequired
		}
		pw := opts.Password
		return []ssh.AuthMethod{
			ssh.Password(pw),
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = pw
				}
				return answers, nil
			}),
		}, nil, nil

	case config.AuthKey, "":
		path := srv.Auth.KeyPath
		if path == "" {
			path = opts.DefaultKey
		}
		if path == "" {
			return nil, nil, errors.New("no SSH key configured")
		}
		signer, err := keys.LoadSigner(path, opts.KeyPassphrase)
		if err != nil {
			if errors.Is(err, keys.ErrPassphraseRequired) {
				return nil, nil, &PassphraseRequiredError{Path: path}
			}
			return nil, nil, err
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil, nil
	}
	return nil, nil, fmt.Errorf("unsupported auth method %q", srv.Auth.Method)
}

// Close terminates the connection.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	var err error
	if c.conn != nil {
		err = c.conn.Close()
	}
	for _, closer := range c.closers {
		_ = closer.Close()
	}
	c.closers = nil
	return err
}

// Server returns the configuration this client was dialed with.
func (c *Client) Server() config.ServerConfig { return c.srv }

// Run executes a fixed remote command with a bounded timeout and output size.
func (c *Client) Run(ctx context.Context, command string, timeout time.Duration) ([]byte, error) {
	return c.run(ctx, command, timeout, nil, MaxOutput)
}

func (c *Client) run(ctx context.Context, command string, timeout time.Duration, stdin io.Reader, maxBytes int) ([]byte, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	session, err := c.conn.NewSession()
	if err != nil {
		return nil, fmt.Errorf("open SSH session: %w", err)
	}
	defer session.Close()

	var stdout limitedBuffer
	var stderr limitedBuffer
	stdout.max = maxBytes
	stderr.max = 4096
	session.Stdout = &stdout
	session.Stderr = &stderr
	if stdin != nil {
		session.Stdin = stdin
	}
	if err := session.Start(command); err != nil {
		return nil, fmt.Errorf("start remote command: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	select {
	case <-ctx.Done():
		_ = session.Close()
		<-done
		return stdout.Bytes(), &TimeoutError{Op: "remote command", Timeout: timeout}
	case err := <-done:
		if stdout.overflow {
			return nil, fmt.Errorf("remote output exceeded %d bytes", maxBytes)
		}
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			return stdout.Bytes(), fmt.Errorf("remote command failed: %s", msg)
		}
		return stdout.Bytes(), nil
	}
}

// Test verifies connectivity and authentication with a fixed read-only command.
func (c *Client) Test(ctx context.Context, timeout time.Duration) error {
	out, err := c.Run(ctx, "echo bare-eye-ok", timeout)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(out)) != "bare-eye-ok" {
		return errors.New("unexpected response from test command")
	}
	return nil
}

// InstallAuthorizedKey appends pubkey to the remote ~/.ssh/authorized_keys
// without overwriting existing content. The key is sent on stdin and never
// interpolated into the shell command.
func (c *Client) InstallAuthorizedKey(ctx context.Context, pubkey string, timeout time.Duration) error {
	const script = `set -e
mkdir -p "$HOME/.ssh"
chmod 700 "$HOME/.ssh"
touch "$HOME/.ssh/authorized_keys"
chmod 600 "$HOME/.ssh/authorized_keys"
k=$(cat)
if ! grep -qxF "$k" "$HOME/.ssh/authorized_keys"; then
  printf '%s\n' "$k" >> "$HOME/.ssh/authorized_keys"
fi
echo bare-eye-installed`
	out, err := c.run(ctx, script, timeout, strings.NewReader(strings.TrimSpace(pubkey)+"\n"), 4096)
	if err != nil {
		return err
	}
	if !strings.Contains(string(out), "bare-eye-installed") {
		return errors.New("key installation did not confirm success")
	}
	return nil
}

type limitedBuffer struct {
	buf      bytes.Buffer
	max      int
	overflow bool
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	if w.overflow {
		return len(p), nil
	}
	if w.buf.Len()+len(p) > w.max {
		room := w.max - w.buf.Len()
		if room > 0 {
			w.buf.Write(p[:room])
		}
		w.overflow = true
		return len(p), nil
	}
	return w.buf.Write(p)
}

func (w *limitedBuffer) Bytes() []byte  { return w.buf.Bytes() }
func (w *limitedBuffer) String() string { return w.buf.String() }
