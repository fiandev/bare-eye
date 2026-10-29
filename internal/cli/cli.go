// Package cli implements Bare Eye's command-line interface.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/fiandev/bare-eye/internal/config"
	"github.com/fiandev/bare-eye/internal/keys"
	"github.com/fiandev/bare-eye/internal/scheduler"
	"github.com/fiandev/bare-eye/internal/sshx"
	"github.com/fiandev/bare-eye/internal/state"
	"github.com/fiandev/bare-eye/internal/tui"
	"github.com/fiandev/bare-eye/internal/version"
)

// Run dispatches a command and returns a process exit code.
func Run(args []string) int {
	if len(args) == 0 {
		return runDashboard()
	}
	switch args[0] {
	case "init":
		return cmdInit(args[1:])
	case "keygen":
		return cmdKeygen(args[1:])
	case "key":
		return cmdKey(args[1:])
	case "hosts":
		return cmdHosts(args[1:])
	case "config":
		return cmdConfig(args[1:])
	case "-screenshot":
		return runDashboard()
	case "version", "--version", "-v":
		fmt.Printf("bare-eye %s (commit %s, built %s)\n", version.Version, version.Commit, version.Date)
		return 0
	case "help", "--help", "-h":
		printHelp()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "bare-eye: unknown command %q\n\n", args[0])
		printHelp()
		return 2
	}
}

func printHelp() {
	fmt.Print(`Bare Eye — multi-server SSH monitoring TUI

Usage:
  bare-eye                     Start the interactive dashboard
  bare-eye init                Initialize config and optionally generate a key
  bare-eye keygen              Generate a Bare Eye SSH key pair
  bare-eye key show            Print the public key and fingerprint
  bare-eye key install <id>    Install the public key on a server (guided)
  bare-eye key delete          Delete the local key pair (explicit)
  bare-eye hosts list          List configured hosts
  bare-eye hosts add           Add a host (flags or interactive)
  bare-eye hosts test <id>     Test SSH connectivity and authentication
  bare-eye hosts remove <id>   Remove a host locally
  bare-eye hosts enable <id>   Enable monitoring for a host
  bare-eye hosts disable <id>  Disable monitoring for a host
  bare-eye config path         Print resolved config paths
  bare-eye config set password <id> [password]
                               Store an SSH password (encrypted at rest)
  bare-eye version             Print version/build information

Configuration lives in ~/.config/bare-eye/ (or $XDG_CONFIG_HOME/bare-eye,
or $BARE_EYE_CONFIG_DIR). Bare Eye never stores plaintext passwords in
config.yaml and never disables SSH host-key verification.
`)
}

func runDashboard() int {
	cfg, secrets, err := loadConfig()
	if errors.Is(err, config.ErrNotInitialized) {
		fmt.Fprintln(os.Stderr, "Bare Eye is not initialized.")
		if !isInteractive() {
			fmt.Fprintln(os.Stderr, "Run `bare-eye init` first.")
			return 1
		}
		if code := cmdInit(nil); code != 0 {
			return code
		}
		cfg, secrets, err = loadConfig()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	store := state.New(cfg.Monitoring.StaleAfter.Duration())
	events := state.NewEventLog(200)
	prober := scheduler.NewSSHProber(cfg, secrets)
	sched := scheduler.New(cfg, store, events, prober)
	sched.Start()
	defer sched.Stop()

	if err := tui.Run(cfg, secrets, store, events, sched, version.Version); err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	return 0
}

func loadConfig() (*config.Config, *config.Secrets, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	dir, err := config.Dir()
	if err != nil {
		return nil, nil, err
	}
	secrets, err := config.LoadSecrets(dir)
	if err != nil {
		return nil, nil, err
	}
	return cfg, secrets, nil
}

func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// splitPositional allows flags to follow the positional argument
// (the standard flag package stops parsing at the first non-flag).
func splitPositional(args []string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}

func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	force := fs.Bool("force", false, "overwrite an existing config (explicit)")
	noKey := fs.Bool("no-key", false, "do not offer to generate an SSH key")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dir, err := config.EnsureDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	exists, err := config.Exists()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	cfg, err := config.Default()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	if exists && !*force {
		fmt.Printf("Config already exists: %s\n", filepath.Join(dir, config.FileName))
		fmt.Println("Leaving it untouched. Use --force to overwrite explicitly.")
	} else {
		if err := cfg.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
			return 1
		}
		fmt.Printf("Initialized %s (0700) and wrote %s (0600)\n", dir, filepath.Join(dir, config.FileName))
	}

	fmt.Printf("  config:     %s\n", filepath.Join(dir, config.FileName))
	fmt.Printf("  keys:       %s\n", filepath.Join(dir, "keys"))
	fmt.Printf("  known_hosts: %s\n", cfg.SSH.KnownHosts)

	if *noKey || !isInteractive() {
		return 0
	}
	if keys.Exists(cfg.SSH.DefaultKey) {
		fmt.Printf("\nSSH key already present: %s\n", cfg.SSH.DefaultKey)
		showKey(cfg.SSH.DefaultKey)
		return 0
	}
	if !confirm("Generate a dedicated Ed25519 SSH key now?", true) {
		fmt.Println("Skipped key generation. Run `bare-eye keygen` when ready.")
		return 0
	}
	pass, err := promptNewPassphrase()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	if err := keys.Generate(cfg.SSH.DefaultKey, pass, "bare-eye", false); err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	fmt.Printf("\nGenerated key pair:\n  private: %s (0600)\n  public:  %s.pub\n", cfg.SSH.DefaultKey, cfg.SSH.DefaultKey)
	showKey(cfg.SSH.DefaultKey)
	fmt.Println("\nNext: authorize the public key on each server, then add the host:")
	fmt.Println("  bare-eye key install <host-id>")
	fmt.Println("  bare-eye hosts add")
	return 0
}

