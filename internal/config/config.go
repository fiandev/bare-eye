// Package config owns Bare Eye's on-disk configuration: schema, defaults,
// path resolution, validation, and atomic persistence.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// Version is the current configuration schema version.
	Version = 1

	// FileName is the configuration file name inside the config directory.
	FileName = "config.yaml"

	// AuthKey, AuthAgent and AuthPassword are the supported SSH auth methods.
	AuthKey      = "key"
	AuthAgent    = "agent"
	AuthPassword = "password"
)

// ErrNotInitialized is returned when the configuration file does not exist.
var ErrNotInitialized = errors.New("bare-eye is not initialized")

// Duration is a time.Duration that marshals as a human-readable string.
type Duration time.Duration

func (d Duration) Duration() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		var n int64
		if err2 := node.Decode(&n); err2 != nil {
			return fmt.Errorf("line %d: duration must be a string such as \"2s\"", node.Line)
		}
		*d = Duration(time.Duration(n))
		return nil
	}
	v, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("line %d: invalid duration %q", node.Line, s)
	}
	*d = Duration(v)
	return nil
}

// Config is the root configuration document.
type Config struct {
	Version    int              `yaml:"version"`
	SSH        SSHConfig        `yaml:"ssh"`
	Monitoring MonitoringConfig `yaml:"monitoring"`
	AI         AIConfig         `yaml:"ai,omitempty"`
	Servers    []ServerConfig   `yaml:"servers"`

	// Extra preserves unknown top-level fields for forward compatibility.
	Extra map[string]any `yaml:",inline"`
}

// AIConfig configures the optional LLM automation agent. The API key is never
// stored in this file; it is read from the environment variable named by
// APIKeyEnv (or OPENAI_API_KEY as a fallback).
type AIConfig struct {
	Enabled        bool     `yaml:"enabled"`
	BaseURL        string   `yaml:"base_url,omitempty"`
	Model          string   `yaml:"model,omitempty"`
	APIKeyEnv      string   `yaml:"api_key_env,omitempty"`
	Timeout        Duration `yaml:"timeout,omitempty"`
	CommandTimeout Duration `yaml:"command_timeout,omitempty"`
	MaxSteps       int      `yaml:"max_steps,omitempty"`

	Extra map[string]any `yaml:",inline"`
}

// DefaultAIBaseURL is the OpenAI-compatible endpoint used when none is set.
const DefaultAIBaseURL = "https://api.openai.com/v1"

// APIKey resolves the agent API key. An explicit environment variable wins,
// then the encrypted secrets store, then OPENAI_API_KEY as a convenience.
func (a AIConfig) APIKey(secrets *Secrets) string {
	name := strings.TrimSpace(a.APIKeyEnv)
	if name == "" {
		name = "BARE_EYE_API_KEY"
	}
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	if secrets != nil {
		if v, ok := secrets.GetSecret(SecretAIAPIKey); ok {
			return strings.TrimSpace(v)
		}
	}
	return strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
}

// SSHConfig holds global SSH defaults.
type SSHConfig struct {
	DefaultKey  string `yaml:"default_key,omitempty"`
	KnownHosts  string `yaml:"known_hosts,omitempty"`
	DefaultUser string `yaml:"default_user,omitempty"`

	Extra map[string]any `yaml:",inline"`
}

// Thresholds are warning/critical percentages. Zero disables a threshold.
type Thresholds struct {
	CPU  float64 `yaml:"cpu,omitempty"`
	Mem  float64 `yaml:"mem,omitempty"`
	Swap float64 `yaml:"swap,omitempty"`
	Disk float64 `yaml:"disk,omitempty"`
}

// MonitoringConfig controls polling and freshness.
type MonitoringConfig struct {
	Interval       Duration   `yaml:"interval"`
	ConnectTimeout Duration   `yaml:"connect_timeout"`
	CommandTimeout Duration   `yaml:"command_timeout"`
	StaleAfter     Duration   `yaml:"stale_after"`
	MaxConcurrent  int        `yaml:"max_concurrent"`
	Thresholds     Thresholds `yaml:"thresholds,omitempty"`

	Extra map[string]any `yaml:",inline"`
}

