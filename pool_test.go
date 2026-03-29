package main

import (
	"net/url"
	"testing"
)

func makeBackend(rawURL string, alive bool) *Backend {
	u := mustParseURL(rawURL)
	b := &Backend{URL: u}
	b.SetAlive(alive)
	return b
}

func TestServerPool_GetNextPeer_RoundRobin(t *testing.T) {
	pool := &ServerPool{}
	pool.AddBackend(makeBackend("http://localhost:8081", true))
	pool.AddBackend(makeBackend("http://localhost:8082", true))
	pool.AddBackend(makeBackend("http://localhost:8083", true))

	seen := map[string]int{}
	for i := 0; i < 9; i++ {
		peer := pool.GetNextPeer()
		if peer == nil {
			t.Fatal("GetNextPeer returned nil with alive backends")
		}
		seen[peer.URL.String()]++
	}

	for url, count := range seen {
		if count != 3 {
			t.Errorf("backend %s got %d requests, want 3", url, count)
		}
	}
}

func TestServerPool_GetNextPeer_SkipsDeadBackends(t *testing.T) {
	pool := &ServerPool{}
	pool.AddBackend(makeBackend("http://localhost:8081", false))
	pool.AddBackend(makeBackend("http://localhost:8082", true))
	pool.AddBackend(makeBackend("http://localhost:8083", false))

	for i := 0; i < 6; i++ {
		peer := pool.GetNextPeer()
		if peer == nil {
			t.Fatal("GetNextPeer returned nil when one backend is alive")
		}
		if peer.URL.String() != "http://localhost:8082" {
			t.Errorf("expected only alive backend, got %s", peer.URL.String())
		}
	}
}

func TestServerPool_GetNextPeer_AllDead_ReturnsNil(t *testing.T) {
	pool := &ServerPool{}
	pool.AddBackend(makeBackend("http://localhost:8081", false))
	pool.AddBackend(makeBackend("http://localhost:8082", false))

	if peer := pool.GetNextPeer(); peer != nil {
		t.Fatalf("expected nil when all backends dead, got %s", peer.URL.String())
	}
}

func TestServerPool_MarkBackendStatus(t *testing.T) {
	pool := &ServerPool{}
	b := makeBackend("http://localhost:8081", true)
	pool.AddBackend(b)

	u := mustParseURL("http://localhost:8081")
	pool.MarkBackendStatus(u, false)

	if b.IsAlive() {
		t.Fatal("backend should be marked dead")
	}
}

func mustParseURL2(raw string) *url.URL {
	u, _ := url.Parse(raw)
	return u
}
