package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/fiandev/bare-eye/internal/state"
)

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit && exp < 5; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func humanRate(bps float64) string {
	if bps <= 0 {
		return "0 B/s"
	}
	return humanBytes(uint64(bps)) + "/s"
}

func humanUptime(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Second:
		return "now"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

func pct(v float64) string {
	if v <= 0 {
		return "0%"
	}
	return fmt.Sprintf("%.0f%%", v)
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 1 {
		return string(r[:w])
	}
	return string(r[:w-1]) + "…"
}

func statusLabel(s state.Status) string {
	switch s {
	case state.StatusOnline:
		return "online"
	case state.StatusStale:
		return "STALE"
	case state.StatusOffline:
		return "offline"
	case state.StatusAuthError:
		return "auth"
	case state.StatusHostKey:
		return "host-key"
	case state.StatusUnsupported:
		return "unsupported"
	case state.StatusDisabled:
		return "disabled"
	case state.StatusPending:
		return "pending"
	}
	return strings.ToLower(string(s))
}
