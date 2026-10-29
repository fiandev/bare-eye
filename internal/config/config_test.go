package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestParseDefaultsAndValidation(t *testing.T) {
	doc := `
version: 1
servers:
  - id: web-1
    host: 203.0.113.10
    user: monitor
    auth:
      method: key
      key_path: /tmp/key
`
	cfg, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Monitoring.Interval.Duration() != 2*time.Second {
		t.Errorf("interval = %v, want 2s", cfg.Monitoring.Interval)
	}
	if cfg.Servers[0].Port != 22 {
		t.Errorf("port = %d, want 22", cfg.Servers[0].Port)
	}
	if !cfg.Servers[0].Enabled {
		t.Error("enabled should default to true")
	}
	if cfg.SSH.KnownHosts == "" {
		t.Error("known_hosts should default to a path")
	}
}

func TestValidationErrors(t *testing.T) {
	base := func(mut func(*ServerConfig)) string {
		s := ServerConfig{
			ID: "a", Name: "A", Host: "example.com", Port: 22, User: "u",
			Auth: AuthConfig{Method: AuthKey, KeyPath: "/k"}, Enabled: true,
		}
		mut(&s)
		cfg := &Config{
			Version: 1,
			SSH:     SSHConfig{DefaultKey: "/k", KnownHosts: "/kh"},
			Monitoring: MonitoringConfig{
				Interval: Duration(time.Second), ConnectTimeout: Duration(time.Second),
				CommandTimeout: Duration(time.Second), StaleAfter: Duration(time.Second), MaxConcurrent: 4,
			},
			Servers: []ServerConfig{s},
		}
		out, _ := cfg.marshalForTest()
		return out
	}
	cases := map[string]string{
		"bad port":    base(func(s *ServerConfig) { s.Port = 70000 }),
		"empty host":  base(func(s *ServerConfig) { s.Host = "" }),
		"bad id":      base(func(s *ServerConfig) { s.ID = "bad id" }),
		"empty user":  base(func(s *ServerConfig) { s.User = "" }),
		"bad auth":    base(func(s *ServerConfig) { s.Auth.Method = "magic" }),
		"no key path": base(func(s *ServerConfig) { s.Auth.KeyPath = "" }),
	}
	for name, doc := range cases {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}

	dup := `
version: 1
servers:
  - {id: a, host: h1, user: u, auth: {method: agent}}
  - {id: a, host: h2, user: u, auth: {method: agent}}
`
	if _, err := Parse([]byte(dup)); err == nil {
		t.Error("duplicate ids should fail")
	}
}

func TestDurationAndPathExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	doc := `
version: 1
ssh:
  default_key: ~/keys/id
  known_hosts: ~/kh
monitoring:
  interval: 1500ms
servers:
  - id: a
    host: localhost
    user: u
    auth: {method: key, key_path: "~/other"}
`
	cfg, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Monitoring.Interval.Duration() != 1500*time.Millisecond {
		t.Errorf("interval = %v", cfg.Monitoring.Interval)
	}
	if cfg.SSH.DefaultKey != filepath.Join(home, "keys/id") {
		t.Errorf("default_key = %q", cfg.SSH.DefaultKey)
	}
	if cfg.Servers[0].Auth.KeyPath != filepath.Join(home, "other") {
		t.Errorf("key_path = %q", cfg.Servers[0].Auth.KeyPath)
	}
}

func TestSaveLoadAndPermissions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BARE_EYE_CONFIG_DIR", dir)

	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Servers = append(cfg.Servers, ServerConfig{
		ID: "one", Name: "One", Host: "127.0.0.1", Port: 22, User: "u",
		Auth: AuthConfig{Method: AuthAgent}, Enabled: true,
	})
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	st, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("config mode = %o, want 600", perm)
	}
	dst, _ := os.Stat(dir)
	if perm := dst.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir mode = %o, want 700", perm)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Servers) != 1 || loaded.Servers[0].ID != "one" {
		t.Fatalf("unexpected servers: %+v", loaded.Servers)
	}
}

func TestLoadMissing(t *testing.T) {
	t.Setenv("BARE_EYE_CONFIG_DIR", t.TempDir())
	if _, err := Load(); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("err = %v, want ErrNotInitialized", err)
	}
}

func TestUnknownFieldsPreserved(t *testing.T) {
	doc := `
version: 1
future_top: hello
ssh:
  known_hosts: /kh
  future_ssh: 1
monitoring:
  interval: 2s
servers:
  - id: a
    host: h
    user: u
    auth: {method: agent}
    future_server: keepme
`
	cfg, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	out, err := cfg.marshalForTest()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"future_top", "future_ssh", "future_server"} {
		if !strings.Contains(out, want) {
			t.Errorf("marshaled config lost %q:\n%s", want, out)
		}
	}
}

func TestSecretsEncryptedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Set("web", "hunter2")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, secretsFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hunter2") {
		t.Fatal("secrets file contains plaintext password")
	}
	if perm := fileMode(t, filepath.Join(dir, secretsFile)); perm != 0o600 {
		t.Errorf("secrets mode = %o", perm)
	}
	loaded, err := LoadSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if pw, ok := loaded.Get("web"); !ok || pw != "hunter2" {
		t.Fatalf("password = %q, %v", pw, ok)
	}
	loaded.Delete("web")
	if _, ok := loaded.Get("web"); ok {
		t.Error("password should be deleted")
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Production VPS":    "production-vps",
		"  web_01.example ": "web_01.example",
		"":                  "server",
		"---":               "server",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

// marshalForTest is a helper so tests can round-trip through YAML.
func (c *Config) marshalForTest() (string, error) {
	data, err := yaml.Marshal(c)
	return string(data), err
}

func TestExampleConfigParses(t *testing.T) {
	data, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("example config: %v", err)
	}
	if len(cfg.Servers) != 3 {
		t.Errorf("servers = %d, want 3", len(cfg.Servers))
	}
	if cfg.Servers[1].Interval.Duration() != 5*time.Second {
		t.Errorf("per-host interval = %v", cfg.Servers[1].Interval)
	}
	if cfg.Monitoring.Thresholds.CPU != 0 {
		t.Error("thresholds should ship disabled (0)")
	}
}
