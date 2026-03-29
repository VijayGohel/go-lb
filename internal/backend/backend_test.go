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

func TestBackend_IsAlive_DefaultsToFalse(t *testing.T) {
	b := &backend.Backend{}
	if b.IsAlive() {
		t.Fatal("new backend should default to not alive")
	}
}

func TestBackend_SetAlive_True(t *testing.T) {
	b := &backend.Backend{}
	b.SetAlive(true)
	if !b.IsAlive() {
		t.Fatal("backend should be alive after SetAlive(true)")
	}
}

func TestBackend_SetAlive_False(t *testing.T) {
	b := &backend.Backend{}
	b.SetAlive(true)
	b.SetAlive(false)
	if b.IsAlive() {
		t.Fatal("backend should be dead after SetAlive(false)")
	}
}

func TestBackend_ConcurrentSetAlive(t *testing.T) {
	b := &backend.Backend{URL: mustParseURL("http://localhost:8081")}
	done := make(chan struct{})
	for i := 0; i < 100; i++ {
		go func(alive bool) {
			b.SetAlive(alive)
			_ = b.IsAlive()
			done <- struct{}{}
		}(i%2 == 0)
	}
	for i := 0; i < 100; i++ {
		<-done
	}
}
