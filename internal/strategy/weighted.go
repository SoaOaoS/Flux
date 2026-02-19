package strategy

import "sync"

// WeightedRoundRobin implements the Smooth Weighted Round Robin algorithm
// (the same algorithm used by nginx). It distributes requests proportionally
// to each backend's weight without producing long consecutive runs to a single
// server — making latency distribution more even than naive weight expansion.
//
// Algorithm (per request):
//  1. For every healthy backend, add its weight to currentWeight.
//  2. Select the backend with the highest currentWeight.
//  3. Subtract the sum of all healthy weights from the selected backend's
//     currentWeight.
type WeightedRoundRobin struct {
	mu      sync.Mutex
	entries []*wrrEntry
}

type wrrEntry struct {
	backend       *Backend
	currentWeight int
}

func NewWeightedRoundRobin(backends []*Backend) *WeightedRoundRobin {
	entries := make([]*wrrEntry, len(backends))
	for i, b := range backends {
		entries[i] = &wrrEntry{backend: b}
	}
	return &WeightedRoundRobin{entries: entries}
}

func (w *WeightedRoundRobin) Next() (*Backend, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Collect healthy entries and their total weight.
	var healthy []*wrrEntry
	total := 0
	for _, e := range w.entries {
		if e.backend.IsHealthy() {
			healthy = append(healthy, e)
			total += e.backend.Weight
		}
	}
	if len(healthy) == 0 {
		return nil, ErrNoHealthyBackend
	}

	// Step 1 — raise each healthy backend's currentWeight by its weight.
	for _, e := range healthy {
		e.currentWeight += e.backend.Weight
	}

	// Step 2 — pick the backend with the highest currentWeight.
	best := healthy[0]
	for _, e := range healthy[1:] {
		if e.currentWeight > best.currentWeight {
			best = e
		}
	}

	// Step 3 — subtract the total so the winner doesn't monopolise the next
	// several rounds.
	best.currentWeight -= total

	best.backend.IncConns()
	return best.backend, nil
}

func (w *WeightedRoundRobin) Done(b *Backend) { b.DecConns() }