// AuthConfig describes how to authenticate to one server.
type AuthConfig struct {
	Method  string `yaml:"method"`
	KeyPath string `yaml:"key_path,omitempty"`
}

// ServerConfig is one monitored host.
type ServerConfig struct {
	ID             string     `yaml:"id"`
	Name           string     `yaml:"name,omitempty"`
	Host           string     `yaml:"host"`
	Port           int        `yaml:"port"`
	User           string     `yaml:"user"`
	Auth           AuthConfig `yaml:"auth"`
	Enabled        bool       `yaml:"enabled"`
	ConnectTimeout Duration   `yaml:"connect_timeout,omitempty"`
	CommandTimeout Duration   `yaml:"command_timeout,omitempty"`
	Interval       Duration   `yaml:"interval,omitempty"`
	Tags           []string   `yaml:"tags,omitempty"`

	Extra map[string]any `yaml:",inline"`
}

// UnmarshalYAML applies per-server defaults before decoding.
func (s *ServerConfig) UnmarshalYAML(node *yaml.Node) error {
	type plain ServerConfig
	p := plain{Port: 22, Enabled: true, Auth: AuthConfig{Method: AuthKey}}
	if err := node.Decode(&p); err != nil {
		return err
	}
	*s = ServerConfig(p)
	return nil
}

// Addr returns host:port suitable for SSH dialing.
func (s ServerConfig) Addr() string {
	return net.JoinHostPort(s.Host, fmt.Sprint(s.Port))
}

// DisplayName returns the friendly name, falling back to the host.
func (s ServerConfig) DisplayName() string {
	if strings.TrimSpace(s.Name) != "" {
		return s.Name
	}
	return s.Host
}

// IsEnabled reports whether monitoring is enabled for this server.
func (s ServerConfig) IsEnabled() bool { return s.Enabled }

// IntervalOrDefault returns the effective polling interval.
func (s ServerConfig) IntervalOrDefault(cfg *Config) time.Duration {
	if s.Interval > 0 {
		return s.Interval.Duration()
	}
	return cfg.Monitoring.Interval.Duration()
}

// ConnectTimeoutOrDefault returns the effective connect timeout.
func (s ServerConfig) ConnectTimeoutOrDefault(cfg *Config) time.Duration {
	if s.ConnectTimeout > 0 {
		return s.ConnectTimeout.Duration()
	}
	return cfg.Monitoring.ConnectTimeout.Duration()
}

// CommandTimeoutOrDefault returns the effective remote command timeout.
func (s ServerConfig) CommandTimeoutOrDefault(cfg *Config) time.Duration {
	if s.CommandTimeout > 0 {
		return s.CommandTimeout.Duration()
	}
	return cfg.Monitoring.CommandTimeout.Duration()
}

// Dir resolves the Bare Eye configuration directory.
func Dir() (string, error) {
	if v := strings.TrimSpace(os.Getenv("BARE_EYE_CONFIG_DIR")); v != "" {
		return expandPath(v)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", fmt.Errorf("resolve home directory: %w", herr)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "bare-eye"), nil
}

// Path returns the config file path.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// KeysDir returns the directory that holds Bare Eye-managed keys.
func KeysDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "keys"), nil
}

// DefaultKeyPath returns the default generated private-key path.
func DefaultKeyPath() (string, error) {
	dir, err := KeysDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "bare-eye-ed25519"), nil
}

// DefaultKnownHosts returns the default Bare Eye-managed known-hosts path.
func DefaultKnownHosts() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "known_hosts"), nil
}

