// Package strategy implements pluggable load-balancing algorithms.
// All pickers are safe for concurrent use.
package strategy

import (
	"errors"
	"fmt"
)

// ErrNoHealthyBackend is returned when every backend is marked unhealthy.
var ErrNoHealthyBackend = errors.New("strategy: no healthy backend available")

// Picker selects the next backend for an incoming request.
// Done must be called exactly once after the request to backend b completes
// (success or failure) — used by LeastConnections to track active connections.
type Picker interface {
	Next() (*Backend, error)
	Done(b *Backend)
}

// New constructs the Picker named by strategy from the given backends.
// Valid strategy names: "round_robin", "weighted_round_robin", "least_connections".
func New(strategy string, backends []*Backend) (Picker, error) {
	if len(backends) == 0 {
		return nil, fmt.Errorf("strategy: at least one backend required")
	}
	switch strategy {
	case "round_robin", "":
		return NewRoundRobin(backends), nil
	case "weighted_round_robin":
		return NewWeightedRoundRobin(backends), nil
	case "least_connections":
		return NewLeastConnections(backends), nil
	default:
		return nil, fmt.Errorf("strategy: unknown algorithm %q", strategy)
	}
}
