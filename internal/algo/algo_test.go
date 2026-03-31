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

func TestLeastConnections_Name(t *testing.T) {
	a, err := algo.New("least_connections")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name() != "least_connections" {
		t.Fatalf("expected least_connections, got %s", a.Name())
	}
}

func TestLeastConnections_PicksFewestConns(t *testing.T) {
	b1 := makeBackend("http://localhost:8081", true)
	b2 := makeBackend("http://localhost:8082", true)
	b3 := makeBackend("http://localhost:8083", true)
	b1.IncrConns()
	b1.IncrConns() // b1 has 2
	b2.IncrConns() // b2 has 1
	// b3 has 0

	a, _ := algo.New("least_connections")
	got := a.Next([]*backend.Backend{b1, b2, b3})
	if got == nil {
		t.Fatal("Next returned nil")
	}
	if got.URL.String() != "http://localhost:8083" {
		t.Errorf("expected b3 (fewest conns), got %s", got.URL.String())
	}
}

func TestLeastConnections_TieBrokenByOrder(t *testing.T) {
	b1 := makeBackend("http://localhost:8081", true)
	b2 := makeBackend("http://localhost:8082", true)
	// both have 0 conns

	a, _ := algo.New("least_connections")
	got := a.Next([]*backend.Backend{b1, b2})
	if got.URL.String() != "http://localhost:8081" {
		t.Errorf("tie should be broken by registration order (first wins), got %s", got.URL.String())
	}
}

func TestLeastConnections_SkipsDeadBackends(t *testing.T) {
	dead := makeBackend("http://localhost:8081", false)
	alive := makeBackend("http://localhost:8082", true)
	alive.IncrConns()
	alive.IncrConns()
	alive.IncrConns() // more conns than dead, but dead should be skipped

	a, _ := algo.New("least_connections")
	got := a.Next([]*backend.Backend{dead, alive})
	if got == nil {
		t.Fatal("Next returned nil when one backend is alive")
	}
	if got.URL.String() != "http://localhost:8082" {
		t.Errorf("expected alive backend, got %s", got.URL.String())
	}
}

func TestWeightedRoundRobin_Name(t *testing.T) {
	a, err := algo.New("weighted_round_robin")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name() != "weighted_round_robin" {
		t.Fatalf("expected weighted_round_robin, got %s", a.Name())
	}
}

func TestWeightedRoundRobin_DistributesProportionally(t *testing.T) {
	b1 := makeBackend("http://localhost:8081", true)
	b1.Weight = 3
	b2 := makeBackend("http://localhost:8082", true)
	b2.Weight = 1

	a, _ := algo.New("weighted_round_robin")
	hits := map[string]int{}
	// 8 requests: b1 should get 6, b2 should get 2
	for i := 0; i < 8; i++ {
		b := a.Next([]*backend.Backend{b1, b2})
		if b == nil {
			t.Fatalf("iteration %d: Next returned nil", i)
		}
		hits[b.URL.String()]++
	}
	if hits["http://localhost:8081"] != 6 {
		t.Errorf("b1 (weight=3) expected 6 hits, got %d", hits["http://localhost:8081"])
	}
	if hits["http://localhost:8082"] != 2 {
		t.Errorf("b2 (weight=1) expected 2 hits, got %d", hits["http://localhost:8082"])
	}
}

func TestWeightedRoundRobin_ZeroWeightTreatedAsOne(t *testing.T) {
	b1 := makeBackend("http://localhost:8081", true)
	b1.Weight = 0 // should be treated as 1
	b2 := makeBackend("http://localhost:8082", true)
	b2.Weight = 0

	a, _ := algo.New("weighted_round_robin")
	hits := map[string]int{}
	for i := 0; i < 6; i++ {
		b := a.Next([]*backend.Backend{b1, b2})
		if b == nil {
			t.Fatalf("iteration %d: Next returned nil", i)
		}
		hits[b.URL.String()]++
	}
	if hits["http://localhost:8081"] != 3 || hits["http://localhost:8082"] != 3 {
		t.Errorf("equal-weight backends should get equal hits, got %v", hits)
	}
}

func TestWeightedRoundRobin_SkipsDeadBackends(t *testing.T) {
	dead := makeBackend("http://localhost:8081", false)
	dead.Weight = 5
	alive := makeBackend("http://localhost:8082", true)
	alive.Weight = 1

	a, _ := algo.New("weighted_round_robin")
	for i := 0; i < 4; i++ {
		b := a.Next([]*backend.Backend{dead, alive})
		if b == nil || b.URL.String() != "http://localhost:8082" {
			t.Fatalf("iteration %d: should only pick alive backend", i)
		}
	}
}