// Default returns a fully populated configuration with no servers.
func Default() (*Config, error) {
	key, err := DefaultKeyPath()
	if err != nil {
		return nil, err
	}
	known, err := DefaultKnownHosts()
	if err != nil {
		return nil, err
	}
	return &Config{
		Version: Version,
		SSH: SSHConfig{
			DefaultKey: key,
			KnownHosts: known,
		},
		Monitoring: MonitoringConfig{
			Interval:       Duration(2 * time.Second),
			ConnectTimeout: Duration(5 * time.Second),
			CommandTimeout: Duration(5 * time.Second),
			StaleAfter:     Duration(10 * time.Second),
			MaxConcurrent:  8,
		},
		AI: AIConfig{
			Enabled:        false,
			BaseURL:        DefaultAIBaseURL,
			Model:          "gpt-4o-mini",
			APIKeyEnv:      "BARE_EYE_API_KEY",
			Timeout:        Duration(90 * time.Second),
			CommandTimeout: Duration(2 * time.Minute),
			MaxSteps:       12,
		},
		Servers: []ServerConfig{},
	}, nil
}

// EnsureDir creates the config directory with owner-only permissions.
func EnsureDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create config directory: %w", err)
	}
	// MkdirAll is subject to umask; tighten explicitly.
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("set config directory permissions: %w", err)
	}
	return dir, nil
}

// Exists reports whether a config file is present.
func Exists() (bool, error) {
	p, err := Path()
	if err != nil {
		return false, err
	}
	_, statErr := os.Stat(p)
	if statErr == nil {
		return true, nil
	}
	if errors.Is(statErr, os.ErrNotExist) {
		return false, nil
	}
	return false, statErr
}

// Load reads, expands, defaults, and validates the configuration.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotInitialized
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(data)
}

// Parse decodes a config document and applies defaults and validation.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// normalize fills defaults and expands paths.
func (c *Config) normalize() error {
	if c.Version == 0 {
		c.Version = Version
	}
	def, err := Default()
	if err != nil {
		return err
	}
	if c.Monitoring.Interval <= 0 {
		c.Monitoring.Interval = def.Monitoring.Interval
	}
	if c.Monitoring.ConnectTimeout <= 0 {
		c.Monitoring.ConnectTimeout = def.Monitoring.ConnectTimeout
	}
	if c.Monitoring.CommandTimeout <= 0 {
		c.Monitoring.CommandTimeout = def.Monitoring.CommandTimeout
	}
	if c.Monitoring.StaleAfter <= 0 {
		c.Monitoring.StaleAfter = def.Monitoring.StaleAfter
	}
	if c.Monitoring.MaxConcurrent <= 0 {
		c.Monitoring.MaxConcurrent = def.Monitoring.MaxConcurrent
	}
	if strings.TrimSpace(c.AI.BaseURL) == "" {
		c.AI.BaseURL = def.AI.BaseURL
	}
	if strings.TrimSpace(c.AI.Model) == "" {
		c.AI.Model = def.AI.Model
	}
	if strings.TrimSpace(c.AI.APIKeyEnv) == "" {
		c.AI.APIKeyEnv = def.AI.APIKeyEnv
	}
	if c.AI.Timeout <= 0 {
		c.AI.Timeout = def.AI.Timeout
	}
	if c.AI.CommandTimeout <= 0 {
		c.AI.CommandTimeout = def.AI.CommandTimeout
	}
	if c.AI.MaxSteps <= 0 {
		c.AI.MaxSteps = def.AI.MaxSteps
	}
	if strings.TrimSpace(c.SSH.DefaultKey) == "" {
		c.SSH.DefaultKey = def.SSH.DefaultKey
	}
	if strings.TrimSpace(c.SSH.KnownHosts) == "" {
		c.SSH.KnownHosts = def.SSH.KnownHosts
	}
	if c.Servers == nil {
		c.Servers = []ServerConfig{}
	}
	for i := range c.Servers {
		s := &c.Servers[i]
		if s.Port == 0 {
			s.Port = 22
		}
		if s.Auth.Method == "" {
			s.Auth.Method = AuthKey
		}
		if s.User == "" {
			s.User = c.SSH.DefaultUser
		}
	}
	for _, p := range []*string{&c.SSH.DefaultKey, &c.SSH.KnownHosts} {
		expanded, err := expandPath(*p)
		if err != nil {
			return err
		}
		*p = expanded
	}
	for i := range c.Servers {
		if c.Servers[i].Auth.Method == AuthKey && strings.TrimSpace(c.Servers[i].Auth.KeyPath) == "" {
			c.Servers[i].Auth.KeyPath = c.SSH.DefaultKey
		}
		if c.Servers[i].Auth.KeyPath != "" {
			expanded, err := expandPath(c.Servers[i].Auth.KeyPath)
			if err != nil {
				return err
			}
			c.Servers[i].Auth.KeyPath = expanded
		}
	}
	return nil
}

