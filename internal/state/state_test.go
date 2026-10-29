package state

import (
	"errors"
	"testing"
	"time"

	"github.com/fiandev/bare-eye/internal/collector"
	"github.com/fiandev/bare-eye/internal/config"
	"github.com/fiandev/bare-eye/internal/sshx"
)

func TestStalenessAndFailurePreservesMetrics(t *testing.T) {
	s := New(5 * time.Second)
	s.SetServers([]config.ServerConfig{
		{ID: "a", Host: "h", Enabled: true},
		{ID: "b", Host: "h2", Enabled: false},
	})
	snap := s.Snapshot()
	if len(snap) != 2 || snap[1].Status != StatusDisabled {
		t.Fatalf("initial snapshot = %+v", snap)
	}

	m := collector.Metrics{CollectedAt: time.Now(), CPUPercent: 42, CPUValid: true, MemTotal: 100, MemUsed: 50}
	s.RecordSuccess("a", m)
	snap = s.Snapshot()
	if snap[0].Status != StatusOnline || !snap[0].HasMetrics || len(snap[0].History) != 1 {
		t.Fatalf("after success = %+v", snap[0])
	}

	s.Update("a", func(h *HostState) { h.LastSuccess = time.Now().Add(-10 * time.Second) })
	if got := s.Snapshot()[0].Status; got != StatusStale {
		t.Errorf("status = %s, want stale", got)
	}

	s.RecordFailure("a", errors.New("dial tcp: refused"))
	h, _ := s.Get("a")
	if h.Status != StatusOffline || h.Error == "" {
		t.Errorf("failure state = %+v", h)
	}
	if !h.HasMetrics || h.Metrics.CPUPercent != 42 {
		t.Error("last good metrics should be preserved on failure")
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		err  error
		want Status
	}{
		{&sshx.UnknownHostKeyError{}, StatusHostKey},
		{&sshx.ChangedHostKeyError{}, StatusHostKey},
		{&sshx.AuthError{}, StatusAuthError},
		{&sshx.PassphraseRequiredError{}, StatusAuthError},
		{sshx.ErrPasswordRequired, StatusAuthError},
		{collector.ErrUnsupported, StatusUnsupported},
		{errors.New("network down"), StatusOffline},
	}
	for _, c := range cases {
		if got := Classify(c.err); got != c.want {
			t.Errorf("Classify(%T) = %s, want %s", c.err, got, c.want)
		}
	}
}

func TestEventLogBounded(t *testing.T) {
	l := NewEventLog(3)
	for i := 0; i < 5; i++ {
		l.Add(LevelInfo, "h", "event")
	}
	if got := len(l.Recent(10)); got != 3 {
		t.Errorf("len = %d, want 3", got)
	}
	if got := len(l.Recent(0)); got != 3 {
		t.Errorf("len(0) = %d, want 3", got)
	}
}

func TestHistoryBounded(t *testing.T) {
	s := New(time.Minute)
	s.SetServers([]config.ServerConfig{{ID: "a", Host: "h", Enabled: true}})
	for i := 0; i < 200; i++ {
		s.RecordSuccess("a", collector.Metrics{CollectedAt: time.Now()})
	}
	h, _ := s.Get("a")
	if len(h.History) > 120 {
		t.Errorf("history = %d, want <= 120", len(h.History))
	}
}
