# Troubleshooting

## "bare-eye is not initialized"

Run:

```sh
bare-eye init
```

Or point Bare Eye at another directory:

```sh
BARE_EYE_CONFIG_DIR=/path/to/config bare-eye
```

## Permission denied (publickey)

1. Print the key Bare Eye will use:

   ```sh
   bare-eye key show
   ```

2. Confirm the server has that exact line in `~/.ssh/authorized_keys`:

   ```sh
   ssh monitor@host 'grep -F "ssh-ed25519 AAAA..." ~/.ssh/authorized_keys'
   ```

3. Check the server-side permissions: `~/.ssh` must be `700`, `authorized_keys`
   `600`, and the home directory must not be group/world writable.

4. Try the guided installer (works with password auth or an already-working
   key):

   ```sh
   bare-eye key install <host-id>
   ```

5. Verify what the server actually sees:

   ```sh
   ssh -vvv -i ~/.config/bare-eye/keys/bare-eye-ed25519 monitor@host true
   ```

## "passphrase required"

Background monitoring cannot prompt. Either:

- use an unencrypted key for monitoring,
- use `auth.method: agent` with the key loaded in `ssh-agent`, or
- run `bare-eye hosts test <id>` interactively and use a password/agent setup.

## "unknown host key"

Bare Eye has never seen this host. The dialog shows a SHA256 fingerprint.
Compare it with the server operator's out-of-band value, then answer `y` or run:

```sh
bare-eye hosts test <id> --yes
```

The key is appended to `~/.config/bare-eye/known_hosts` (never to
`~/.ssh/known_hosts`).

## "HOST KEY CHANGED"

The server presented a different key than the one stored. Bare Eye blocks the
connection and does **not** replace the stored key. This can be a legitimate
server rebuild or a man-in-the-middle attack.

1. Verify the server out of band (provider console, another network path).
2. Compare both fingerprints shown in the warning.
3. Only after confirming, remove the stale line from
   `~/.config/bare-eye/known_hosts` and reconnect to verify the new key.

Never edit `known_hosts` just to silence the warning.

## Host shows "unsupported"

Bare Eye only supports Linux remotes. The collector checks `uname -s`; other
systems are reported as unsupported instead of fabricating metrics.

## Metrics are missing

- Memory, swap, load, CPU and uptime come from `/proc`; containers or hardened
  kernels may hide some files.
- Disk data comes from `df -kP`. Pseudo filesystems (`tmpfs`, `devtmpfs`,
  `overlay` outside `/`, etc.) are filtered out.
- The `Unavailable` line in the host detail view lists metrics missing from the
  latest sample.
- CPU needs two samples: the first poll shows `—`, the next shows a percentage.
- Network rates need two samples too; cumulative counters are shown immediately.

## Host is "offline" but the server is up

Check, in order:

1. `bare-eye hosts test <id>` for the exact error.
2. TCP reachability: `nc -vz host 22`.
3. The SSH port in the host entry (`hosts list`).
4. Firewall / fail2ban bans after repeated failed auth.
5. `command_timeout`: a loaded host may need a larger value.
6. `MaxSessions`/`MaxStartups` on the server when polling many hosts; lower
   `monitoring.max_concurrent`.

## Data looks frozen / "STALE"

`stale_after` defaults to `10s`. If the last successful sample is older, the
host is marked `STALE` and values are labelled as the last sample. Press `r` to
reconnect, or check the event log for the underlying error.

## Terminal is garbled after a crash

Bare Eye restores the terminal on normal exit, `Ctrl+C`, and recoverable
errors. If a terminal is still broken after a hard kill, run:

```sh
reset
```

## Debugging configuration

```sh
bare-eye config path        # resolved paths
bare-eye hosts list         # inventory summary
bare-eye hosts list --json  # machine-readable
```

Configuration is validated on load; errors include the YAML line/key context
where possible. Back up `config.yaml` before hand-editing. Bare Eye preserves
unknown fields, but manual YAML errors will block startup.

## Password storage

`bare-eye config set password <id>` writes `secrets.enc` encrypted with
AES-GCM; the key is `secrets.key` (both `0600`). This protects against casual
disclosure, not a full compromise of the config directory. Prefer SSH keys or
an agent for higher assurance. Removing a host deletes its stored password.
