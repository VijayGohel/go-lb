package algo

import (
	"sync/atomic"

	"github.com/VijayGohel/go-lb/internal/backend"
)

// RoundRobin distributes requests evenly across alive backends using an atomic counter.
type RoundRobin struct {
	current uint64
}

func (rr *RoundRobin) Name() string { return "round_robin" }

// Next returns the next alive backend in round-robin order.
// Returns nil if no backends are alive.
func (rr *RoundRobin) Next(backends []*backend.Backend) *backend.Backend {
	n := len(backends)
	if n == 0 {
		return nil
	}
	next := int(atomic.AddUint64(&rr.current, 1) % uint64(n))
	for i := 0; i < n; i++ {
		idx := (next + i) % n
		if backends[idx].IsAlive() {
			atomic.StoreUint64(&rr.current, uint64(idx))
			return backends[idx]
		}
	}
	return nil
}
