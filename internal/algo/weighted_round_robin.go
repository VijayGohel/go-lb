package algo

import (
	"sync"

	"github.com/VijayGohel/go-lb/internal/backend"
)

// WeightedRoundRobin implements the smooth weighted round-robin algorithm (Nginx).
type WeightedRoundRobin struct {
	mu             sync.Mutex
	currentWeights map[string]int
}

func NewWeightedRoundRobin() *WeightedRoundRobin {
	return &WeightedRoundRobin{currentWeights: make(map[string]int)}
}

func (w *WeightedRoundRobin) Name() string { return "weighted_round_robin" }

func (w *WeightedRoundRobin) Next(backends []*backend.Backend) *backend.Backend {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Prune keys for backends no longer in the pool.
	active := make(map[string]struct{}, len(backends))
	for _, b := range backends {
		active[b.URL.String()] = struct{}{}
	}
	for key := range w.currentWeights {
		if _, ok := active[key]; !ok {
			delete(w.currentWeights, key)
		}
	}

	totalWeight := 0
	for _, b := range backends {
		if !b.IsAlive() {
			continue
		}
		weight := b.GetWeight()
		if weight <= 0 {
			weight = 1
		}
		totalWeight += weight
		w.currentWeights[b.URL.String()] += weight
	}

	var best *backend.Backend
	for _, b := range backends {
		if !b.IsAlive() {
			continue
		}
		if best == nil || w.currentWeights[b.URL.String()] > w.currentWeights[best.URL.String()] {
			best = b
		}
	}
	if best != nil {
		w.currentWeights[best.URL.String()] -= totalWeight
	}
	return best
}
