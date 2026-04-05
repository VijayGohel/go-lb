package algo_test

import (
	"net/url"
	"sync"
	"testing"

	"github.com/VijayGohel/go-lb/internal/algo"
	"github.com/VijayGohel/go-lb/internal/backend"
)

func TestWeightedRoundRobin_ConcurrentWeightChange(t *testing.T) {
	wrr, _ := algo.New("weighted_round_robin")

	u1, _ := url.Parse("http://localhost:8081")
	u2, _ := url.Parse("http://localhost:8082")
	b1 := &backend.Backend{URL: u1, Weight: 3}
	b1.SetAlive(true)
	b2 := &backend.Backend{URL: u2, Weight: 1}
	b2.SetAlive(true)
	backends := []*backend.Backend{b1, b2}

	var wg sync.WaitGroup

	// Concurrent algo selections.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				peer := wrr.Next(backends)
				if peer == nil {
					t.Error("Next returned nil with alive backends")
					return
				}
			}
		}()
	}

	// Concurrent weight changes (simulating hot reload).
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				b1.SetWeight(w)
				b2.SetWeight(w + 1)
			}
		}(i + 1)
	}

	wg.Wait()
	// No panics, races, or deadlocks = pass.
}
