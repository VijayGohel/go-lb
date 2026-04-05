package backend_test

import (
	"sync"
	"testing"

	"github.com/VijayGohel/go-lb/internal/backend"
)

func TestGetWeight_SetWeight_Concurrent(t *testing.T) {
	b := &backend.Backend{Weight: 5}

	var wg sync.WaitGroup
	// Concurrent writers.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				b.SetWeight(w)
			}
		}(i + 1)
	}
	// Concurrent readers.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				w := b.GetWeight()
				if w < 0 {
					t.Errorf("negative weight: %d", w)
				}
			}
		}()
	}
	wg.Wait()
	// Verify final weight is a valid positive value.
	if w := b.GetWeight(); w <= 0 {
		t.Errorf("final weight should be positive, got %d", w)
	}
}

func TestGetWeight_ReturnsSetValue(t *testing.T) {
	b := &backend.Backend{Weight: 3}
	if got := b.GetWeight(); got != 3 {
		t.Errorf("GetWeight: want 3, got %d", got)
	}
	b.SetWeight(10)
	if got := b.GetWeight(); got != 10 {
		t.Errorf("GetWeight after SetWeight(10): want 10, got %d", got)
	}
}
