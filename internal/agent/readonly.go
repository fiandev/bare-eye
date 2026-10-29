package agent

import "strings"

// ReadOnly reports whether a shell command is positively recognised as
// non-mutating. It is deliberately conservative: anything containing shell
// chaining, redirection, substitution or an unknown command requires explicit
// operator approval. A false result does not mean the command is dangerous, it
// only means the agent may not run it without confirmation.
func ReadOnly(command string) bool {
	c := strings.TrimSpace(command)
	if c == "" {
		return false
	}
	// Reject anything that can chain, redirect, substitute, escape or glob into
	// a write. Commands in AutoReadOnly must be a single simple invocation.
	if strings.ContainsAny(c, "\n\r;&|<>`$\\") {
		return false
	}
	if sensitive(c) {
		return false
	}
	fields := strings.Fields(c)
	if len(fields) == 0 {
		return false
	}
	head := fields[0]
	if strings.Contains(head, "/") || strings.Contains(head, "=") {
		return false
	}
	args := fields[1:]
	switch head {
	case "ls", "cat", "head", "tail", "grep", "egrep", "fgrep",
		"df", "du", "free", "uptime", "uname", "id", "whoami", "ps", "ss",
		"stat", "wc", "cut", "tr", "which", "readlink", "realpath", "pwd",
		"nproc", "lscpu", "lsblk", "findmnt", "sha256sum", "sha1sum",
		"md5sum", "arch", "getconf", "basename", "dirname":
		return safeFlagged(head, args)
	case "hostname":
		// `hostname` and `hostname -f` read; `hostname NAME` changes it.
		return !hasPositional(args)
	case "uniq":
		return countPositional(args) <= 1 && safeFlagged(head, args)
	case "sort":
		return safeFlagged(head, args)
	case "ip":
		return ipReadOnly(args)
	case "systemctl":
		return systemctlReadOnly(args)
	case "journalctl":
		return journalReadOnly(args)
	case "docker", "podman":
		return containerReadOnly(args)
	case "apt":
		return firstPositional(args) == "list" || firstPositional(args) == "show" ||
			firstPositional(args) == "search" || firstPositional(args) == "policy"
	case "apt-cache":
		switch firstPositional(args) {
		case "policy", "show", "search", "depends", "rdepends":
			return true
		}
		return false
	case "dpkg":
		return hasAnyFlag(args, "-l", "--list", "-s", "--status", "-L", "--listfiles", "-p", "--print-avail")
	case "rpm":
		return hasAnyFlag(args, "-q", "-qa", "-qi", "-ql", "-qR", "--query")
	case "nginx":
		return hasAnyFlag(args, "-t") && !hasAnyFlag(args, "-T")
	case "apachectl", "httpd":
		return hasAnyFlag(args, "-t") && !hasAnyFlag(args, "-S")
	}
	return false
}

func safeFlagged(head string, args []string) bool {
	for _, a := range args {
		switch head {
		case "tail":
			if a == "-f" || a == "-F" || a == "--follow" ||
				strings.HasPrefix(a, "--follow=") || strings.HasPrefix(a, "-f") {
				return false
			}
		case "sort":
			if a == "-o" || strings.HasPrefix(a, "-o") || strings.HasPrefix(a, "--output") {
				return false
			}
		}
	}
	return true
}

func ipReadOnly(args []string) bool {
	sub := positional(args)
	if len(sub) == 0 {
		return false
	}
	switch sub[0] {
	case "a", "addr", "address", "r", "route", "n", "neigh", "neighbor":
		return true
	case "link":
		if len(sub) == 1 {
			return true
		}
		return sub[1] == "show" || sub[1] == "list" || sub[1] == "ls"
	case "s", "stats":
		return true
	}
	return false
}

func systemctlReadOnly(args []string) bool {
	switch firstPositional(args) {
	case "status", "show", "cat", "help", "list-units", "list-unit-files",
		"list-sockets", "list-timers", "list-jobs", "is-active", "is-enabled",
		"is-failed", "is-system-running", "show-environment":
		return true
	}
	return false
}

func journalReadOnly(args []string) bool {
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--vacuum"),
			a == "--rotate", a == "--flush", a == "--sync",
			a == "--setup-keys", a == "--relinquish-var",
			a == "--smart-relinquish-var", a == "-f", a == "--follow":
			return false
		}
	}
	return true
}

func containerReadOnly(args []string) bool {
	sub := positional(args)
	if len(sub) == 0 {
		return false
	}
	switch sub[0] {
	case "ps", "images", "inspect", "logs", "version", "info", "stats", "top", "port", "diff":
		return true
	case "system":
		return len(sub) > 1 && (sub[1] == "df" || sub[1] == "info")
	case "compose":
		return len(sub) > 1 && (sub[1] == "ps" || sub[1] == "logs" || sub[1] == "config" || sub[1] == "top")
	}
	return false
}

func positional(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		out = append(out, a)
	}
	return out
}

func firstPositional(args []string) string {
	p := positional(args)
	if len(p) == 0 {
		return ""
	}
	return p[0]
}

func countPositional(args []string) int { return len(positional(args)) }

func hasPositional(args []string) bool { return countPositional(args) > 0 }

func hasAnyFlag(args []string, flags ...string) bool {
	for _, a := range args {
		for _, f := range flags {
			if a == f {
				return true
			}
		}
	}
	return false
}

// sensitive blocks commands that can expose credentials or private keys even
// when the command itself is read-only.
func sensitive(command string) bool {
	l := strings.ToLower(command)
	for _, p := range []string{
		"/etc/shadow", "/etc/gshadow", "id_rsa", "id_ed25519", "id_ecdsa",
		"id_dsa", ".ssh", "authorized_keys", "known_hosts", "secrets.enc",
		"secrets.key", ".env", "credential", "password", "passwd", "token",
		"api_key", "apikey", "/proc/", "private_key", "privatekey",
		"bash_history", "zsh_history", ".netrc", ".npmrc", ".pgpass",
		".docker/config.json",
	} {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}
