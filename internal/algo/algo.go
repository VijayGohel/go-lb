package algo

import (
	"fmt"

	"github.com/VijayGohel/go-lb/internal/backend"
)

// Algorithm selects the next backend from a slice of alive candidates.
type Algorithm interface {
	Next(backends []*backend.Backend) *backend.Backend
	Name() string
}

// New returns the Algorithm for the given name, or an error if unknown.
// Valid names: "round_robin", "least_connections", "weighted_round_robin".
func New(name string) (Algorithm, error) {
	switch name {
	case "round_robin":
		return &RoundRobin{}, nil
	case "least_connections":
		return &LeastConnections{}, nil
	case "weighted_round_robin":
		return NewWeightedRoundRobin(), nil
	default:
		return nil, fmt.Errorf("unknown algorithm %q: must be round_robin, least_connections, or weighted_round_robin", name)
	}
}
