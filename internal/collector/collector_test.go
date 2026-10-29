package collector

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type fakeRunner struct {
	out string
	err error
}

func (f fakeRunner) Run(context.Context, string, time.Duration) ([]byte, error) {
	return []byte(f.out), f.err
}

func fixture(cpuLine string, rx, tx uint64) string {
	return fmt.Sprintf(`###BAREEYE:uname
Linux 6.8.0-45-generic
###BAREEYE:hostname
web-01
###BAREEYE:cores
8
###BAREEYE:uptime
90061.50 12345.00
###BAREEYE:loadavg
1.50 1.00 0.50 2/300 12345
###BAREEYE:meminfo
MemTotal:        8000000 kB
MemFree:         1000000 kB
MemAvailable:    4000000 kB
Buffers:          100000 kB
Cached:          2000000 kB
SwapTotal:       2000000 kB
SwapFree:        1500000 kB
###BAREEYE:stat
%s
cpu0 50 0 50 400 0 0 0 0 0 0
###BAREEYE:netdev
Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 999999 0 0 0 0 0 0 0 999999 0 0 0 0 0 0 0
  eth0: %d 10 0 0 0 0 0 0 %d 20 0 0 0 0 0 0
###BAREEYE:df
Filesystem     1024-blocks    Used Available Capacity Mounted on
/dev/sda1        1000000  250000    750000      25%% /
tmpfs               1000       1       999       1%% /run
###BAREEYE:end
`, cpuLine, rx, tx)
}

func TestCollectDerivesRatesAndUsage(t *testing.T) {
	c := New()
	now := time.Unix(1700000000, 0)
	c.Now = func() time.Time { return now }

	m1, err := c.Collect(context.Background(), fakeRunner{out: fixture("cpu  100 0 100 800 0 0 0 0 0 0", 1000, 2000)}, time.Second)
	if err != nil {
		t.Fatalf("Collect 1: %v", err)
	}
	if m1.CPUValid {
		t.Error("first sample should not claim CPU usage")
	}
	if m1.MemTotal != 8000000*1024 || m1.MemUsed != 4000000*1024 {
		t.Errorf("memory = %d/%d", m1.MemUsed, m1.MemTotal)
	}
	if m1.SwapTotal != 2000000*1024 || m1.SwapUsed != 500000*1024 {
		t.Errorf("swap = %d/%d", m1.SwapUsed, m1.SwapTotal)
	}
	if m1.Load1 != 1.5 || !m1.LoadValid {
		t.Errorf("load = %v", m1.Load1)
	}
	if m1.Uptime != 90061*time.Second+500*time.Millisecond {
		t.Errorf("uptime = %v", m1.Uptime)
	}
	if m1.CPUCores != 8 || m1.Hostname != "web-01" {
		t.Errorf("cores/hostname = %d/%q", m1.CPUCores, m1.Hostname)
	}
	if len(m1.Filesystems) != 1 {
		t.Fatalf("filesystems = %+v, want only /", m1.Filesystems)
	}
	if m1.Filesystems[0].Mount != "/" || m1.Filesystems[0].Total != 1000000*1024 {
		t.Errorf("filesystem = %+v", m1.Filesystems[0])
	}
	if m1.NetRXBytes != 1000 || m1.NetTXBytes != 2000 {
		t.Errorf("counters = %d/%d", m1.NetRXBytes, m1.NetTXBytes)
	}

	now = now.Add(2 * time.Second)
	m2, err := c.Collect(context.Background(), fakeRunner{out: fixture("cpu  200 0 200 1600 0 0 0 0 0 0", 3000, 4000)}, time.Second)
	if err != nil {
		t.Fatalf("Collect 2: %v", err)
	}
	if !m2.CPUValid {
		t.Fatal("second sample should compute CPU")
	}
	if m2.CPUPercent < 19.9 || m2.CPUPercent > 20.1 {
		t.Errorf("cpu = %v, want ~20", m2.CPUPercent)
	}
	if m2.NetRXRate != 1000 || m2.NetTXRate != 1000 {
		t.Errorf("rates = %v/%v, want 1000/1000", m2.NetRXRate, m2.NetTXRate)
	}
}

func TestCollectRejectsNonLinux(t *testing.T) {
	out := "###BAREEYE:uname\nFreeBSD 14.0-RELEASE\n###BAREEYE:end\n"
	_, err := New().Collect(context.Background(), fakeRunner{out: out}, time.Second)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestCollectToleratesMissingSections(t *testing.T) {
	out := "###BAREEYE:uname\nLinux 6.1\n###BAREEYE:stat\ncpu  1 1 1 1\n###BAREEYE:end\n"
	m, err := New().Collect(context.Background(), fakeRunner{out: out}, time.Second)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if m.MemTotal != 0 || m.LoadValid {
		t.Errorf("unexpected data: %+v", m)
	}
	if len(m.Warnings) == 0 {
		t.Error("expected warnings for unavailable metrics")
	}
}

func TestParseProcStatIgnoresAggregateMismatch(t *testing.T) {
	c := New()
	now := time.Unix(1700000000, 0)
	c.Now = func() time.Time { return now }
	if _, err := c.Collect(context.Background(), fakeRunner{out: fixture("cpu  100 0 100 800", 0, 0)}, time.Second); err != nil {
		t.Fatal(err)
	}
	// Counters reset (reboot): usage must be marked invalid, not negative.
	now = now.Add(time.Second)
	m, err := c.Collect(context.Background(), fakeRunner{out: fixture("cpu  1 0 1 8", 0, 0)}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if m.CPUValid {
		t.Error("counter reset should not produce a CPU percentage")
	}
}
