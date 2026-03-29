package health_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VijayGohel/go-lb/internal/backend"
	"github.com/VijayGohel/go-lb/internal/health"
	"github.com/VijayGohel/go-lb/internal/pool"
)

func makeBackend(rawURL string, alive bool) *backend.Backend {
	u, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	b := &backend.Backend{URL: u}
	b.SetAlive(alive)
	return b
}

func TestHealthChecker_MarksBackendAlive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	p := &pool.ServerPool{}
	b := makeBackend(srv.URL, false)
	p.AddBackend(b)

	hc := health.NewHealthChecker(p, "/health", time.Second, time.Second)
	hc.CheckBackend(context.Background(), b)

	if !b.IsAlive() {
		t.Fatal("backend should be marked alive after 200 health check")
	}
}

func TestHealthChecker_MarksBackendDead_On500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := &pool.ServerPool{}
	b := makeBackend(srv.URL, true)
	p.AddBackend(b)

	hc := health.NewHealthChecker(p, "/health", time.Second, time.Second)
	hc.CheckBackend(context.Background(), b)

	if b.IsAlive() {
		t.Fatal("backend should be dead after 500 health response")
	}
}

func TestHealthChecker_MarksBackendDead_OnConnectionRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := "http://" + ln.Addr().String()
	ln.Close()

	p := &pool.ServerPool{}
	b := makeBackend(addr, true)
	p.AddBackend(b)

	hc := health.NewHealthChecker(p, "/health", time.Second, 200*time.Millisecond)
	hc.CheckBackend(context.Background(), b)

	if b.IsAlive() {
		t.Fatal("backend should be dead when connection refused")
	}
}

func TestHealthChecker_RecoverDeadBackend(t *testing.T) {
	var healthy atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if healthy.Load() {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	p := &pool.ServerPool{}
	b := makeBackend(srv.URL, false)
	p.AddBackend(b)

	hc := health.NewHealthChecker(p, "/health", 100*time.Millisecond, time.Second)

	hc.CheckBackend(context.Background(), b)
	if b.IsAlive() {
		t.Fatal("backend should be dead when returning 500")
	}

	healthy.Store(true)
	hc.CheckBackend(context.Background(), b)
	if !b.IsAlive() {
		t.Fatal("backend should recover to alive after 200 health check")
	}
}
