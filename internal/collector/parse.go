package collector

import (
	"strconv"
	"strings"
	"time"
)

type cpuTimes struct {
	total uint64
	idle  uint64
}

func (c cpuTimes) usageSince(prev cpuTimes) (float64, bool) {
	if c.total <= prev.total || c.idle < prev.idle {
		return 0, false
	}
	totalDelta := c.total - prev.total
	idleDelta := c.idle - prev.idle
	if idleDelta > totalDelta {
		return 0, false
	}
	pct := float64(totalDelta-idleDelta) / float64(totalDelta) * 100
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct, true
}

func parseProcStat(s string) (cpuTimes, bool) {
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		var vals []uint64
		for _, f := range fields[1:] {
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				break
			}
			vals = append(vals, v)
		}
		if len(vals) < 4 {
			return cpuTimes{}, false
		}
		var total uint64
		for i, v := range vals {
			if i >= 8 {
				break
			}
			total += v
		}
		idle := vals[3]
		if len(vals) > 4 {
			idle += vals[4] // iowait counts as idle
		}
		return cpuTimes{total: total, idle: idle}, true
	}
	return cpuTimes{}, false
}

type memInfo struct {
	total, used         uint64
	swapTotal, swapUsed uint64
}

func parseMeminfo(s string) (memInfo, bool) {
	vals := map[string]uint64{}
	for _, line := range strings.Split(s, "\n") {
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		vals[strings.TrimSpace(key)] = v * 1024 // kB
	}
	total, ok := vals["MemTotal"]
	if !ok || total == 0 {
		return memInfo{}, false
	}
	avail, ok := vals["MemAvailable"]
	if !ok {
		avail = vals["MemFree"] + vals["Buffers"] + vals["Cached"] + vals["SReclaimable"]
	}
	if avail > total {
		avail = total
	}
	m := memInfo{total: total, used: total - avail}
	m.swapTotal = vals["SwapTotal"]
	if m.swapTotal >= vals["SwapFree"] {
		m.swapUsed = m.swapTotal - vals["SwapFree"]
	}
	return m, true
}

func parseLoadAvg(s string) (float64, float64, float64, bool) {
	fields := strings.Fields(firstLine(s))
	if len(fields) < 3 {
		return 0, 0, 0, false
	}
	l1, err1 := strconv.ParseFloat(fields[0], 64)
	l5, err2 := strconv.ParseFloat(fields[1], 64)
	l15, err3 := strconv.ParseFloat(fields[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, false
	}
	return l1, l5, l15, true
}

func parseUptime(s string) (time.Duration, bool) {
	fields := strings.Fields(firstLine(s))
	if len(fields) == 0 {
		return 0, false
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || secs < 0 {
		return 0, false
	}
	return time.Duration(secs * float64(time.Second)), true
}

func parseCores(s string) (int, bool) {
	n, err := strconv.Atoi(firstLine(s))
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

type netTimes struct {
	rx, tx uint64
}

func parseNetDev(s string) (netTimes, bool) {
	var out netTimes
	found := false
	for _, line := range strings.Split(s, "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		iface := strings.TrimSpace(name)
		if iface == "" || iface == "Inter-|" || iface == "face" || iface == "lo" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			continue
		}
		rx, err1 := strconv.ParseUint(fields[0], 10, 64)
		tx, err2 := strconv.ParseUint(fields[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out.rx += rx
		out.tx += tx
		found = true
	}
	return out, found
}

func parseDF(s string) ([]Filesystem, bool) {
	var out []Filesystem
	seen := map[string]bool{}
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 || strings.EqualFold(fields[0], "Filesystem") {
			continue
		}
		device := fields[0]
		totalKB, err1 := strconv.ParseUint(fields[1], 10, 64)
		usedKB, err2 := strconv.ParseUint(fields[2], 10, 64)
		if err1 != nil || err2 != nil || totalKB == 0 {
			continue
		}
		mount := strings.Join(fields[5:], " ")
		if isPseudoFS(device, mount) || seen[mount] {
			continue
		}
		seen[mount] = true
		pct := 0.0
		if totalKB > 0 {
			pct = float64(usedKB) / float64(totalKB) * 100
		}
		out = append(out, Filesystem{
			Device:  device,
			Mount:   mount,
			Total:   totalKB * 1024,
			Used:    usedKB * 1024,
			Percent: pct,
		})
	}
	return out, len(out) > 0
}

func isPseudoFS(device, mount string) bool {
	if mount == "/" {
		return false
	}
	if strings.HasPrefix(device, "/dev/") || strings.Contains(device, ":") {
		return false
	}
	switch device {
	case "tmpfs", "devtmpfs", "udev", "none", "shm", "squashfs", "ramfs", "efivarfs",
		"cgroup", "cgroup2", "proc", "sysfs", "devpts", "mqueue", "hugetlbfs",
		"debugfs", "tracefs", "securityfs", "pstore", "bpf", "configfs",
		"fusectl", "autofs", "binfmt_misc", "nsfs":
		return true
	}
	for _, p := range []string{"/proc", "/sys", "/dev", "/run"} {
		if mount == p || strings.HasPrefix(mount, p+"/") {
			return true
		}
	}
	return false
}
