// Package admin provides the management dashboard API and web UI for GOLB.
package admin

import (
	"fmt"
	"sync"

	"golb/internal/strategy"
)

// BackendInfo is the JSON representation of a backend's current state and stats.
type BackendInfo struct {
	URL           string `json:"url"`
	Weight        int    `json:"weight"`
	Healthy       bool   `json:"healthy"`
	Blocked       bool   `json:"blocked"`
	ActiveConns   int64  `json:"active_conns"`
	TotalRequests int64  `json:"total_requests"`
	TotalErrors   int64  `json:"total_errors"`
}

// Registry is a thread-safe, mutable list of backends. It is the single
// source of truth for the runtime backend pool — both the admin API and the
// YAML hot-reload path write through it.
type Registry struct {
	mu       sync.RWMutex
	backends []*strategy.Backend
	strategy string // current strategy name

	// onChange is called (outside the lock) whenever the backend list changes.
	// The gateway uses this to rebuild and swap its Picker.
	onChange func(strategyName string, backends []*strategy.Backend)
}

// NewRegistry creates a Registry seeded with the given backends and strategy.
// onChange is called whenever the backend list is mutated.
func NewRegistry(
	backends []*strategy.Backend,
	strategyName string,
	onChange func(string, []*strategy.Backend),
) *Registry {
	return &Registry{
		backends: backends,
		strategy: strategyName,
		onChange: onChange,
	}
}

// List returns a snapshot of all backends with their current runtime state.
func (r *Registry) List() []BackendInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]BackendInfo, len(r.backends))
	for i, b := range r.backends {
		out[i] = BackendInfo{
			URL:           b.RawURL,
			Weight:        b.Weight,
			Healthy:       b.IsHealthy(),
			Blocked:       b.IsBlocked(),
			ActiveConns:   b.ActiveConns(),
			TotalRequests: b.TotalRequests(),
			TotalErrors:   b.TotalErrors(),
		}
	}
	return out
}

// Add appends a new backend to the pool and notifies the gateway.
// Returns an error if rawURL is already registered.
func (r *Registry) Add(rawURL string, weight int) error {
	b, err := strategy.NewBackend(rawURL, weight)
	if err != nil {
		return err
	}

	r.mu.Lock()
	for _, existing := range r.backends {
		if existing.RawURL == rawURL {
			r.mu.Unlock()
			return fmt.Errorf("backend %q already exists", rawURL)
		}
	}
	r.backends = append(r.backends, b)
	snapshot := r.snapshot()
	strat := r.strategy
	r.mu.Unlock()

	r.onChange(strat, snapshot)
	return nil
}

// Remove deletes the backend with the given URL from the pool.
// Returns an error if no backend with that URL is found.
func (r *Registry) Remove(rawURL string) error {
	r.mu.Lock()
	idx := r.find(rawURL)
	if idx < 0 {
		r.mu.Unlock()
		return fmt.Errorf("backend %q not found", rawURL)
	}
	r.backends = append(r.backends[:idx], r.backends[idx+1:]...)
	snapshot := r.snapshot()
	strat := r.strategy
	r.mu.Unlock()

	r.onChange(strat, snapshot)
	return nil
}

// Block marks the backend as blocked so the load-balancer skips it.
func (r *Registry) Block(rawURL string) error {
	r.mu.RLock()
	idx := r.find(rawURL)
	if idx < 0 {
		r.mu.RUnlock()
		return fmt.Errorf("backend %q not found", rawURL)
	}
	b := r.backends[idx]
	strat := r.strategy
	snapshot := r.snapshot()
	r.mu.RUnlock()

	b.SetBlocked(true)
	r.onChange(strat, snapshot)
	return nil
}

// Unblock clears the blocked flag, allowing traffic to the backend again.
func (r *Registry) Unblock(rawURL string) error {
	r.mu.RLock()
	idx := r.find(rawURL)
	if idx < 0 {
		r.mu.RUnlock()
		return fmt.Errorf("backend %q not found", rawURL)
	}
	b := r.backends[idx]
	strat := r.strategy
	snapshot := r.snapshot()
	r.mu.RUnlock()

	b.SetBlocked(false)
	r.onChange(strat, snapshot)
	return nil
}

// ReplaceAll atomically swaps the entire backend list (called on YAML hot-reload).
// Stats on the new backends start at zero.
func (r *Registry) ReplaceAll(backends []*strategy.Backend, strategyName string) {
	r.mu.Lock()
	r.backends = backends
	r.strategy = strategyName
	snapshot := r.snapshot()
	r.mu.Unlock()

	r.onChange(strategyName, snapshot)
}

// Backends returns the current backend slice (caller must not mutate).
func (r *Registry) Backends() []*strategy.Backend {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot()
}

// --- helpers ----------------------------------------------------------------

// find returns the index of the backend with the given URL, or -1.
// Must be called with at least a read lock held.
func (r *Registry) find(rawURL string) int {
	for i, b := range r.backends {
		if b.RawURL == rawURL {
			return i
		}
	}
	return -1
}

// snapshot returns a shallow copy of the backends slice.
// Must be called with at least a read lock held.
func (r *Registry) snapshot() []*strategy.Backend {
	cp := make([]*strategy.Backend, len(r.backends))
	copy(cp, r.backends)
	return cp
}
