// Package health implements active health checking for upstream backends.
// A Monitor runs in the background and periodically probes each backend via
// an HTTP GET to a configurable path (default "/healthz"). Unhealthy backends
// are automatically excluded from traffic by the load-balancing strategy.
//
// Passive health checks (marking a backend unhealthy after a proxy error) are
// handled inside internal/proxy — this package only covers active probing.
package health

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"golb/internal/strategy"
)

// Config holds the parameters for the health monitor.
type Config struct {
	Interval time.Duration
	Timeout  time.Duration
	Path     string // e.g. "/healthz"
}

// Monitor periodically probes all registered backends and updates their health
// state. It is safe to call UpdateBackends while the monitor is running.
type Monitor struct {
	cfg    Config
	client *http.Client

	mu       sync.RWMutex
	backends []*strategy.Backend

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a Monitor but does not start it; call Start to begin probing.
func New(backends []*strategy.Backend, cfg Config) *Monitor {
	return &Monitor{
		cfg:      cfg,
		backends: backends,
		client:   &http.Client{Timeout: cfg.Timeout},
	}
}

// Start begins the background health-check loop. It runs an immediate check
// before the first ticker tick so backends are classified quickly at startup.
func (m *Monitor) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()

		ticker := time.NewTicker(m.cfg.Interval)
		defer ticker.Stop()

		m.probeAll() // immediate check on startup

		for {
			select {
			case <-ticker.C:
				m.probeAll()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop shuts down the background goroutine and waits for it to exit.
func (m *Monitor) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
}

// UpdateBackends atomically replaces the backend list. Safe to call while the
// monitor is running (e.g. on a config hot-reload).
func (m *Monitor) UpdateBackends(backends []*strategy.Backend) {
	m.mu.Lock()
	m.backends = backends
	m.mu.Unlock()
}

// probeAll checks every backend concurrently and waits for all to finish.
func (m *Monitor) probeAll() {
	m.mu.RLock()
	backends := m.backends
	m.mu.RUnlock()

	var wg sync.WaitGroup
	for _, b := range backends {
		wg.Add(1)
		go func(b *strategy.Backend) {
			defer wg.Done()
			m.probe(b)
		}(b)
	}
	wg.Wait()
}

// probe sends a single GET request and updates the backend's health flag.
func (m *Monitor) probe(b *strategy.Backend) {
	target := b.RawURL + m.cfg.Path

	resp, err := m.client.Get(target)
	if err != nil {
		if b.IsHealthy() {
			slog.Warn("health: backend became unhealthy",
				"backend", b.RawURL,
				"error", err,
			)
		}
		b.SetHealthy(false)
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		if !b.IsHealthy() {
			slog.Info("health: backend recovered", "backend", b.RawURL)
		}
		b.SetHealthy(true)
	} else {
		if b.IsHealthy() {
			slog.Warn("health: backend became unhealthy",
				"backend", b.RawURL,
				"status", resp.StatusCode,
			)
		}
		b.SetHealthy(false)
	}
}
