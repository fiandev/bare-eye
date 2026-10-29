// Package scheduler polls configured hosts concurrently with bounded
// concurrency, retries, and per-host isolation.
package scheduler

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/fiandev/bare-eye/internal/collector"
	"github.com/fiandev/bare-eye/internal/config"
	"github.com/fiandev/bare-eye/internal/state"
)

// Scheduler owns the per-host polling goroutines.
type Scheduler struct {
	cfg    *config.Config
	store  *state.Store
	events *state.EventLog
	prober Prober

	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	sem     chan struct{}
	mu      sync.Mutex
	workers map[string]*worker
}

type worker struct {
	srv        config.ServerConfig
	trigger    chan struct{}
	lastKey    string
	lastStatus state.Status
}

// New creates a scheduler. Call Start to begin polling.
func New(cfg *config.Config, store *state.Store, events *state.EventLog, prober Prober) *Scheduler {
	return &Scheduler{
		cfg:     cfg,
		store:   store,
		events:  events,
		prober:  prober,
		sem:     make(chan struct{}, cfg.Monitoring.MaxConcurrent),
		workers: map[string]*worker{},
	}
}

// Start launches one goroutine per enabled host.
func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.store.SetServers(s.cfg.Servers)
	for _, srv := range s.cfg.Servers {
		if !srv.IsEnabled() {
			s.store.SetDisabled(srv.ID)
			continue
		}
		s.startLocked(srv)
	}
	s.mu.Unlock()
}

func (s *Scheduler) startLocked(srv config.ServerConfig) {
	w := &worker{srv: srv, trigger: make(chan struct{}, 1)}
	s.workers[srv.ID] = w
	s.wg.Add(1)
	go s.loop(w)
}

// Stop cancels all workers and closes connections.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.workers = map[string]*worker{}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
	s.prober.Close()
}

// Restart applies a new configuration by replacing all workers.
func (s *Scheduler) Restart(cfg *config.Config) {
	s.Stop()
	s.mu.Lock()
	s.cfg = cfg
	s.sem = make(chan struct{}, cfg.Monitoring.MaxConcurrent)
	s.mu.Unlock()
	s.Start()
}

// Reconnect drops the cached connection and triggers an immediate poll.
func (s *Scheduler) Reconnect(id string) {
	s.prober.Reconnect(id)
	s.mu.Lock()
	w := s.workers[id]
	s.mu.Unlock()
	if w != nil {
		select {
		case w.trigger <- struct{}{}:
		default:
		}
	}
}

func (s *Scheduler) loop(w *worker) {
	defer s.wg.Done()
	failures := 0
	for {
		if s.ctx.Err() != nil {
			return
		}
		m, err := s.probe(w)
		if s.ctx.Err() != nil {
			return
		}
		if err != nil {
			failures++
			s.store.RecordFailure(w.srv.ID, err)
			s.note(w, state.Classify(err), err.Error())
		} else {
			failures = 0
			s.store.RecordSuccess(w.srv.ID, m)
			s.note(w, state.StatusOnline, "")
		}

		wait := w.srv.IntervalOrDefault(s.cfg)
		if failures > 0 {
			wait = backoff(wait, failures)
		}
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(wait):
		case <-w.trigger:
			failures = 0
		}
	}
}

func (s *Scheduler) probe(w *worker) (collector.Metrics, error) {
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-s.ctx.Done():
		return collector.Metrics{}, s.ctx.Err()
	}
	return s.prober.Probe(s.ctx, w.srv)
}

func (s *Scheduler) note(w *worker, status state.Status, msg string) {
	key := string(status) + "\x00" + msg
	if key == w.lastKey {
		return
	}
	prev := w.lastStatus
	w.lastStatus = status
	w.lastKey = key
	name := w.srv.DisplayName()
	switch {
	case status == state.StatusOnline && prev == "":
		s.events.Add(state.LevelInfo, name, "connected")
	case status == state.StatusOnline:
		s.events.Add(state.LevelInfo, name, "recovered")
	case status == state.StatusOffline:
		s.events.Add(state.LevelWarn, name, msg)
	default:
		s.events.Add(state.LevelError, name, msg)
	}
}

func backoff(base time.Duration, failures int) time.Duration {
	d := base
	for i := 1; i < failures && d < 30*time.Second; i++ {
		d *= 2
	}
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	// +/- 20% jitter avoids synchronized reconnect storms.
	jitter := time.Duration(float64(d) * (0.2 * (rand.Float64()*2 - 1)))
	d += jitter
	if d < 100*time.Millisecond {
		d = 100 * time.Millisecond
	}
	return d
}
