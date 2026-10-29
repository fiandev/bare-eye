# Bare Eye

**One terminal. Multiple servers. No mandatory agents.**

Bare Eye is a local-first terminal dashboard for monitoring Linux VPS and
bare-metal servers over SSH. It collects CPU, memory, swap, load, disk, network
and uptime data with fixed read-only commands and renders everything in one
TUI. Nothing is installed on the monitored hosts.

```
┌─ Bare Eye ─────────────────────────────────────────────────────────────┐
│ Bare Eye 0.1.0   hosts 3 · online 2 · issues 1   interval 2s           │
└─ ? help · a add · e edit · t test · r reconnect · d toggle · q quit────┘
```

## Requirements

- Go 1.26+ to build (required by `golang.org/x/crypto`; `gotui` itself needs 1.24+).
- A modern ANSI terminal. TrueColor is used when available; basic terminals
  remain usable.
- Linux on the monitored servers. Non-Linux remotes are reported as
  `unsupported`, never faked.
- `ssh-keygen` for passphrase-protected key generation (an Ed25519 key is
  generated in pure Go otherwise).
- An SSH account that can read `/proc` and run `df`, `uname`, `hostname`,
  `grep` and `cat`. No root or agent required.

## Build

```sh
go build -o bare-eye ./cmd/bare-eye
# optional release metadata
go build -ldflags "-X github.com/fiandev/bare-eye/internal/version.Version=0.1.0" ./cmd/bare-eye
```

## Quick start

```sh
bare-eye init                 # create ~/.config/bare-eye (0700) and a config (0600)
bare-eye keygen               # optional dedicated Ed25519 key pair
bare-eye key show             # print the public key + SHA256 fingerprint
bare-eye key install web-01   # guided append to ~/.ssh/authorized_keys
bare-eye hosts add            # add a host (or use flags)
bare-eye hosts test web-01    # verify connectivity before monitoring
bare-eye                      # launch the dashboard
```

`bare-eye init` is interactive on a terminal and offers to generate a key. On a
non-interactive shell it creates only the configuration; use `bare-eye keygen`.

Adding a host non-interactively:

```sh
bare-eye hosts add \
  --id web-01 --name "Web 01" \
  --host 203.0.113.10 --port 22 --user monitor \
  --auth key --key ~/.config/bare-eye/keys/bare-eye-ed25519
```

Password authentication:

```sh
bare-eye hosts add --id db-01 --host 203.0.113.11 --user admin --auth password
bare-eye config set password db-01        # prompted, never echoed
```

## Dashboard

| Key | Action |
|---|---|
| `↑`/`↓`, `j`/`k` | select server |
| `Enter` | host detail |
| `Esc` | back / cancel |
| `a` | add server |
| `e` | edit selected server |
| `t` | test SSH connection |
| `r` | reconnect now |
| `d` | enable / disable monitoring |
| `x` | remove server locally (with confirmation) |
| `/` | filter inventory |
| `K` | show the Bare Eye public key and fingerprint |
| `?` | keyboard help |
| `q`, `Ctrl+C` | quit |

Status values are intentionally honest:

- `online` – latest sample is fresh.
- `STALE` – last successful sample is older than `stale_after`; values are
  marked as the last sample, not live.
- `offline` – connection or command failed. Last known values stay visible but
  are labelled `last sample`.
- `auth` – authentication failed or a password/passphrase is required.
- `host-key` – unknown or changed host key. Bare Eye will not connect until the
  fingerprint is explicitly trusted (unknown) or the change is investigated
  (changed keys are never auto-replaced).
- `unsupported` – remote OS is not Linux.
- `disabled` – monitoring is off for this host.

## CLI reference

```
bare-eye                          start the dashboard
bare-eye init [--force] [--no-key]
bare-eye keygen [--force] [--path P] [--comment C]
bare-eye key show [--path P]
bare-eye key install <id> [--yes]
bare-eye key delete [--path P] [--yes]
bare-eye hosts list [--json]
bare-eye hosts add [flags]
bare-eye hosts test <id> [--yes] [--passphrase P]
bare-eye hosts remove <id> [--yes]
bare-eye hosts enable <id>
bare-eye hosts disable <id>
bare-eye config path
bare-eye config set password <id> [password]
bare-eye version
```

