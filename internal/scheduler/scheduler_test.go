package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fiandev/bare-eye/internal/collector"
	"github.com/fiandev/bare-eye/internal/config"
	"github.com/fiandev/bare-eye/internal/state"
)

type fakeProber struct {
	mu      sync.Mutex
	calls   map[string]int
	fail    map[string]error
	delay   time.Duration
	cur     int
	maxConc int
	closed  bool
}

func newFakeProber() *fakeProber {
	return &fakeProber{calls: map[string]int{}, fail: map[string]error{}}
}

func (f *fakeProber) Probe(ctx context.Context, srv config.ServerConfig) (collector.Metrics, error) {
	f.mu.Lock()
	f.calls[srv.ID]++
	f.cur++
	if f.cur > f.maxConc {
		f.maxConc = f.cur
	}
	err := f.fail[srv.ID]
	delay := f.delay
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.cur--
		f.mu.Unlock()
	}()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return collector.Metrics{}, ctx.Err()
		}
	}
	if err != nil {
		return collector.Metrics{}, err
	}
	return collector.Metrics{
		CollectedAt: time.Now(), CPUValid: true, CPUPercent: 12.5,
		MemTotal: 100, MemUsed: 50,
	}, nil
}

func (f *fakeProber) Reconnect(string) {}

func (f *fakeProber) Close() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
}

func testConfig(servers ...config.ServerConfig) *config.Config {
	return &config.Config{
		Version: 1,
		SSH:     config.SSHConfig{DefaultKey: "/k", KnownHosts: "/kh"},
		Monitoring: config.MonitoringConfig{
			Interval:       config.Duration(40 * time.Millisecond),
			ConnectTimeout: config.Duration(time.Second),
			CommandTimeout: config.Duration(time.Second),
			StaleAfter:     config.Duration(5 * time.Second),
			MaxConcurrent:  2,
		},
		Servers: servers,
	}
}

func TestSchedulerIsolatesFailingHost(t *testing.T) {
	cfg := testConfig(
		config.ServerConfig{ID: "bad", Host: "bad", Port: 22, User: "u", Auth: config.AuthConfig{Method: config.AuthAgent}, Enabled: true},
		config.ServerConfig{ID: "good", Host: "good", Port: 22, User: "u", Auth: config.AuthConfig{Method: config.AuthAgent}, Enabled: true},
		config.ServerConfig{ID: "off", Host: "off", Port: 22, User: "u", Auth: config.AuthConfig{Method: config.AuthAgent}, Enabled: false},
	)
	store := state.New(5 * time.Second)
	events := state.NewEventLog(20)
	p := newFakeProber()
	p.fail["bad"] = errors.New("connection refused")
	s := New(cfg, store, events, p)
	s.Start()
	defer s.Stop()

	ok := WaitFor(context.Background(), 2*time.Second, func() bool {
		snap := store.Snapshot()
		return snap[1].Status == state.StatusOnline && snap[0].Status == state.StatusOffline
	})
	if !ok {
		t.Fatalf("states = %+v", store.Snapshot())
	}
	if got := store.Snapshot()[2].Status; got != state.StatusDisabled {
		t.Errorf("disabled host status = %s", got)
	}
	time.Sleep(120 * time.Millisecond)
	p.mu.Lock()
	goodCalls := p.calls["good"]
	p.mu.Unlock()
	if goodCalls < 2 {
		t.Errorf("good host polled %d times, want >= 2", goodCalls)
	}
	if len(events.Recent(10)) == 0 {
		t.Error("expected status events")
	}
}

func TestSchedulerBoundsConcurrency(t *testing.T) {
	var servers []config.ServerConfig
	for _, id := range []string{"a", "b", "c", "d"} {
		servers = append(servers, config.ServerConfig{
			ID: id, Host: id, Port: 22, User: "u",
			Auth: config.AuthConfig{Method: config.AuthAgent}, Enabled: true,
		})
	}
	cfg := testConfig(servers...)
	cfg.Monitoring.MaxConcurrent = 1
	store := state.New(5 * time.Second)
	p := newFakeProber()
	p.delay = 30 * time.Millisecond
	s := New(cfg, store, state.NewEventLog(20), p)
	s.Start()
	defer s.Stop()

	WaitFor(context.Background(), 2*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.calls["a"] > 0 && p.calls["b"] > 0
	})
	p.mu.Lock()
	max := p.maxConc
	p.mu.Unlock()
	if max > 1 {
		t.Errorf("max concurrent probes = %d, want <= 1", max)
	}
}

func TestRestartAndStop(t *testing.T) {
	cfg := testConfig(config.ServerConfig{
		ID: "a", Host: "a", Port: 22, User: "u",
		Auth: config.AuthConfig{Method: config.AuthAgent}, Enabled: true,
	})
	store := state.New(5 * time.Second)
	p := newFakeProber()
	s := New(cfg, store, state.NewEventLog(10), p)
	s.Start()
	WaitFor(context.Background(), time.Second, func() bool {
		h, _ := store.Get("a")
		return h.Status == state.StatusOnline
	})
	s.Restart(cfg)
	WaitFor(context.Background(), time.Second, func() bool {
		h, _ := store.Get("a")
		return h.Status == state.StatusOnline
	})
	s.Stop()
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if !closed {
		t.Error("Stop should close the prober")
	}
}
