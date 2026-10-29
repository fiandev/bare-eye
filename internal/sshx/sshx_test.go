package sshx

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/fiandev/bare-eye/internal/collector"
	"github.com/fiandev/bare-eye/internal/config"
)

type testSSHServer struct {
	ln      net.Listener
	cfg     *ssh.ServerConfig
	hostKey ssh.Signer
	handler func(cmd string) (string, uint32)
	wg      sync.WaitGroup
}

func newTestServer(t *testing.T, hostKey ssh.Signer, password string, handler func(string) (string, uint32)) *testSSHServer {
	t.Helper()
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if string(pass) == password {
				return nil, nil
			}
			return nil, errors.New("invalid password")
		},
	}
	cfg.AddHostKey(hostKey)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &testSSHServer{ln: ln, cfg: cfg, hostKey: hostKey, handler: handler}
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(s.Close)
	return s
}

func (s *testSSHServer) Addr() string { return s.ln.Addr().String() }

func (s *testSSHServer) Close() {
	_ = s.ln.Close()
	s.wg.Wait()
}

func (s *testSSHServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(conn)
		}()
	}
}

func (s *testSSHServer) handle(conn net.Conn) {
	sconn, chans, reqs, err := ssh.NewServerConn(conn, s.cfg)
	if err != nil {
		return
	}
	defer sconn.Close()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "only session")
			continue
		}
		ch, requests, err := nc.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range requests {
				if req.Type != "exec" {
					_ = req.Reply(false, nil)
					continue
				}
				var payload struct{ Command string }
				if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
					_ = req.Reply(false, nil)
					return
				}
				_ = req.Reply(true, nil)
				out, code := s.handler(payload.Command)
				_, _ = ch.Write([]byte(out))
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{code}))
				return
			}
		}()
	}
}

func hostSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func passwordServer(t *testing.T, handler func(string) (string, uint32)) *testSSHServer {
	signer := hostSigner(t)
	if handler == nil {
		handler = func(cmd string) (string, uint32) {
			if strings.HasPrefix(cmd, "echo ") {
				return strings.TrimPrefix(cmd, "echo ") + "\n", 0
			}
			return "", 0
		}
	}
	return newTestServer(t, signer, "s3cret", handler)
}

func serverConfig(addr, method string) config.ServerConfig {
	host, port, _ := net.SplitHostPort(addr)
	p := 0
	_, _ = fmt.Sscan(port, &p)
	return config.ServerConfig{
		ID: "test", Name: "test", Host: host, Port: p, User: "tester",
		Auth: config.AuthConfig{Method: method}, Enabled: true,
	}
}

func TestUnknownHostKeyMustBeTrusted(t *testing.T) {
	srv := passwordServer(t, nil)
	dir := t.TempDir()
	kh := filepath.Join(dir, "known_hosts")
	opts := Options{KnownHosts: kh, Password: "s3cret", ConnectTimeout: 2 * time.Second}
	cfg := serverConfig(srv.Addr(), config.AuthPassword)

	_, err := Dial(context.Background(), cfg, opts)
	var unknown *UnknownHostKeyError
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %v, want UnknownHostKeyError", err)
	}
	if !strings.HasPrefix(unknown.Fingerprint, "SHA256:") {
		t.Errorf("fingerprint = %q", unknown.Fingerprint)
	}

	if err := AppendKnownHost(kh, cfg.Addr(), unknown.Key); err != nil {
		t.Fatal(err)
	}
	client, err := Dial(context.Background(), cfg, opts)
	if err != nil {
		t.Fatalf("Dial after trust: %v", err)
	}
	defer client.Close()
	if err := client.Test(context.Background(), 2*time.Second); err != nil {
		t.Fatalf("Test: %v", err)
	}
}

