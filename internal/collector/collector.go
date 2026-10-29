package collector

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// command is a fixed, read-only POSIX shell script. It uses only /proc and
// base utilities and never interpolates user input.
const command = `echo '###BAREEYE:uname'; uname -sr 2>/dev/null || true
echo '###BAREEYE:hostname'; hostname 2>/dev/null || true
echo '###BAREEYE:cores'; grep -c '^processor' /proc/cpuinfo 2>/dev/null || true
echo '###BAREEYE:uptime'; cat /proc/uptime 2>/dev/null || true
echo '###BAREEYE:loadavg'; cat /proc/loadavg 2>/dev/null || true
echo '###BAREEYE:meminfo'; cat /proc/meminfo 2>/dev/null || true
echo '###BAREEYE:stat'; cat /proc/stat 2>/dev/null || true
echo '###BAREEYE:netdev'; cat /proc/net/dev 2>/dev/null || true
echo '###BAREEYE:df'; df -kP 2>/dev/null || true
echo '###BAREEYE:end'`

// Collect runs the fixed collection script and parses the result.
func (c *Collector) Collect(ctx context.Context, r Runner, timeout time.Duration) (Metrics, error) {
	out, err := r.Run(ctx, command, timeout)
	if err != nil {
		return Metrics{}, err
	}
	now := c.now()
	sections := splitSections(string(out))
	if len(sections) == 0 {
		return Metrics{}, ErrUnsupported
	}
	if kernel := firstLine(sections["uname"]); kernel != "" && !strings.HasPrefix(strings.ToLower(kernel), "linux") {
		return Metrics{}, fmt.Errorf("%w: %s", ErrUnsupported, kernel)
	}

	m := Metrics{CollectedAt: now}
	m.Kernel = firstLine(sections["uname"])
	m.Hostname = firstLine(sections["hostname"])

	if n, ok := parseCores(sections["cores"]); ok {
		m.CPUCores = n
	}
	if up, ok := parseUptime(sections["uptime"]); ok {
		m.Uptime = up
	}
	if l1, l5, l15, ok := parseLoadAvg(sections["loadavg"]); ok {
		m.Load1, m.Load5, m.Load15, m.LoadValid = l1, l5, l15, true
	} else {
		m.Warnings = append(m.Warnings, "load")
	}
	if mem, ok := parseMeminfo(sections["meminfo"]); ok {
		m.MemTotal, m.MemUsed = mem.total, mem.used
		m.SwapTotal, m.SwapUsed = mem.swapTotal, mem.swapUsed
	} else {
		m.Warnings = append(m.Warnings, "memory")
	}
	if cpu, ok := parseProcStat(sections["stat"]); ok {
		if c.haveCPU {
			m.CPUPercent, m.CPUValid = cpu.usageSince(c.prevCPU)
		}
		c.prevCPU, c.haveCPU = cpu, true
	}
	if net, ok := parseNetDev(sections["netdev"]); ok {
		m.NetRXBytes, m.NetTXBytes = net.rx, net.tx
		if c.haveNet {
			elapsed := now.Sub(c.prevAt).Seconds()
			if elapsed > 0 {
				if net.rx >= c.prevNet.rx {
					m.NetRXRate = float64(net.rx-c.prevNet.rx) / elapsed
				}
				if net.tx >= c.prevNet.tx {
					m.NetTXRate = float64(net.tx-c.prevNet.tx) / elapsed
				}
			}
		}
		c.prevNet, c.haveNet = net, true
	}
	if fs, ok := parseDF(sections["df"]); ok {
		m.Filesystems = fs
	} else {
		m.Warnings = append(m.Warnings, "disk")
	}
	c.prevAt = now
	return m, nil
}

func splitSections(out string) map[string]string {
	sections := map[string]string{}
	var current string
	var b strings.Builder
	flush := func() {
		if current != "" {
			sections[current] = b.String()
			b.Reset()
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "###BAREEYE:") {
			flush()
			current = strings.TrimSpace(strings.TrimPrefix(line, "###BAREEYE:"))
			continue
		}
		if current != "" {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	flush()
	return sections
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if v := strings.TrimSpace(line); v != "" {
			return v
		}
	}
	return ""
}
