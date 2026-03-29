package pool_test

import (
	"net/url"
	"testing"

	"github.com/VijayGohel/go-lb/internal/backend"
	"github.com/VijayGohel/go-lb/internal/pool"
)

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

func makeBackend(rawURL string, alive bool) *backend.Backend {
	b := &backend.Backend{URL: mustParseURL(rawURL)}
	b.SetAlive(alive)
	return b
}

func TestServerPool_GetNextPeer_RoundRobin(t *testing.T) {
	p := &pool.ServerPool{}
	p.AddBackend(makeBackend("http://localhost:8081", true))
	p.AddBackend(makeBackend("http://localhost:8082", true))
	p.AddBackend(makeBackend("http://localhost:8083", true))

	seen := map[string]int{}
	for i := 0; i < 9; i++ {
		peer := p.GetNextPeer()
		if peer == nil {
			t.Fatalf("iteration %d: GetNextPeer returned nil with alive backends", i)
		} else {
			seen[peer.URL.String()]++
		}
	}
	for u, count := range seen {
		if count != 3 {
			t.Errorf("backend %s got %d requests, want 3", u, count)
		}
	}
}

func TestServerPool_GetNextPeer_SkipsDeadBackends(t *testing.T) {
	p := &pool.ServerPool{}
	p.AddBackend(makeBackend("http://localhost:8081", false))
	p.AddBackend(makeBackend("http://localhost:8082", true))
	p.AddBackend(makeBackend("http://localhost:8083", false))

	for i := 0; i < 6; i++ {
		peer := p.GetNextPeer()
		if peer == nil {
			t.Fatal("GetNextPeer returned nil when one backend is alive")
		} else if peer.URL.String() != "http://localhost:8082" {
			t.Errorf("expected only alive backend, got %s", peer.URL.String())
		}
	}
}

func TestServerPool_GetNextPeer_AllDead_ReturnsNil(t *testing.T) {
	p := &pool.ServerPool{}
	p.AddBackend(makeBackend("http://localhost:8081", false))
	p.AddBackend(makeBackend("http://localhost:8082", false))

	if peer := p.GetNextPeer(); peer != nil {
		t.Fatalf("expected nil when all backends dead, got %s", peer.URL.String())
	}
}

func TestServerPool_MarkBackendStatus(t *testing.T) {
	p := &pool.ServerPool{}
	b := makeBackend("http://localhost:8081", true)
	p.AddBackend(b)

	p.MarkBackendStatus(mustParseURL("http://localhost:8081"), false)

	if b.IsAlive() {
		t.Fatal("backend should be marked dead")
	}
}
