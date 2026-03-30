package algo_test

import (
	"net/url"
	"testing"

	"github.com/VijayGohel/go-lb/internal/algo"
	"github.com/VijayGohel/go-lb/internal/backend"
)

func makeBackend(rawURL string, alive bool) *backend.Backend {
	u, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	b := &backend.Backend{URL: u, Weight: 1}
	b.SetAlive(alive)
	return b
}

func TestNew_UnknownAlgorithm(t *testing.T) {
	_, err := algo.New("magic")
	if err == nil {
		t.Fatal("expected error for unknown algorithm name")
	}
}

func TestRoundRobin_Name(t *testing.T) {
	a, err := algo.New("round_robin")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name() != "round_robin" {
		t.Fatalf("expected name round_robin, got %s", a.Name())
	}
}

func TestRoundRobin_DistributesEvenly(t *testing.T) {
	backends := []*backend.Backend{
		makeBackend("http://localhost:8081", true),
		makeBackend("http://localhost:8082", true),
		makeBackend("http://localhost:8083", true),
	}
	a, _ := algo.New("round_robin")
	hits := map[string]int{}
	for i := 0; i < 9; i++ {
		b := a.Next(backends)
		if b == nil {
			t.Fatalf("iteration %d: Next returned nil", i)
		}
		hits[b.URL.String()]++
	}
	for u, count := range hits {
		if count != 3 {
			t.Errorf("backend %s got %d hits, want 3", u, count)
		}
	}
}

func TestRoundRobin_SkipsDeadBackends(t *testing.T) {
	backends := []*backend.Backend{
		makeBackend("http://localhost:8081", false),
		makeBackend("http://localhost:8082", true),
		makeBackend("http://localhost:8083", false),
	}
	a, _ := algo.New("round_robin")
	for i := 0; i < 6; i++ {
		b := a.Next(backends)
		if b == nil {
			t.Fatal("Next returned nil when one backend is alive")
		}
		if b.URL.String() != "http://localhost:8082" {
			t.Errorf("iteration %d: expected only alive backend, got %s", i, b.URL.String())
		}
	}
}

func TestRoundRobin_AllDead_ReturnsNil(t *testing.T) {
	backends := []*backend.Backend{
		makeBackend("http://localhost:8081", false),
		makeBackend("http://localhost:8082", false),
	}
	a, _ := algo.New("round_robin")
	if b := a.Next(backends); b != nil {
		t.Fatalf("expected nil when all dead, got %s", b.URL.String())
	}
}
