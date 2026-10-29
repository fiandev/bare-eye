// Package collector collects read-only Linux metrics over SSH.
package collector

import (
	"context"
	"errors"
	"time"
)

// ErrUnsupported means the remote system is not a supported Linux host.
var ErrUnsupported = errors.New("remote system is not supported (Linux only)")

// Filesystem is one mounted filesystem's usage.
type Filesystem struct {
	Device  string
	Mount   string
	Total   uint64
	Used    uint64
	Percent float64
}

// Metrics is a normalized snapshot from one host.
type Metrics struct {
	CollectedAt time.Time

	CPUPercent float64
	CPUValid   bool

	MemTotal  uint64
	MemUsed   uint64
	SwapTotal uint64
	SwapUsed  uint64

	Load1, Load5, Load15 float64
	LoadValid            bool

	Filesystems []Filesystem

	NetRXBytes uint64
	NetTXBytes uint64
	NetRXRate  float64
	NetTXRate  float64

	Uptime   time.Duration
	Kernel   string
	Hostname string
	CPUCores int

	// Warnings lists metrics that were unavailable in this sample.
	Warnings []string
}

// MemPercent returns memory utilization, or 0 when unknown.
func (m Metrics) MemPercent() float64 {
	if m.MemTotal == 0 {
		return 0
	}
	return float64(m.MemUsed) / float64(m.MemTotal) * 100
}

// SwapPercent returns swap utilization, or 0 when no swap is configured.
func (m Metrics) SwapPercent() float64 {
	if m.SwapTotal == 0 {
		return 0
	}
	return float64(m.SwapUsed) / float64(m.SwapTotal) * 100
}

// Runner executes a fixed remote command.
type Runner interface {
	Run(ctx context.Context, command string, timeout time.Duration) ([]byte, error)
}

// Collector holds the previous counters needed to derive CPU and network rates.
// One Collector must be used per host, and calls must not overlap.
type Collector struct {
	prevCPU cpuTimes
	haveCPU bool
	prevNet netTimes
	haveNet bool
	prevAt  time.Time

	// Now is injectable for tests; nil means time.Now.
	Now func() time.Time
}

// New returns a ready Collector.
func New() *Collector { return &Collector{} }

func (c *Collector) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Reset forgets previous counters, e.g. after a reconnect.
func (c *Collector) Reset() {
	c.haveCPU = false
	c.haveNet = false
	c.prevAt = time.Time{}
}