// Validate checks invariants and returns contextual errors.
func (c *Config) Validate() error {
	if c.Version != Version {
		return fmt.Errorf("unsupported config version %d (expected %d)", c.Version, Version)
	}
	if c.Monitoring.Interval.Duration() < 500*time.Millisecond {
		return fmt.Errorf("monitoring.interval must be at least 500ms")
	}
	if c.Monitoring.Interval.Duration() > 24*time.Hour {
		return fmt.Errorf("monitoring.interval is unreasonably large")
	}
	for name, d := range map[string]Duration{
		"monitoring.connect_timeout": c.Monitoring.ConnectTimeout,
		"monitoring.command_timeout": c.Monitoring.CommandTimeout,
		"monitoring.stale_after":     c.Monitoring.StaleAfter,
	} {
		if d.Duration() <= 0 || d.Duration() > 10*time.Minute {
			return fmt.Errorf("%s must be between 0 and 10m", name)
		}
	}
	if c.Monitoring.MaxConcurrent < 1 || c.Monitoring.MaxConcurrent > 128 {
		return fmt.Errorf("monitoring.max_concurrent must be between 1 and 128")
	}
	if err := validateThresholds(c.Monitoring.Thresholds); err != nil {
		return err
	}
	if err := c.AI.Validate(); err != nil {
		return err
	}
	seen := make(map[string]bool, len(c.Servers))
	for i, s := range c.Servers {
		if err := s.Validate(); err != nil {
			return fmt.Errorf("servers[%d]: %w", i, err)
		}
		if seen[s.ID] {
			return fmt.Errorf("servers[%d]: duplicate id %q", i, s.ID)
		}
		seen[s.ID] = true
	}
	return nil
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidID reports whether an ID satisfies the server ID pattern.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Validate checks one server entry.
func (s ServerConfig) Validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return errors.New("id is required")
	}
	if !idPattern.MatchString(s.ID) {
		return fmt.Errorf("id %q may only contain letters, digits, dots, dashes and underscores", s.ID)
	}
	if strings.TrimSpace(s.Host) == "" {
		return errors.New("host is required")
	}
	if err := validateHost(s.Host); err != nil {
		return err
	}
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("port %d is out of range 1-65535", s.Port)
	}
	if strings.TrimSpace(s.User) == "" {
		return errors.New("user is required")
	}
	if strings.ContainsAny(s.User, " \t\r\n@/\\") {
		return fmt.Errorf("user %q contains invalid characters", s.User)
	}
	switch s.Auth.Method {
	case AuthKey:
		if strings.TrimSpace(s.Auth.KeyPath) == "" {
			return errors.New("auth.key_path is required for key authentication")
		}
	case AuthAgent, AuthPassword:
	default:
		return fmt.Errorf("auth.method %q must be one of key, agent, password", s.Auth.Method)
	}
	for name, d := range map[string]Duration{
		"connect_timeout": s.ConnectTimeout,
		"command_timeout": s.CommandTimeout,
		"interval":        s.Interval,
	} {
		if d != 0 && (d.Duration() < 100*time.Millisecond || d.Duration() > 10*time.Minute) {
			return fmt.Errorf("%s must be between 100ms and 10m", name)
		}
	}
	return nil
}