func TestChangedHostKeyIsRejected(t *testing.T) {
	srv := passwordServer(t, nil)
	kh := filepath.Join(t.TempDir(), "known_hosts")
	cfg := serverConfig(srv.Addr(), config.AuthPassword)

	oldKey := hostSigner(t).PublicKey()
	if err := AppendKnownHost(kh, cfg.Addr(), oldKey); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(kh)
	_, err := Dial(context.Background(), cfg, Options{KnownHosts: kh, Password: "s3cret", ConnectTimeout: 2 * time.Second})
	var changed *ChangedHostKeyError
	if !errors.As(err, &changed) {
		t.Fatalf("err = %v, want ChangedHostKeyError", err)
	}
	if len(changed.Previous) != 1 {
		t.Errorf("previous = %v", changed.Previous)
	}
	after, _ := os.ReadFile(kh)
	if string(before) != string(after) {
		t.Error("known_hosts must not be modified on a changed key")
	}
}

func TestBadPasswordIsAuthError(t *testing.T) {
	srv := passwordServer(t, nil)
	kh := filepath.Join(t.TempDir(), "known_hosts")
	cfg := serverConfig(srv.Addr(), config.AuthPassword)
	if err := AppendKnownHost(kh, cfg.Addr(), srv.hostKey.PublicKey()); err != nil {
		t.Fatal(err)
	}
	_, err := Dial(context.Background(), cfg, Options{KnownHosts: kh, Password: "wrong", ConnectTimeout: 2 * time.Second})
	var auth *AuthError
	if !errors.As(err, &auth) {
		t.Fatalf("err = %v, want AuthError", err)
	}
}

func TestCommandTimeout(t *testing.T) {
	srv := passwordServer(t, func(string) (string, uint32) {
		time.Sleep(2 * time.Second)
		return "late\n", 0
	})
	kh := filepath.Join(t.TempDir(), "known_hosts")
	cfg := serverConfig(srv.Addr(), config.AuthPassword)
	if err := AppendKnownHost(kh, cfg.Addr(), srv.hostKey.PublicKey()); err != nil {
		t.Fatal(err)
	}
	client, err := Dial(context.Background(), cfg, Options{KnownHosts: kh, Password: "s3cret", ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	start := time.Now()
	_, err = client.Run(context.Background(), "sleep", 150*time.Millisecond)
	var timeout *TimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("err = %v, want TimeoutError", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("timeout took %v", elapsed)
	}
}

func TestCollectorAgainstSSHServer(t *testing.T) {
	fixture := `###BAREEYE:uname
Linux 6.8.0
###BAREEYE:hostname
box
###BAREEYE:cores
4
###BAREEYE:uptime
100.0 200.0
###BAREEYE:loadavg
0.10 0.20 0.30 1/10 1
###BAREEYE:meminfo
MemTotal: 1000 kB
MemAvailable: 400 kB
SwapTotal: 0 kB
SwapFree: 0 kB
###BAREEYE:stat
cpu  10 0 10 80 0 0 0 0 0 0
###BAREEYE:netdev
  eth0: 100 1 0 0 0 0 0 0 200 1 0 0 0 0 0 0
###BAREEYE:df
Filesystem 1024-blocks Used Available Capacity Mounted on
/dev/sda1 1000 250 750 25% /
###BAREEYE:end
`
	srv := passwordServer(t, func(string) (string, uint32) { return fixture, 0 })
	kh := filepath.Join(t.TempDir(), "known_hosts")
	cfg := serverConfig(srv.Addr(), config.AuthPassword)
	if err := AppendKnownHost(kh, cfg.Addr(), srv.hostKey.PublicKey()); err != nil {
		t.Fatal(err)
	}
	client, err := Dial(context.Background(), cfg, Options{KnownHosts: kh, Password: "s3cret", ConnectTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	m, err := collector.New().Collect(context.Background(), client, 2*time.Second)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if m.MemTotal != 1000*1024 || m.Hostname != "box" || len(m.Filesystems) != 1 {
		t.Errorf("metrics = %+v", m)
	}
}
