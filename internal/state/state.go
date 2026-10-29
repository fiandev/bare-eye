// Package state holds per-host runtime state, bounded history, and events.
package state

import (
	"errors"
	"sync"
	"time"

	"github.com/fiandev/bare-eye/internal/collector"
	"github.com/fiandev/bare-eye/internal/config"
	"github.com/fiandev/bare-eye/internal/sshx"
)

// Status is the honest connection/collection state of a host.
type Status string

const (
	StatusDisabled    Status = "disabled"
	StatusPending     Status = "pending"
	StatusOnline      Status = "online"
	StatusStale       Status = "stale"
	StatusOffline     Status = "offline"
	StatusAuthError   Status = "auth-error"
	StatusHostKey     Status = "host-key"
	StatusUnsupported Status = "unsupported"
)

// Sample is one point in a host's bounded in-memory history.
type Sample struct {
	At       time.Time
	CPU      float64
	CPUValid bool
	Mem      float64
	Swap     float64
	Load1    float64
	NetRX    float64
	NetTX    float64
}

// HostState is the runtime view of one configured server.
type HostState struct {
	ID          string
	Status      Status
	Error       string
	LastAttempt time.Time
	LastSuccess time.Time
	Connected   bool
	Metrics     collector.Metrics
	HasMetrics  bool
	History     []Sample
}

// Store is a concurrency-safe collection of host states.
type Store struct {
	mu         sync.RWMutex
	hosts      map[string]*HostState
	order      []string
	staleAfter time.Duration
	maxHistory int
}

// New creates a store that marks data stale after staleAfter.
func New(staleAfter time.Duration) *Store {
	if staleAfter <= 0 {
		staleAfter = 10 * time.Second
	}
	return &Store{
		hosts:      map[string]*HostState{},
		staleAfter: staleAfter,
		maxHistory: 120,
	}
}

// SetServers reconciles the store with the configured inventory.
func (s *Store) SetServers(servers []config.ServerConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]*HostState, len(servers))
	order := make([]string, 0, len(servers))
	for _, srv := range servers {
		order = append(order, srv.ID)
		if existing, ok := s.hosts[srv.ID]; ok {
			next[srv.ID] = existing
			continue
		}
		st := &HostState{ID: srv.ID, Status: StatusPending}
		if !srv.IsEnabled() {
			st.Status = StatusDisabled
		}
		next[srv.ID] = st
	}
	s.hosts = next
	s.order = order
}

// Update mutates one host state under lock. It returns false if unknown.
func (s *Store) Update(id string, fn func(*HostState)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.hosts[id]
	if !ok {
		return false
	}
	fn(h)
	return true
}

// Get returns a copy of one host state.
func (s *Store) Get(id string) (HostState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h, ok := s.hosts[id]
	if !ok {
		return HostState{}, false
	}
	cp := *h
	cp.History = append([]Sample(nil), h.History...)
	return cp, true
}

// Snapshot returns all host states in configured order, with staleness applied.
func (s *Store) Snapshot() []HostState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now()
	out := make([]HostState, 0, len(s.order))
	for _, id := range s.order {
		h, ok := s.hosts[id]
		if !ok {
			continue
		}
		cp := *h
		cp.History = append([]Sample(nil), h.History...)
		if cp.Status == StatusOnline && !cp.LastSuccess.IsZero() && now.Sub(cp.LastSuccess) > s.staleAfter {
			cp.Status = StatusStale
		}
		out = append(out, cp)
	}
	return out
}

// RecordSuccess stores a successful sample and appends bounded history.
func (s *Store) RecordSuccess(id string, m collector.Metrics) {
	s.Update(id, func(h *HostState) {
		h.Status = StatusOnline
		h.Error = ""
		h.Connected = true
		h.Metrics = m
		h.HasMetrics = true
		h.LastAttempt = m.CollectedAt
		h.LastSuccess = m.CollectedAt
		h.History = append(h.History, Sample{
			At:       m.CollectedAt,
			CPU:      m.CPUPercent,
			CPUValid: m.CPUValid,
			Mem:      m.MemPercent(),
			Swap:     m.SwapPercent(),
			Load1:    m.Load1,
			NetRX:    m.NetRXRate,
			NetTX:    m.NetTXRate,
		})
		if len(h.History) > s.maxHistory {
			h.History = h.History[len(h.History)-s.maxHistory:]
		}
	})
}

// RecordFailure stores a classified failure while preserving last good metrics.
func (s *Store) RecordFailure(id string, err error) {
	status := Classify(err)
	s.Update(id, func(h *HostState) {
		h.Status = status
		h.Error = err.Error()
		h.LastAttempt = time.Now()
		h.Connected = false
	})
}

// SetDisabled marks a host disabled and clears its live state.
func (s *Store) SetDisabled(id string) {
	s.Update(id, func(h *HostState) {
		h.Status = StatusDisabled
		h.Error = ""
		h.Connected = false
		h.HasMetrics = false
		h.History = nil
	})
}

// SetPending marks a host as queued for collection.
func (s *Store) SetPending(id string) {
	s.Update(id, func(h *HostState) {
		h.Status = StatusPending
		h.Error = ""
	})
}

// Classify maps an error to a status.
func Classify(err error) Status {
	var unknown *sshx.UnknownHostKeyError
	var changed *sshx.ChangedHostKeyError
	if errors.As(err, &unknown) || errors.As(err, &changed) {
		return StatusHostKey
	}
	var pass *sshx.PassphraseRequiredError
	if errors.As(err, &pass) || errors.Is(err, sshx.ErrPasswordRequired) {
		return StatusAuthError
	}
	var auth *sshx.AuthError
	if errors.As(err, &auth) {
		return StatusAuthError
	}
	if errors.Is(err, collector.ErrUnsupported) {
		return StatusUnsupported
	}
	return StatusOffline
}

// Level is an event severity.
type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Event is a recent status change worth showing to the operator.
type Event struct {
	At      time.Time
	Host    string
	Level   Level
	Message string
}

// EventLog is a bounded ring of recent events.
type EventLog struct {
	mu     sync.Mutex
	events []Event
	max    int
}

// NewEventLog creates an event log retaining max entries.
func NewEventLog(max int) *EventLog {
	if max <= 0 {
		max = 100
	}
	return &EventLog{max: max}
}

// Add appends an event, dropping the oldest when full.
func (l *EventLog) Add(level Level, host, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, Event{At: time.Now(), Host: host, Level: level, Message: msg})
	if len(l.events) > l.max {
		l.events = l.events[len(l.events)-l.max:]
	}
}

// Recent returns the newest n events, oldest first.
func (l *EventLog) Recent(n int) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n <= 0 || n > len(l.events) {
		n = len(l.events)
	}
	out := make([]Event, n)
	copy(out, l.events[len(l.events)-n:])
	return out
}
