package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/fiandev/bare-eye/internal/collector"
	"github.com/fiandev/bare-eye/internal/config"
	"github.com/fiandev/bare-eye/internal/sshx"
)

// Prober connects to a host and returns one metrics sample. Implementations
// must isolate per-host connection state.
type Prober interface {
	Probe(ctx context.Context, srv config.ServerConfig) (collector.Metrics, error)
	Reconnect(id string)
	Close()
}

// sshProber keeps one reusable SSH connection and collector per host.
type sshProber struct {
	mu      sync.Mutex
	clients map[string]*sshx.Client
	colls   map[string]*collector.Collector
	cfg     *config.Config
	secrets *config.Secrets
}

// NewSSHProber builds the default SSH prober.
func NewSSHProber(cfg *config.Config, secrets *config.Secrets) Prober {
	return &sshProber{
		clients: map[string]*sshx.Client{},
		colls:   map[string]*collector.Collector{},
		cfg:     cfg,
		secrets: secrets,
	}
}

func (p *sshProber) Probe(ctx context.Context, srv config.ServerConfig) (collector.Metrics, error) {
	p.mu.Lock()
	client := p.clients[srv.ID]
	coll := p.colls[srv.ID]
	if coll == nil {
		coll = collector.New()
		p.colls[srv.ID] = coll
	}
	p.mu.Unlock()

	if client == nil {
		opts := sshx.Options{
			KnownHosts:     p.cfg.SSH.KnownHosts,
			DefaultKey:     p.cfg.SSH.DefaultKey,
			ConnectTimeout: srv.ConnectTimeoutOrDefault(p.cfg),
		}
		if p.secrets != nil {
			opts.Password, _ = p.secrets.Get(srv.ID)
		}
		conn, err := sshx.Dial(ctx, srv, opts)
		if err != nil {
			return collector.Metrics{}, err
		}
		p.mu.Lock()
		p.clients[srv.ID] = conn
		p.mu.Unlock()
		client = conn
		coll.Reset()
	}

	metrics, err := coll.Collect(ctx, client, srv.CommandTimeoutOrDefault(p.cfg))
	if err != nil {
		// Drop the connection so the next attempt reconnects cleanly.
		p.drop(srv.ID)
		return collector.Metrics{}, err
	}
	return metrics, nil
}

func (p *sshProber) drop(id string) {
	p.mu.Lock()
	client := p.clients[id]
	delete(p.clients, id)
	p.mu.Unlock()
	if client != nil {
		_ = client.Close()
	}
}

func (p *sshProber) Reconnect(id string) { p.drop(id) }

func (p *sshProber) Close() {
	p.mu.Lock()
	clients := p.clients
	p.clients = map[string]*sshx.Client{}
	p.mu.Unlock()
	for _, c := range clients {
		_ = c.Close()
	}
}

// TestConnection dials a server and runs the fixed test command.
func TestConnection(ctx context.Context, srv config.ServerConfig, cfg *config.Config, secrets *config.Secrets, password, passphrase string) error {
	opts := sshx.Options{
		KnownHosts:     cfg.SSH.KnownHosts,
		DefaultKey:     cfg.SSH.DefaultKey,
		ConnectTimeout: srv.ConnectTimeoutOrDefault(cfg),
		Password:       password,
		KeyPassphrase:  passphrase,
	}
	if opts.Password == "" && secrets != nil {
		opts.Password, _ = secrets.Get(srv.ID)
	}
	client, err := sshx.Dial(ctx, srv, opts)
	if err != nil {
		return err
	}
	defer client.Close()
	return client.Test(ctx, srv.CommandTimeoutOrDefault(cfg))
}

// InstallKey connects and appends the public key to authorized_keys.
func InstallKey(ctx context.Context, srv config.ServerConfig, cfg *config.Config, secrets *config.Secrets, pubkey, password, passphrase string) error {
	if password == "" && secrets != nil {
		password, _ = secrets.Get(srv.ID)
	}
	attempts := []config.ServerConfig{srv}
	// A server configured for key auth may not have the key installed yet;
	// fall back to a stored password when one exists.
	if srv.Auth.Method != config.AuthPassword && password != "" {
		alt := srv
		alt.Auth.Method = config.AuthPassword
		alt.Auth.KeyPath = ""
		attempts = append(attempts, alt)
	}
	var lastErr error
	for _, attempt := range attempts {
		opts := sshx.Options{
			KnownHosts:     cfg.SSH.KnownHosts,
			DefaultKey:     cfg.SSH.DefaultKey,
			ConnectTimeout: attempt.ConnectTimeoutOrDefault(cfg),
			Password:       password,
			KeyPassphrase:  passphrase,
		}
		client, err := sshx.Dial(ctx, attempt, opts)
		if err != nil {
			lastErr = err
			continue
		}
		err = client.InstallAuthorizedKey(ctx, pubkey, attempt.CommandTimeoutOrDefault(cfg))
		client.Close()
		if err != nil {
			return fmt.Errorf("install public key: %w", err)
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("no usable authentication method")
	}
	return lastErr
}

// WaitFor polls until cond is true or the timeout expires (test helper).
func WaitFor(ctx context.Context, timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		select {
		case <-ctx.Done():
			return cond()
		case <-time.After(20 * time.Millisecond):
		}
	}
	return cond()
}