func cmdKeygen(args []string) int {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	force := fs.Bool("force", false, "overwrite an existing key (explicit)")
	path := fs.String("path", "", "key path (default: Bare Eye config keys directory)")
	comment := fs.String("comment", "bare-eye", "key comment")
	passFlag := fs.String("passphrase", "", "passphrase (prefer the interactive prompt)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, _, err := loadConfig()
	if errors.Is(err, config.ErrNotInitialized) {
		cfg, err = config.Default()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	keyPath := *path
	if keyPath == "" {
		keyPath = cfg.SSH.DefaultKey
	}
	if keyPath, err = config.ExpandPath(keyPath); err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	pass := *passFlag
	if pass == "" && isInteractive() {
		if pass, err = promptNewPassphrase(); err != nil {
			fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
			return 1
		}
	}
	if err := keys.Generate(keyPath, pass, *comment, *force); err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	fmt.Printf("Generated key pair at %s (private 0600)\n", keyPath)
	showKey(keyPath)
	return 0
}

func cmdKey(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: bare-eye key show|install <id>")
		return 2
	}
	switch args[0] {
	case "show":
		cfg, _, err := loadConfig()
		if errors.Is(err, config.ErrNotInitialized) {
			cfg, err = config.Default()
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
			return 1
		}
		fs := flag.NewFlagSet("key show", flag.ContinueOnError)
		path := fs.String("path", "", "key path (default: configured key)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		p := *path
		if p == "" {
			p = cfg.SSH.DefaultKey
		}
		showKey(p)
		return 0
	case "install":
		return cmdKeyInstall(args[1:])
	case "delete":
		return cmdKeyDelete(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown key command %q\n", args[0])
		return 2
	}
}

func showKey(path string) {
	pub, err := keys.PublicKey(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return
	}
	fp, _ := keys.Fingerprint(path)
	fmt.Printf("\nPublic key:\n%s\n\nFingerprint (SHA256):\n%s\n", pub, fp)
}

func cmdKeyInstall(args []string) int {
	id, rest := splitPositional(args)
	fs := flag.NewFlagSet("key install", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if id == "" && fs.NArg() == 1 {
		id = fs.Arg(0)
	}
	if id == "" {
		fmt.Fprintln(os.Stderr, "usage: bare-eye key install <host-id> [--yes]")
		return 2
	}
	cfg, secrets, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	srv, err := cfg.FindServer(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	keyPath := srv.Auth.KeyPath
	if keyPath == "" {
		keyPath = cfg.SSH.DefaultKey
	}
	pub, err := keys.PublicKey(keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	fmt.Printf("Target: %s (%s@%s)\nAction: append the Bare Eye public key to ~/.ssh/authorized_keys\n", srv.DisplayName(), srv.User, srv.Addr())
	fmt.Printf("Key:    %s\n", pub)
	if !*yes && !confirm("Proceed?", false) {
		fmt.Println("Cancelled.")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	password := ""
	if srv.Auth.Method == config.AuthPassword {
		if p, ok := secrets.Get(srv.ID); ok {
			password = p
		} else if isInteractive() {
			p, perr := promptSecret("SSH password for " + srv.User + "@" + srv.Addr() + ": ")
			if perr != nil {
				fmt.Fprintf(os.Stderr, "bare-eye: %v\n", perr)
				return 1
			}
			password = p
		}
	}
	if err := scheduler.InstallKey(ctx, *srv, cfg, secrets, pub, password, ""); err != nil {
		var unknown *sshx.UnknownHostKeyError
		if errors.As(err, &unknown) && !*yes {
			fmt.Printf("\nUnknown host key for %s\nFingerprint: %s\n", unknown.Host, unknown.Fingerprint)
			if !confirm("Trust and save this key?", false) {
				fmt.Println("Cancelled.")
				return 1
			}
			if err := sshx.AppendKnownHost(cfg.SSH.KnownHosts, unknown.Host, unknown.Key); err != nil {
				fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
				return 1
			}
			err = scheduler.InstallKey(ctx, *srv, cfg, secrets, pub, password, "")
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
			return 1
		}
	}
	fmt.Println("Public key installed (authorized_keys was appended, never overwritten).")
	return 0
}

func cmdKeyDelete(args []string) int {
	fs := flag.NewFlagSet("key delete", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	path := fs.String("path", "", "key path (default: configured key)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, _, err := loadConfig()
	if errors.Is(err, config.ErrNotInitialized) {
		cfg, err = config.Default()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	p := *path
	if p == "" {
		p = cfg.SSH.DefaultKey
	}
	if !keys.Exists(p) {
		fmt.Fprintf(os.Stderr, "bare-eye: no key at %s\n", p)
		return 1
	}
	fmt.Printf("Deleting %s and %s.pub\n", p, p)
	fmt.Println("Warning: this does NOT revoke copies already installed on remote servers.")
	fmt.Println("Remove the corresponding line from each server's authorized_keys to revoke access.")
	if !*yes && !confirm("Delete the local key pair?", false) {
		fmt.Println("Cancelled.")
		return 1
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	if err := os.Remove(p + ".pub"); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	fmt.Println("Local key pair deleted.")
	return 0
}

func cmdHosts(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: bare-eye hosts list|add|test|remove|enable|disable")
		return 2
	}
	switch args[0] {
	case "list":
		return cmdHostsList(args[1:])
	case "add":
		return cmdHostsAdd(args[1:])
	case "test":
		return cmdHostsTest(args[1:])
	case "remove":
		return cmdHostsRemove(args[1:])
	case "enable", "disable":
		return cmdHostsToggle(args[0], args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown hosts command %q\n", args[0])
		return 2
	}
}

func cmdHostsList(args []string) int {
	fs := flag.NewFlagSet("hosts list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, _, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	if *asJSON {
		type row struct {
			ID      string   `json:"id"`
			Name    string   `json:"name"`
			Host    string   `json:"host"`
			Port    int      `json:"port"`
			User    string   `json:"user"`
			Auth    string   `json:"auth"`
			Enabled bool     `json:"enabled"`
			Tags    []string `json:"tags,omitempty"`
		}
		out := make([]row, 0, len(cfg.Servers))
		for _, s := range cfg.Servers {
			out = append(out, row{s.ID, s.Name, s.Host, s.Port, s.User, s.Auth.Method, s.Enabled, s.Tags})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return 0
	}
	if len(cfg.Servers) == 0 {
		fmt.Println("No hosts configured. Run `bare-eye hosts add`.")
		return 0
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tADDRESS\tUSER\tAUTH\tENABLED")
	for _, s := range cfg.Servers {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%t\n", s.ID, s.DisplayName(), s.Addr(), s.User, s.Auth.Method, s.Enabled)
	}
	_ = w.Flush()
	return 0
}

func cmdHostsAdd(args []string) int {
	fs := flag.NewFlagSet("hosts add", flag.ContinueOnError)
	id := fs.String("id", "", "stable server ID")
	name := fs.String("name", "", "friendly name")
	host := fs.String("host", "", "hostname or IP")
	port := fs.Int("port", 22, "SSH port")
	user := fs.String("user", "", "SSH username")
	auth := fs.String("auth", "key", "auth method: key, agent, password")
	keyPath := fs.String("key", "", "private key path (key auth)")
	tags := fs.String("tags", "", "comma-separated tags")
	disabled := fs.Bool("disabled", false, "add the host disabled")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, _, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	if *host == "" && isInteractive() {
		*host = prompt("Host (hostname or IP)", "")
	}
	if *name == "" {
		*name = *host
	}
	if *user == "" {
		if cfg.SSH.DefaultUser != "" {
			*user = cfg.SSH.DefaultUser
		} else if isInteractive() {
			*user = prompt("SSH user", os.Getenv("USER"))
		}
	}
	if *id == "" {
		*id = config.Slug(*name)
	}
	if *keyPath == "" && *auth == config.AuthKey {
		*keyPath = cfg.SSH.DefaultKey
	}
	srv := config.ServerConfig{
		ID:      *id,
		Name:    *name,
		Host:    *host,
		Port:    *port,
		User:    *user,
		Auth:    config.AuthConfig{Method: *auth, KeyPath: *keyPath},
		Enabled: !*disabled,
	}
	if *tags != "" {
		for _, t := range strings.Split(*tags, ",") {
			if t = strings.TrimSpace(t); t != "" {
				srv.Tags = append(srv.Tags, t)
			}
		}
	}
	if err := srv.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	if _, err := cfg.FindServer(srv.ID); err == nil {
		fmt.Fprintf(os.Stderr, "bare-eye: host %q already exists (edit config or remove it first)\n", srv.ID)
		return 1
	}
	cfg.UpsertServer(srv)
	if err := cfg.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	fmt.Printf("Added %s (%s@%s)\n", srv.ID, srv.User, srv.Addr())
	if srv.Auth.Method == config.AuthPassword && isInteractive() {
		fmt.Printf("Store a password now? Run: bare-eye config set password %s\n", srv.ID)
	}
	return 0
}

func cmdHostsTest(args []string) int {
	id, rest := splitPositional(args)
	fs := flag.NewFlagSet("hosts test", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "trust an unknown host key without prompting")
	passphrase := fs.String("passphrase", "", "private-key passphrase (prefer the prompt)")
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if id == "" && fs.NArg() == 1 {
		id = fs.Arg(0)
	}
	if id == "" {
		fmt.Fprintln(os.Stderr, "usage: bare-eye hosts test <id> [--yes]")
		return 2
	}
	cfg, secrets, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	srv, err := cfg.FindServer(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	err = testWithTrust(ctx, *srv, cfg, secrets, *yes, *passphrase)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", srv.ID, err)
		return 1
	}
	fmt.Printf("OK %s (%s@%s)\n", srv.ID, srv.User, srv.Addr())
	return 0
}

func testWithTrust(ctx context.Context, srv config.ServerConfig, cfg *config.Config, secrets *config.Secrets, autoYes bool, passphrase string) error {
	pw := ""
	if srv.Auth.Method == config.AuthPassword {
		if p, ok := secrets.Get(srv.ID); ok {
			pw = p
		} else if isInteractive() {
			p, err := promptSecret("SSH password for " + srv.User + "@" + srv.Addr() + ": ")
			if err != nil {
				return err
			}
			pw = p
		}
	}
	if passphrase == "" && srv.Auth.Method == config.AuthKey && isInteractive() {
		// Only prompt if the key is actually encrypted.
		path := srv.Auth.KeyPath
		if path == "" {
			path = cfg.SSH.DefaultKey
		}
		if _, err := keys.LoadSigner(path, ""); errors.Is(err, keys.ErrPassphraseRequired) {
			p, err := promptSecret("Key passphrase: ")
			if err != nil {
				return err
			}
			passphrase = p
		}
	}
	err := scheduler.TestConnection(ctx, srv, cfg, secrets, pw, passphrase)
	var unknown *sshx.UnknownHostKeyError
	if errors.As(err, &unknown) {
		fmt.Printf("Unknown host key for %s\nFingerprint (SHA256): %s\n", unknown.Host, unknown.Fingerprint)
		if autoYes || confirm("Trust and save this host key?", false) {
			if aerr := sshx.AppendKnownHost(cfg.SSH.KnownHosts, unknown.Host, unknown.Key); aerr != nil {
				return aerr
			}
			return scheduler.TestConnection(ctx, srv, cfg, secrets, pw, passphrase)
		}
		return errors.New("host key was not trusted")
	}
	return err
}

func cmdHostsRemove(args []string) int {
	id, rest := splitPositional(args)
	fs := flag.NewFlagSet("hosts remove", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if id == "" && fs.NArg() == 1 {
		id = fs.Arg(0)
	}
	if id == "" {
		fmt.Fprintln(os.Stderr, "usage: bare-eye hosts remove <id> [--yes]")
		return 2
	}
	cfg, secrets, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	srv, err := cfg.FindServer(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	if !*yes && !confirm(fmt.Sprintf("Remove %s locally? This does not change the server.", srv.DisplayName()), false) {
		fmt.Println("Cancelled.")
		return 1
	}
	cfg.RemoveServer(srv.ID)
	secrets.Delete(srv.ID)
	if err := cfg.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	if err := secrets.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	fmt.Printf("Removed %s\n", srv.ID)
	return 0
}

func cmdHostsToggle(action string, args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "usage: bare-eye hosts %s <id>\n", action)
		return 2
	}
	cfg, _, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	srv, err := cfg.FindServer(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	srv.Enabled = action == "enable"
	if err := cfg.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	fmt.Printf("%s: enabled=%t\n", srv.ID, srv.Enabled)
	return 0
}

func cmdConfig(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: bare-eye config path | config set password <id> [password]")
		return 2
	}
	switch args[0] {
	case "path":
		dir, err := config.Dir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
			return 1
		}
		keysDir, _ := config.KeysDir()
		fmt.Printf("config dir:  %s\n", dir)
		fmt.Printf("config file: %s\n", filepath.Join(dir, config.FileName))
		fmt.Printf("keys dir:    %s\n", keysDir)
		known, _ := config.DefaultKnownHosts()
		fmt.Printf("known_hosts: %s\n", known)
		return 0
	case "set":
		return cmdConfigSet(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown config command %q\n", args[0])
		return 2
	}
}

func cmdConfigSet(args []string) int {
	if len(args) < 2 || args[0] != "password" {
		fmt.Fprintln(os.Stderr, "usage: bare-eye config set password <id> [password]")
		fmt.Fprintln(os.Stderr, "Omit the password to be prompted securely.")
		return 2
	}
	id := args[1]
	cfg, secrets, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	srv, err := cfg.FindServer(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	var pw string
	if len(args) >= 3 {
		pw = args[2]
		fmt.Fprintln(os.Stderr, "warning: passwords passed as arguments may leak via shell history and process listings.")
	} else {
		if !isInteractive() {
			fmt.Fprintln(os.Stderr, "bare-eye: no password provided and stdin is not a terminal")
			return 1
		}
		pw, err = promptSecret("Password for " + srv.User + "@" + srv.Addr() + ": ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
			return 1
		}
		again, err := promptSecret("Repeat password: ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
			return 1
		}
		if pw != again {
			fmt.Fprintln(os.Stderr, "bare-eye: passwords do not match")
			return 1
		}
	}
	secrets.Set(srv.ID, pw)
	if err := secrets.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "bare-eye: %v\n", err)
		return 1
	}
	if srv.Auth.Method != config.AuthPassword {
		fmt.Fprintf(os.Stderr, "note: %s uses auth method %q; set auth.method to password to use it.\n", srv.ID, srv.Auth.Method)
	}
	fmt.Printf("Password stored for %s (encrypted at rest, 0600).\n", srv.ID)
	return 0
}

func prompt(label, def string) string {
	fmt.Printf("%s", label)
	if def != "" {
		fmt.Printf(" [%s]", def)
	}
	fmt.Print(": ")
	var line string
	_, _ = fmt.Fscanln(os.Stdin, &line)
	if line == "" {
		return def
	}
	return line
}

func promptSecret(label string) (string, error) {
	fmt.Print(label)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func promptNewPassphrase() (string, error) {
	if !isInteractive() {
		return "", nil
	}
	fmt.Print("Key passphrase (empty for none): ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	if len(b) == 0 {
		return "", nil
	}
	fmt.Print("Repeat passphrase: ")
	b2, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	if string(b) != string(b2) {
		return "", errors.New("passphrases do not match")
	}
	return string(b), nil
}

func confirm(question string, def bool) bool {
	if !isInteractive() {
		return false
	}
	suffix := " [y/N]: "
	if def {
		suffix = " [Y/n]: "
	}
	fmt.Print(question + suffix)
	var line string
	_, _ = fmt.Fscanln(os.Stdin, &line)
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return def
	}
	return line == "y" || line == "yes"
}
