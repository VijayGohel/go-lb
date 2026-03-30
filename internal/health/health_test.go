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

	hc := health.NewHealthChecker(p, "/health", time.Second, time.Second,
		health.WithHealthyThreshold(1))
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

	hc := health.NewHealthChecker(p, "/health", time.Second, time.Second,
		health.WithUnhealthyThreshold(1))
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
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}

	p := &pool.ServerPool{}
	b := makeBackend(addr, true)
	p.AddBackend(b)

	hc := health.NewHealthChecker(p, "/health", time.Second, 200*time.Millisecond,
		health.WithUnhealthyThreshold(1))
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

	hc := health.NewHealthChecker(p, "/health", 100*time.Millisecond, time.Second,
		health.WithUnhealthyThreshold(1), health.WithHealthyThreshold(1))

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

func TestHealthChecker_UnhealthyThreshold_MarksDeadAfterNFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := &pool.ServerPool{}
	b := makeBackend(srv.URL, true)
	p.AddBackend(b)

	// unhealthyThreshold=3: backend must stay alive for first 2 failures
	hc := health.NewHealthChecker(p, "/health", time.Second, time.Second,
		health.WithUnhealthyThreshold(3),
		health.WithHealthyThreshold(2),
	)

	hc.CheckBackend(context.Background(), b)
	if !b.IsAlive() {
		t.Fatal("after 1st failure (threshold=3): backend should still be alive")
	}

	hc.CheckBackend(context.Background(), b)
	if !b.IsAlive() {
		t.Fatal("after 2nd failure (threshold=3): backend should still be alive")
	}

	hc.CheckBackend(context.Background(), b)
	if b.IsAlive() {
		t.Fatal("after 3rd failure (threshold=3): backend should be dead")
	}
}

func TestHealthChecker_HealthyThreshold_MarksAliveAfterNSuccesses(t *testing.T) {
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
	b := makeBackend(srv.URL, false) // start dead
	p.AddBackend(b)

	hc := health.NewHealthChecker(p, "/health", time.Second, time.Second,
		health.WithUnhealthyThreshold(3),
		health.WithHealthyThreshold(2),
	)

	healthy.Store(true)

	hc.CheckBackend(context.Background(), b)
	if b.IsAlive() {
		t.Fatal("after 1st success (healthyThreshold=2): backend should still be dead")
	}

	hc.CheckBackend(context.Background(), b)
	if !b.IsAlive() {
		t.Fatal("after 2nd success (healthyThreshold=2): backend should be alive")
	}
}

func TestHealthChecker_DirectionChange_ResetsCounter(t *testing.T) {
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
	b := makeBackend(srv.URL, true) // start alive
	p.AddBackend(b)

	hc := health.NewHealthChecker(p, "/health", time.Second, time.Second,
		health.WithUnhealthyThreshold(3),
		health.WithHealthyThreshold(2),
	)

	// 2 failures — not yet at threshold
	hc.CheckBackend(context.Background(), b)
	hc.CheckBackend(context.Background(), b)
	if !b.IsAlive() {
		t.Fatal("after 2 failures (threshold=3): backend should still be alive")
	}

	// 1 success — resets counter
	healthy.Store(true)
	hc.CheckBackend(context.Background(), b)
	if !b.IsAlive() {
		t.Fatal("after direction change (success): backend should still be alive")
	}

	// 2 more failures (fresh count) — not at threshold yet
	healthy.Store(false)
	hc.CheckBackend(context.Background(), b)
	hc.CheckBackend(context.Background(), b)
	if !b.IsAlive() {
		t.Fatal("counter was reset; 2 failures with threshold=3 should not kill backend")
	}
}