// Validate checks the optional AI automation settings.
func (a AIConfig) Validate() error {
	if !a.Enabled {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(a.BaseURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("ai.base_url must be an http(s) URL")
	}
	if strings.TrimSpace(a.Model) == "" {
		return fmt.Errorf("ai.model is required when ai.enabled is true")
	}
	if a.Timeout.Duration() < time.Second || a.Timeout.Duration() > 10*time.Minute {
		return fmt.Errorf("ai.timeout must be between 1s and 10m")
	}
	if a.CommandTimeout.Duration() < time.Second || a.CommandTimeout.Duration() > 30*time.Minute {
		return fmt.Errorf("ai.command_timeout must be between 1s and 30m")
	}
	if a.MaxSteps < 1 || a.MaxSteps > 50 {
		return fmt.Errorf("ai.max_steps must be between 1 and 50")
	}
	return nil
}

func validateThresholds(t Thresholds) error {
	for name, v := range map[string]float64{
		"cpu": t.CPU, "mem": t.Mem, "swap": t.Swap, "disk": t.Disk,
	} {
		if v < 0 || v > 100 {
			return fmt.Errorf("monitoring.thresholds.%s must be between 0 and 100", name)
		}
	}
	return nil
}

func validateHost(host string) error {
	if strings.ContainsAny(host, " \t\r\n/\\@") {
		return fmt.Errorf("host %q contains invalid characters", host)
	}
	if net.ParseIP(host) != nil {
		return nil
	}
	if len(host) > 253 {
		return errors.New("host is too long")
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 {
			return fmt.Errorf("host %q is not a valid hostname or IP address", host)
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return fmt.Errorf("host %q is not a valid hostname or IP address", host)
			}
		}
	}
	return nil
}

// expandPath expands a leading ~ and converts to an absolute path.
func expandPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", nil
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if p == "~" {
			p = home
		} else {
			p = filepath.Join(home, p[2:])
		}
	}
	return filepath.Clean(p), nil
}

// ExpandPath is the exported form of expandPath for CLI callers.
func ExpandPath(p string) (string, error) { return expandPath(p) }

// Save writes the configuration atomically with 0600 permissions.
func (c *Config) Save() error {
	if err := c.Validate(); err != nil {
		return err
	}
	dir, err := EnsureDir()
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return WriteFileAtomic(filepath.Join(dir, FileName), data, 0o600)
}

// SaveTo writes the config to an explicit path (used by tests).
func (c *Config) SaveTo(path string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return WriteFileAtomic(path, data, 0o600)
}

// WriteFileAtomic writes data to a temp file then renames it into place.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return os.Chmod(path, perm)
}

// FindServer returns a pointer to the server with the given ID.
func (c *Config) FindServer(id string) (*ServerConfig, error) {
	for i := range c.Servers {
		if c.Servers[i].ID == id {
			return &c.Servers[i], nil
		}
	}
	return nil, fmt.Errorf("server %q not found", id)
}

// UpsertServer inserts or replaces a server by ID.
func (c *Config) UpsertServer(s ServerConfig) {
	for i := range c.Servers {
		if c.Servers[i].ID == s.ID {
			c.Servers[i] = s
			return
		}
	}
	c.Servers = append(c.Servers, s)
}

// RemoveServer deletes a server by ID and reports whether it existed.
func (c *Config) RemoveServer(id string) bool {
	for i := range c.Servers {
		if c.Servers[i].ID == id {
			c.Servers = append(c.Servers[:i], c.Servers[i+1:]...)
			return true
		}
	}
	return false
}

// Slug converts a display name into a valid server ID.
func Slug(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '.' || r == '_' || r == '-':
			if !lastDash && b.Len() > 0 {
				b.WriteRune(r)
				lastDash = true
			}
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), ".-_")
	if out == "" {
		out = "server"
	}
	return out
}
