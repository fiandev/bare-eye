package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fiandev/bare-eye/internal/config"
	"github.com/fiandev/bare-eye/internal/tui"
)

func withTempConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("BARE_EYE_CONFIG_DIR", dir)
	return dir
}

func TestInitAddListRemove(t *testing.T) {
	dir := withTempConfig(t)
	if code := Run([]string{"init", "--no-key"}); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, config.FileName)); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"hosts", "add", "--id", "a", "--name", "A", "--host", "127.0.0.1", "--user", "u", "--auth", "agent"}); code != 0 {
		t.Fatalf("add exit = %d", code)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Servers) != 1 || cfg.Servers[0].ID != "a" {
		t.Fatalf("servers = %+v", cfg.Servers)
	}
	if code := Run([]string{"hosts", "list"}); code != 0 {
		t.Errorf("list exit = %d", code)
	}
	if code := Run([]string{"hosts", "disable", "a"}); code != 0 {
		t.Errorf("disable exit = %d", code)
	}
	cfg, _ = config.Load()
	if cfg.Servers[0].Enabled {
		t.Error("host should be disabled")
	}
	if code := Run([]string{"hosts", "remove", "a", "--yes"}); code != 0 {
		t.Errorf("remove exit = %d", code)
	}
	cfg, _ = config.Load()
	if len(cfg.Servers) != 0 {
		t.Error("host should be removed")
	}
}

func TestSetPasswordIsEncrypted(t *testing.T) {
	dir := withTempConfig(t)
	if code := Run([]string{"init", "--no-key"}); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	if code := Run([]string{"hosts", "add", "--id", "db", "--host", "127.0.0.1", "--user", "u", "--auth", "password"}); code != 0 {
		t.Fatalf("add exit = %d", code)
	}
	if code := Run([]string{"config", "set", "password", "db", "hunter2"}); code != 0 {
		t.Fatalf("set password exit = %d", code)
	}
	secrets, err := config.LoadSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if pw, ok := secrets.Get("db"); !ok || pw != "hunter2" {
		t.Fatalf("password = %q, %v", pw, ok)
	}
	cfgRaw, err := os.ReadFile(filepath.Join(dir, config.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfgRaw), "hunter2") {
		t.Fatal("config.yaml must not contain the password")
	}
	encRaw, err := os.ReadFile(filepath.Join(dir, "secrets.enc"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encRaw), "hunter2") {
		t.Fatal("secrets.enc must not contain plaintext")
	}
}

func TestAddDefaultsIDToHost(t *testing.T) {
	withTempConfig(t)
	if code := Run([]string{"init", "--no-key"}); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	if code := Run([]string{"hosts", "add", "--name", "Web", "--host", "198.51.100.7", "--user", "u", "--auth", "agent"}); code != 0 {
		t.Fatalf("add exit = %d", code)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Servers) != 1 {
		t.Fatalf("servers = %+v", cfg.Servers)
	}
	if got := cfg.Servers[0].ID; got != "198.51.100.7" {
		t.Errorf("id = %q, want host address", got)
	}
	if got := cfg.Servers[0].Name; got != "Web" {
		t.Errorf("name = %q, want explicit name", got)
	}
}

func TestUnknownCommand(t *testing.T) {
	if code := Run([]string{"nope"}); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}

func TestSetPasswordShorthandSingleServer(t *testing.T) {
	dir := withTempConfig(t)
	if code := Run([]string{"init", "--no-key"}); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	if code := Run([]string{"hosts", "add", "--id", "only", "--host", "127.0.0.1", "--user", "u", "--auth", "password"}); code != 0 {
		t.Fatalf("add exit = %d", code)
	}
	if code := Run([]string{"config", "set", "password", "shorthand"}); code != 0 {
		t.Fatalf("set password exit = %d", code)
	}
	secrets, err := config.LoadSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if pw, ok := secrets.Get("only"); !ok || pw != "shorthand" {
		t.Fatalf("password = %q, %v", pw, ok)
	}
}

func TestSetAPIKeyIsEncrypted(t *testing.T) {
	dir := withTempConfig(t)
	if code := Run([]string{"init", "--no-key"}); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	if code := Run([]string{"config", "set", "api-key", "sk-test-secret"}); code != 0 {
		t.Fatalf("set api-key exit = %d", code)
	}
	secrets, err := config.LoadSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	key, ok := secrets.GetSecret(config.SecretAIAPIKey)
	if !ok || key != "sk-test-secret" {
		t.Fatalf("api key = %q, %v", key, ok)
	}
	cfgRaw, err := os.ReadFile(filepath.Join(dir, config.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfgRaw), "sk-test-secret") {
		t.Fatal("config.yaml must not contain the API key")
	}
	encRaw, err := os.ReadFile(filepath.Join(dir, "secrets.enc"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encRaw), "sk-test-secret") {
		t.Fatal("secrets.enc must not contain the plaintext API key")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AI.Enabled {
		t.Fatal("setting an API key should enable the agent")
	}
}

func TestConfigAIToggle(t *testing.T) {
	dir := withTempConfig(t)
	if code := Run([]string{"init", "--no-key"}); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	if code := Run([]string{"config", "ai", "on"}); code != 0 {
		t.Fatalf("config ai on exit = %d", code)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AI.Enabled {
		t.Fatal("agent should be enabled")
	}
	raw, err := os.ReadFile(filepath.Join(dir, config.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "ai:") {
		t.Fatal("config.yaml must contain the ai section after saving")
	}
	if code := Run([]string{"config", "ai", "off"}); code != 0 {
		t.Fatalf("config ai off exit = %d", code)
	}
	cfg, _ = config.Load()
	if cfg.AI.Enabled {
		t.Fatal("agent should be disabled")
	}
	if code := Run([]string{"config", "ai"}); code != 0 {
		t.Errorf("config ai status exit = %d", code)
	}
}

func TestParseTick(t *testing.T) {
	valid := map[string]time.Duration{
		"0":     0,
		"100":   100 * time.Millisecond,
		"1000":  time.Second,
		"250ms": 250 * time.Millisecond,
		"1s":    time.Second,
		" 500 ": 500 * time.Millisecond,
	}
	for in, want := range valid {
		got, err := parseTick(in)
		if err != nil {
			t.Errorf("parseTick(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseTick(%q) = %v, want %v", in, got, want)
		}
	}
	for _, in := range []string{"-1", "1001", "1500ms", "abc", ""} {
		if _, err := parseTick(in); err == nil {
			t.Errorf("parseTick(%q) should fail", in)
		}
	}
}

func TestDashboardTick(t *testing.T) {
	got, err := dashboardTick(nil)
	if err != nil || got != tui.DefaultRenderTick {
		t.Errorf("default tick = %v, %v; want %v", got, err, tui.DefaultRenderTick)
	}
	for _, args := range [][]string{
		{"--tick", "250"},
		{"-tick", "250ms"},
		{"--tick=0"},
	} {
		if _, err := dashboardTick(args); err != nil {
			t.Errorf("dashboardTick(%v) error: %v", args, err)
		}
	}
	if _, err := dashboardTick([]string{"--tick", "2000"}); err == nil {
		t.Error("tick above 1000ms should fail")
	}
	if _, err := dashboardTick([]string{"extra"}); err == nil {
		t.Error("unexpected argument should fail")
	}
}
