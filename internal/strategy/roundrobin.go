package strategy

import "sync/atomic"

// RoundRobin distributes requests evenly across all healthy backends using
// a lock-free atomic counter. The counter monotonically increases; modulo
// arithmetic selects the backend.
type RoundRobin struct {
	backends []*Backend
	counter  atomic.Uint64
}

func NewRoundRobin(backends []*Backend) *RoundRobin {
	return &RoundRobin{backends: backends}
}

func (r *RoundRobin) Next() (*Backend, error) {
	healthy := healthySubset(r.backends)
	if len(healthy) == 0 {
		return nil, ErrNoHealthyBackend
	}
	idx := r.counter.Add(1) - 1
	b := healthy[idx%uint64(len(healthy))]
	b.IncConns()
	return b, nil
}

func (r *RoundRobin) Done(b *Backend) { b.DecConns() }
