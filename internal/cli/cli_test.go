package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fiandev/bare-eye/internal/config"
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

func TestUnknownCommand(t *testing.T) {
	if code := Run([]string{"nope"}); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}
