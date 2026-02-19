package strategy

import "sync"

// LeastConnections routes each new request to the healthy backend that
// currently has the fewest active connections. Ties are broken by the order
// backends appear in the list (first one wins). Active connection counts are
// tracked with the atomic counter on each Backend.
type LeastConnections struct {
	mu       sync.RWMutex
	backends []*Backend
}

func NewLeastConnections(backends []*Backend) *LeastConnections {
	return &LeastConnections{backends: backends}
}

func (l *LeastConnections) Next() (*Backend, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var best *Backend
	for _, b := range l.backends {
		if !b.IsHealthy() {
			continue
		}
		if best == nil || b.ActiveConns() < best.ActiveConns() {
			best = b
		}
	}
	if best == nil {
		return nil, ErrNoHealthyBackend
	}
	best.IncConns()
	return best, nil
}

func (l *LeastConnections) Done(b *Backend) { b.DecConns() }
