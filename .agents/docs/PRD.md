# Product Requirements Document (PRD)

## Bare Eye — Multi-Server SSH Monitoring TUI

**Status:** Final  
**Product type:** Local-first CLI/TUI application  
**Primary implementation language:** Go  
**TUI foundation:** [`github.com/metaspartan/gotui/v5`](https://github.com/metaspartan/gotui)  
**Application config directory:** `~/.config/bare-eye/`

## 1. Product summary

Bare Eye is a lightweight terminal application for monitoring multiple bare-metal servers and VPS instances from one local machine. It connects to configured servers over SSH, collects operating-system metrics, and presents a live dashboard in a single TUI.

Bare Eye should minimize setup friction. On first run, it initializes its configuration directory and can generate an SSH key pair with behavior equivalent to `ssh-keygen`. Users can copy the public key to each target server, or choose an existing SSH key. Monitoring should work without installing a permanent monitoring agent on target servers; collection uses SSH and short-lived remote commands in the MVP.

**Product promise:** One terminal. Multiple servers. No mandatory agents.

## 2. Problem statement

Operators with multiple VPS or bare-metal machines often connect to each host individually or open separate monitoring sessions. This fragments visibility, makes it harder to compare resource usage, and adds repetitive SSH setup. Bare Eye consolidates host health and resource utilization into one terminal while keeping server access and configuration under the operator's control.

## 3. Goals

1. Monitor multiple configured servers simultaneously from one TUI.
2. Make SSH setup straightforward with generated or user-supplied keys.
3. Display current CPU, memory, swap, disk, load, uptime, and network information where supported.
4. Keep installation and runtime overhead low, without requiring a central backend.
5. Clearly show unavailable hosts, stale data, authentication failures, and collection errors rather than presenting old metrics as live.
6. Store configuration and Bare Eye-managed SSH key files under `~/.config/bare-eye/` with restrictive filesystem permissions.

## 4. Non-goals for the MVP

- Web dashboard or hosted control plane.
- Centralized time-series database, long-term historical analytics, or alert delivery integrations.
- Automated remediation, remote shell management beyond the diagnostic commands needed for monitoring, or destructive operations on target hosts.
- Mandatory agent installation or privileged access on monitored servers.
- Full replacement for Prometheus, Netdata, Grafana, or enterprise observability systems.

## 5. Target users and primary use cases

**Target users:** Developers, solo operators, small software teams, and infrastructure administrators managing a handful to dozens of VPS or bare-metal servers.

- Check the health of all servers from one terminal.
- Identify the server whose CPU, RAM, swap, disk, or network usage is unusually high.
- Add a new VPS with an address, SSH username, port, and key; validate access before monitoring it.
- Generate a dedicated Bare Eye SSH key and install its public key on a server.
- Pause, reconnect, inspect, or remove a server without interrupting monitoring of other hosts.
- Diagnose a host that cannot be reached or no longer returns metrics.

## 6. Product principles

- **Local-first:** no account, hosted service, or telemetry is required.
- **SSH-first:** use established SSH authentication and host verification mechanisms.
- **Read-only monitoring by default:** remote collection commands must not alter server state.
- **Fault isolation:** one failing host must not freeze or stop updates for other hosts.
- **Honest status:** distinguish connected, collecting, stale, disconnected, and authentication/host-key errors.
- **Accessible TUI:** keyboard navigation is primary; mouse support is supplementary.
- **Explicit security boundaries:** never silently weaken SSH host verification or expose private keys.

## 7. Functional requirements

### 7.1 First-run setup and configuration

On first launch, Bare Eye must:

1. Resolve the user's home directory and initialize `~/.config/bare-eye/`.
2. Create the directory with owner-only permissions (`0700`) on Unix-like systems where applicable.
3. Create a versioned configuration file with restrictive permissions (`0600`).
4. Offer to generate a dedicated SSH key pair, use an existing private key, or configure servers first and defer key setup.
5. Show the exact paths of any generated key pair and explain that the public key must be authorized on each target server.
6. Never overwrite an existing key or configuration file without explicit confirmation.

Suggested directory layout:

```text
~/.config/bare-eye/
├── config.yaml
├── keys/
│   ├── bare-eye-ed25519
│   └── bare-eye-ed25519.pub
├── known_hosts
└── state/
    └── cache.json
```

`known_hosts` is included only if Bare Eye deliberately manages a separate known-hosts file; otherwise it should use the user's normal SSH known-hosts configuration. The key and state files should only be created when needed. Do not store passwords or private-key passphrases in plaintext configuration.

### 7.2 SSH key management

- Default new key type: Ed25519 when supported by the local SSH implementation.
- Use Go SSH libraries or invoke the installed `ssh-keygen` executable safely; do not interpolate untrusted input into shell commands.
- Support an optional passphrase for the private key. If a passphrase is required at connection time, prompt securely or use a supported SSH agent; never persist the passphrase in plaintext.
- Use dedicated filenames under `~/.config/bare-eye/keys/`, not `~/.ssh/id_*`, to avoid accidental replacement of the user's personal keys.
- Set private-key permissions to owner read/write only (`0600`) and public-key permissions to owner read/write (`0600`) or owner-readable (`0644`) according to the implementation's policy. Key directory permissions must be `0700`.
- Provide a command or TUI action to display/copy the public key, show its fingerprint, and explain how to add it to `~/.ssh/authorized_keys` on the server.
- Provide an opt-in authorized-key installation workflow only if implemented safely. It must show the target and exact action before proceeding, require successful authentication, and never overwrite `authorized_keys`.
- Key rotation and deletion must be explicit. Deleting a local key must warn that it does not revoke copies already installed on remote servers.
- Allow each server to select a key explicitly, with a configurable global default. Also support SSH agent and existing user keys as alternatives.

### 7.3 Server inventory

Users can add, edit, test, enable/disable, reconnect, and remove server entries. Each entry supports:

- Stable ID and friendly display name.
- Hostname or IP address.
- SSH port (default `22`).
- SSH username.
- Authentication method: Bare Eye key, existing key path, SSH agent, or password entered interactively (if password auth is supported).
- Optional key path and key passphrase prompt policy.
- Optional connection timeout and per-host collection interval override.
- Optional SSH jump host/proxy configuration as a later enhancement, not required for MVP.
- Tags or groups for filtering, if time permits.

Server entries should accept DNS names and IP addresses. Input validation must reject empty host/user values and invalid ports, while allowing valid private IPv4/IPv6 addresses and hostnames.

Example YAML shape (illustrative, not a frozen schema):

```yaml
version: 1
ssh:
  default_key: "~/.config/bare-eye/keys/bare-eye-ed25519"
  known_hosts: "~/.ssh/known_hosts"
monitoring:
  interval: 2s
  connect_timeout: 5s
  command_timeout: 5s
servers:
  - id: prod-01
    name: Production VPS
    host: 203.0.113.10
    port: 22
    user: monitor
    auth:
      method: key
      key_path: "~/.config/bare-eye/keys/bare-eye-ed25519"
    enabled: true
```

The example IP is documentation-only and must not be treated as a real server. Configuration loading must support path expansion for `~`, report line/key context for invalid values, and preserve unknown future fields when feasible. The application must not rewrite the full config on every metrics refresh.

### 7.4 SSH connection and host verification

- Use standard SSH authentication and verify the remote server's host key.
- Never use an insecure accept-all host-key callback in normal operation.
- For an unknown host, present the host-key fingerprint and require explicit trust before saving it.
- If a known host key changes, block the connection and display a clear warning with the previous and newly observed fingerprints. Do not silently replace the stored key.
- Respect SSH agent and configured key files. Avoid automatically scanning arbitrary files for credentials.
- Use bounded connection and command timeouts, keepalive where appropriate, and controlled retries with backoff.
- Do not make interactive password prompts block the entire dashboard; credential prompts should be handled in a clear, serialized UI flow.
- Close sessions and cancel in-flight remote commands when a host is disabled, removed, or the application exits.

### 7.5 Metrics collection

MVP data points, where the target OS and permissions support them:

| Metric | Required display | Collection guidance |
|---|---|---|
| Reachability | Connected/disconnected and last successful update | SSH connection and command result |
| CPU | Utilization percentage and short trend | Prefer a robust OS-specific source; calculate deltas where necessary |
| Memory | Used/total and percentage | Parse `/proc/meminfo` on Linux, with documented fallback behavior |
| Swap | Used/total and percentage | Linux `/proc/meminfo` where available |
| Load average | 1, 5, and 15 minutes | Linux `/proc/loadavg` where available |
| Disk | Used/total and percentage per mounted filesystem | `df` with stable, locale-independent output or a safe equivalent |
| Uptime | Human-readable uptime | `/proc/uptime` or a documented fallback |
| Network | RX/TX rate and cumulative counters where available | Read interface counters; calculate rates from successive samples |
| OS/system info | OS/kernel, hostname, CPU core count if available | Read-only commands or `/proc` data |

Remote commands must be read-only, bounded in execution time, and designed to avoid locale-dependent parsing. Linux is the primary MVP target. Unsupported metrics on non-Linux systems must be marked unsupported rather than fabricated. Avoid assuming that every target has `bash`, `jq`, `bc`, or other non-base utilities installed.

### 7.6 Multi-server concurrency and resilience

- Collect metrics from configured servers concurrently with a bounded worker/concurrency limit.
- One server's slow connection or failed command must not delay updates from other servers.
- Track last successful sample time per metric/host.
- Mark data stale after a configurable threshold and visually distinguish it from fresh data.
- Retry transient failures with backoff and jitter; do not create tight reconnect loops.
- Prevent overlapping collection jobs for the same host unless explicitly designed for it.
- Support graceful shutdown and cancellation of all goroutines, tickers, SSH clients, and sessions.
- The default interval should balance responsiveness and low remote overhead (initial proposal: 2 seconds, configurable).

### 7.7 TUI dashboard

Use `gotui` widgets, grid/flex layouts, gauges, sparklines, plots, tables, lists, and input components as appropriate. UI design should prioritize high information density and readability over decorative effects.

**Overview screen:**

- Header with Bare Eye version, total configured hosts, healthy/unhealthy counts, and current refresh interval.
- Server table/list with name, address, status, CPU, memory, disk, load, and last update.
- Selected-host detail area with CPU/RAM/swap gauges and trends; disk and network summaries.
- Status/event list showing recent connection and collection errors.
- Clear legend for healthy, warning, critical, stale, and disconnected states.

**Host detail screen:**

- Identity: friendly name, hostname, SSH user and port, OS/kernel, uptime, last update.
- CPU, memory, swap, load, disk filesystems, and network RX/TX metrics.
- Per-host connection and collection status with a useful error message.

**Inventory/setup screen:**

- Add or edit server through an input form.
- Test SSH connectivity and authentication before saving or enabling monitoring.
- Select auth method/key file and view the public key/fingerprint.
- Confirm removal and show whether the server entry is merely removed locally.

**Keyboard interaction (proposed):**

- `q` or `Ctrl+C`: quit (confirm only if there are unsaved edits).
- `↑` / `↓`: navigate server list.
- `Enter`: open selected host details.
- `a`: add host.
- `e`: edit selected host.
- `t`: test SSH connection.
- `r`: reconnect selected host.
- `/`: filter/search host inventory.
- `Tab`: switch main panels.
- `?`: show keyboard help.

Exact key bindings may change during implementation, but must be consistent and displayed in help. Handle terminal resize, narrow terminal widths, missing color support, and graceful TUI cleanup after errors.

### 7.8 CLI commands

In addition to the interactive TUI, expose enough CLI commands to support scripting and setup. Proposed command surface:

```text
bare-eye                 Start the interactive dashboard
bare-eye init            Initialize config and optionally generate a key pair
bare-eye keygen          Generate a Bare Eye SSH key pair
bare-eye key show        Print the public key and fingerprint
bare-eye hosts list      List configured hosts and status/config summary
bare-eye hosts add       Add a host interactively or from flags
bare-eye hosts test ID   Test SSH connectivity for one host
bare-eye config path     Print the resolved config directory/file path
bare-eye version         Print version/build information
```

This is a proposed interface, not a requirement to implement every subcommand before the first usable release. Secret values must not be printed by diagnostics. If CLI flags accept credentials, they should not encourage passwords in command-line arguments because process listings and shell history may expose them.

## 8. Non-functional requirements

### Performance

- Launch to usable UI should take no more than 2 seconds on a typical developer machine, excluding first-run key generation or user interaction.
- UI input and navigation should remain responsive while SSH connections or metrics commands time out.
- A default configuration of 10 servers should render continuously without visible blocking on an ordinary development laptop.
- Bound goroutine count, connection count, output size, and command execution time.
- Avoid unbounded metric history; keep only enough samples for the current sparklines/trends in memory.

### Reliability

- Invalid host configurations must be isolated and reported without preventing the other valid hosts from loading.
- Network loss, authentication errors, remote command errors, and terminal resize must not crash the app.
- The application must restore terminal state on normal exit, Ctrl+C, and recoverable failures.
- Configuration writes should be atomic (temporary file then rename) and preserve permissions.

### Security and privacy

- No telemetry or remote data exfiltration by default.
- Keep private keys and configuration local; never log private key contents, passwords, passphrases, or raw secret-bearing environment values.
- Enforce owner-only permissions for private keys, config, and other secret-bearing files where supported.
- Verify host keys and flag host-key changes.
- Use the least-privileged SSH user feasible; document that recommended permissions depend on which metrics are readable.
- Monitoring actions must not change remote configuration or services.
- Sanitize and constrain all user-provided values used in remote commands. Prefer fixed remote command strings and arguments over shell interpolation.
- Remote command output must be size-limited and parsed defensively.
- Treat configuration as untrusted input: validate schema and reject invalid timeouts, ports, paths, and excessively large values.

### Compatibility

- Primary target: Linux local workstation and Linux remote servers.
- Target terminal: modern ANSI-compatible terminal; basic monochrome operation should remain usable.
- `gotui` currently documents Go 1.24+ for the library; choose a supported Go toolchain matching the dependency's actual module requirements at implementation time.
- macOS local client support is desirable if dependency/runtime behavior allows. Remote Windows/non-Linux monitoring is not MVP scope.

## 9. Proposed technical architecture

Keep the app divided into testable layers:

1. **CLI/bootstrap:** flags, version, initialization, command dispatch, signal handling.
2. **Config store:** schema, path resolution, validation, atomic persistence, migration/versioning.
3. **SSH identity manager:** key generation, fingerprint/public-key display, key path and SSH-agent selection.
4. **SSH transport:** host verification, auth, connection lifecycle, timeouts, context cancellation.
5. **Remote collector:** fixed read-only collection commands, platform detection, parsing and normalized metric model.
6. **Scheduler/worker pool:** bounded concurrent polling, retries/backoff, stale detection and per-host state.
7. **In-memory state/history:** latest samples and bounded rolling windows for charts.
8. **TUI presentation:** dashboard, detail panels, inventory/setup forms, status messages and key bindings using `gotui`.

The collector should return typed metric structures and errors, not update UI widgets directly. The UI reads state updates through channels or another concurrency-safe mechanism. Each server should have isolated connection/collection state, and UI rendering should occur on the appropriate TUI/event loop rather than concurrently mutating widgets unsafely.

## 10. Data model (conceptual)

**ServerConfig**
- `id`, `name`, `host`, `port`, `user`, `enabled`
- `auth.method`, `auth.key_path` or SSH-agent reference
- `connect_timeout`, `command_timeout`, `interval` overrides
- optional `tags`

**ServerRuntimeState** (memory only unless a future feature needs persistence)
- connection status and last error category/message
- last connection attempt and last successful sample timestamps
- OS/platform capability profile
- latest normalized metrics and bounded samples

**MetricSample**
- timestamp
- CPU utilization
- memory/swap used and totals
- load averages
- filesystems with used/total/percent
- network counters and rates
- uptime and system metadata
- per-field validity/freshness when some metrics fail independently

Do not persist metric history to disk in the MVP. If a lightweight cache is used for non-secret UI preferences, make it explicitly non-authoritative and safe to delete.

## 11. Acceptance criteria

A release candidate satisfies the MVP when all of the following are true:

1. Running Bare Eye for the first time initializes `~/.config/bare-eye/` with restrictive permissions and does not overwrite existing files.
2. A user can generate a dedicated key pair, see its public key/fingerprint, and configure a server to use it.
3. Bare Eye refuses an unknown host key until the user explicitly trusts the displayed fingerprint, and blocks unexpected host-key changes.
4. A user can configure at least 10 servers and view their status and latest metrics in one dashboard.
5. Healthy servers continue to refresh when one or more hosts are unreachable, slow, or incorrectly configured.
6. CPU, memory, swap, load, disk, uptime, and supported network metrics render with units, freshness, and clear unsupported/error states.
7. Connection and command timeouts return control to the UI within configured bounds.
8. Adding, editing, testing, disabling, and removing servers works without manually editing YAML.
9. Private keys and secret material are not printed in logs, status output, or error messages.
10. Exiting restores the terminal and closes connections; the app builds and has automated tests for config validation, metric parsing, and host state transitions.

## 12. Testing strategy

- Unit tests for config parsing, path expansion, validation, permissions where testable, key metadata handling, metric parsing, stale-state calculations, retry/backoff policies, and status transitions.
- SSH integration tests using a local test SSH server with cases for valid key auth, rejected auth, unknown host, changed host key, unreachable host, slow command, and disconnect.
- Concurrency tests to verify failures/timeouts on one host do not block other hosts and to detect races in scheduler/state updates.
- TUI smoke tests for key navigation, resize behavior, no-color mode, and clean shutdown where supported by the test environment.
- Manual acceptance test with at least 10 test hosts or simulated SSH endpoints.
- Run `go test ./...` and `go test -race ./...` in CI where supported.

## 13. MVP milestones

**M1 — Bootstrap and configuration**
- Project structure, CLI, config path/validation, init command, atomic config persistence.

**M2 — SSH identity and host inventory**
- Key generation, key selection, known-host verification, CRUD and connection test.

**M3 — Linux metrics collector**
- Typed metrics, bounded commands, platform detection, parser tests and per-host errors.

**M4 — Concurrent scheduler and status model**
- Polling worker pool, freshness/stale state, retries, cancellation and isolation.

**M5 — TUI overview and host details**
- Dashboard built with `gotui`, filters, status list, trends, and responsive layout.

**M6 — Hardening and release**
- Security review, tests/race checks, packaging, documentation, sample config, operational troubleshooting guide.

## 14. Risks and mitigations

- **Remote command output varies by distribution:** use `/proc` where feasible, fixed commands, explicit platform detection, tested parsing, and unsupported states.
- **SSH host-key friction:** make fingerprint verification clear and simple; never bypass it silently.
- **Permission limitations for some metrics:** document the least privilege needed and show unavailable metrics individually.
- **High latency or many targets:** use bounded concurrency and per-host timeouts, not a sequential polling loop.
- **TUI layout pressure on small terminals:** provide responsive layouts, alternate panels, and minimum terminal guidance.
- **Generated key is not automatically trusted by remote servers:** separate local key generation from remote authorization and explain both steps.

## 15. Open decisions

1. Should MVP support password authentication, or only key files and SSH agent?
2. Should Bare Eye maintain its own `known_hosts` file or default to `~/.ssh/known_hosts`?
3. Should the first release include a guided, opt-in public-key installation workflow, or stop at displaying/copying the public key?
4. Is Linux-only remote monitoring acceptable for the first release?
5. What warning/critical thresholds should ship by default, and should they be user-configurable in the MVP?

## 16. Reference and dependency notes

- TUI library: [`metaspartan/gotui`](https://github.com/metaspartan/gotui)
- Module import shown in the library documentation: `github.com/metaspartan/gotui/v5`
- The library provides rendering/widgets/layout and terminal interaction. Bare Eye is responsible for SSH clients, authentication, safe host-key verification, collection, scheduling, state management, and metrics semantics.

---

**Definition of done:** A user can launch Bare Eye, generate or select an SSH key, trust and connect multiple Linux VPS/bare-metal hosts, and monitor their latest health/resource metrics in a single responsive TUI without installing a permanent agent on each host.
