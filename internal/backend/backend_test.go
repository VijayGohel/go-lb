package backend_test

import (
	"net/url"
	"testing"

	"github.com/VijayGohel/go-lb/internal/backend"
)

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

func TestBackend_ActiveConns(t *testing.T) {
	b := &backend.Backend{URL: mustParseURL("http://localhost:8081"), Weight: 1}
	b.SetAlive(true)

	if b.ActiveConns() != 0 {
		t.Fatalf("expected 0 active conns, got %d", b.ActiveConns())
	}

	b.IncrConns()
	b.IncrConns()
	if b.ActiveConns() != 2 {
		t.Fatalf("expected 2 active conns after 2 IncrConns, got %d", b.ActiveConns())
	}

	b.DecrConns()
	if b.ActiveConns() != 1 {
		t.Fatalf("expected 1 active conn after DecrConns, got %d", b.ActiveConns())
	}
}

func TestBackend_WeightDefault(t *testing.T) {
	b := &backend.Backend{URL: mustParseURL("http://localhost:8081")}
	// Weight zero-value is 0; the algo layer normalises it to 1. Just verify field exists.
	b.Weight = 3
	if b.Weight != 3 {
		t.Fatalf("expected Weight=3, got %d", b.Weight)
	}
}