Passing a password as a command-line argument is supported for automation but
warns: shell history and process listings can expose it. Omit it to be prompted
securely.

## Configuration

Default location: `~/.config/bare-eye/` (respects `XDG_CONFIG_HOME`; override
with `BARE_EYE_CONFIG_DIR`).

```text
~/.config/bare-eye/
├── config.yaml          # 0600, no secrets
├── secrets.enc          # 0600, AES-GCM encrypted passwords
├── secrets.key          # 0600, local encryption key
├── keys/
│   ├── bare-eye-ed25519       # 0600
│   └── bare-eye-ed25519.pub   # 0600
└── known_hosts          # 0600, Bare Eye-managed host keys
```

See [`config.example.yaml`](config.example.yaml) for a full example. Key points:

- `monitoring.interval` (default `2s`) controls polling; `stale_after` (default
  `10s`) controls when data is marked stale.
- `max_concurrent` bounds simultaneous SSH probes (default `8`).
- Per-server `interval`, `connect_timeout` and `command_timeout` override the
  globals.
- `monitoring.thresholds` are percentages. **`0` disables a threshold**; all
  thresholds ship disabled.
- Unknown fields are preserved on rewrite where the YAML shape allows it.
- Configuration writes are atomic (temp file + rename) and keep `0600`.

### Authentication

`auth.method` is one of:

- `key` – private key at `auth.key_path` (or `ssh.default_key`).
- `agent` – keys from `SSH_AUTH_SOCK`.
- `password` – password stored via `bare-eye config set password <id>`, or
  prompted at connection/test time.

Encrypted private keys are supported by the CLI (`hosts test` prompts for the
passphrase). Background monitoring cannot prompt, so use an unencrypted key,
an SSH agent, or test/install interactively first.

## Security model

- **Host keys are always verified.** Bare Eye uses its own `known_hosts`; it
  never enables an accept-all callback. Unknown keys require explicit
  fingerprint confirmation. Changed keys block the connection and show both
  fingerprints; the stored key is never silently replaced.
- **Read-only collection.** The remote script only reads `/proc`, runs `df -P`,
  `uname`, `hostname`, and `grep -c`; no user input is interpolated into
  commands.
- **No telemetry.** Nothing leaves the local machine except SSH connections to
  configured hosts.
- **No secrets in `config.yaml` or logs.** Passwords live in a separate
  AES-GCM-encrypted file. The encryption key sits beside it with `0600`
  permissions, which protects against casual disclosure (grep, backups, pasted
  config output) but not against someone who can read the whole config
  directory. Prefer keys or an agent when that matters.
- **Guided key installation never overwrites** `authorized_keys`; it appends
  only when the key is absent, after showing the target and action.

## Architecture

```
cmd/bare-eye          CLI entry point
internal/cli          command dispatch, prompts, init
internal/config       schema, defaults, validation, atomic writes, secrets
internal/keys         Ed25519 generation, public key/fingerprint, signers
internal/sshx         SSH dialing, strict host keys, bounded commands
internal/collector    fixed remote script + defensive /proc parsing
internal/scheduler    bounded worker pool, backoff, per-host isolation
internal/state        status model, bounded history, event log
internal/tui          gotui dashboard, detail view, forms, modals
```

The collector returns typed metrics; it never touches widgets. The TUI reads
snapshots from a concurrency-safe store. Each host has an isolated worker,
connection, and collector, so one slow or broken host cannot delay the others.

## Development

```sh
go test ./...
go test -race ./...
go vet ./...
```

Tests cover config validation and permissions, encrypted secrets, metric
parsing and rate derivation, status/staleness transitions, key generation,
scheduler isolation/concurrency, and SSH integration (unknown host key, changed
host key, bad password, command timeout) against an in-process SSH server.

## Troubleshooting

See [`docs/TROUBLESHOOTING.md`](docs/TROUBLESHOOTING.md).

## Limitations (MVP)

- Linux remotes only; no Windows/macOS remote monitoring.
- No jump hosts, tunnels, or password-manager integration.
- No persisted history or alerting; sparklines are in-memory only.
- Passphrase-protected keys cannot be unlocked by background polling.
- IPv6 and DNS names are supported; host-key entries use the standard
  `[host]:port` form for non-22 ports.

## License

MIT.
