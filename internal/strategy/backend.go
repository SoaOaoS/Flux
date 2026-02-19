package strategy

import (
	"fmt"
	"net/url"
	"sync/atomic"

	"golb/internal/config"
)

// Backend is the runtime representation of an upstream server.
// Mutable state (health, active connections) uses atomics for lock-free
// concurrent access from many goroutines simultaneously.
type Backend struct {
	URL    *url.URL
	RawURL string
	Weight int

	healthy       atomic.Bool
	blocked       atomic.Bool
	activeConns   atomic.Int64
	totalRequests atomic.Int64
	totalErrors   atomic.Int64
}

// NewBackend parses rawURL and returns a healthy Backend ready for use.
func NewBackend(rawURL string, weight int) (*Backend, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("strategy: invalid backend URL %q: %w", rawURL, err)
	}
	b := &Backend{
		URL:    u,
		RawURL: rawURL,
		Weight: weight,
	}
	b.healthy.Store(true) // backends are assumed healthy at startup
	return b, nil
}

// NewBackends converts a slice of config entries into runtime Backend objects.
func NewBackends(cfgs []config.BackendCfg) ([]*Backend, error) {
	backends := make([]*Backend, 0, len(cfgs))
	for _, c := range cfgs {
		b, err := NewBackend(c.URL, c.Weight)
		if err != nil {
			return nil, err
		}
		backends = append(backends, b)
	}
	return backends, nil
}

func (b *Backend) IsHealthy() bool      { return b.healthy.Load() }
func (b *Backend) SetHealthy(v bool)    { b.healthy.Store(v) }
func (b *Backend) IsBlocked() bool      { return b.blocked.Load() }
func (b *Backend) SetBlocked(v bool)    { b.blocked.Store(v) }
func (b *Backend) IncConns() int64      { return b.activeConns.Add(1) }
func (b *Backend) DecConns() int64      { return b.activeConns.Add(-1) }
func (b *Backend) ActiveConns() int64   { return b.activeConns.Load() }
func (b *Backend) IncRequests()         { b.totalRequests.Add(1) }
func (b *Backend) TotalRequests() int64 { return b.totalRequests.Load() }
func (b *Backend) IncErrors()           { b.totalErrors.Add(1) }
func (b *Backend) TotalErrors() int64   { return b.totalErrors.Load() }

// healthySubset returns only the healthy, non-blocked backends from the given slice.
func healthySubset(all []*Backend) []*Backend {
	out := make([]*Backend, 0, len(all))
	for _, b := range all {
		if b.IsHealthy() && !b.IsBlocked() {
			out = append(out, b)
		}
	}
	return out
}
