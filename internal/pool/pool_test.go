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

func TestServerPool_Backends_ReturnsCopy(t *testing.T) {
	p := &pool.ServerPool{}
	p.AddBackend(makeBackend("http://localhost:8081", true))
	p.AddBackend(makeBackend("http://localhost:8082", true))

	snap := p.Backends()
	if len(snap) != 2 {
		t.Fatalf("expected 2 backends, got %d", len(snap))
	}
	// Mutating the snapshot must not affect the pool.
	snap[0] = nil
	if p.Backends()[0] == nil {
		t.Fatal("mutating snapshot modified the pool's internal slice")
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

func TestServerPool_Remove_ExistingBackend(t *testing.T) {
	p := &pool.ServerPool{}
	p.AddBackend(makeBackend("http://localhost:8081", true))
	p.AddBackend(makeBackend("http://localhost:8082", true))

	removed := p.Remove("http://localhost:8081")
	if !removed {
		t.Fatal("Remove should return true for existing backend")
	}
	for _, b := range p.Backends() {
		if b.URL.String() == "http://localhost:8081" {
			t.Fatal("removed backend still in pool")
		}
	}
}

func TestServerPool_Remove_MissingBackend(t *testing.T) {
	p := &pool.ServerPool{}
	p.AddBackend(makeBackend("http://localhost:8081", true))

	removed := p.Remove("http://localhost:9999")
	if removed {
		t.Fatal("Remove should return false for unknown backend")
	}
	if len(p.Backends()) != 1 {
		t.Fatal("pool length changed after removing non-existent backend")
	}
}

func TestServerPool_AddBackend_NoDuplicates(t *testing.T) {
	p := &pool.ServerPool{}
	p.AddBackend(makeBackend("http://localhost:8081", true))
	p.AddBackend(makeBackend("http://localhost:8081", true)) // duplicate

	if len(p.Backends()) != 1 {
		t.Fatalf("expected 1 backend after duplicate add, got %d", len(p.Backends()))
	}
}
