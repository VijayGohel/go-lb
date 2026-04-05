package health_test

import (
	"context"
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

func TestUpdateConfig_ChangesProbePathAtRuntime(t *testing.T) {
	// Backend returns 200 on /new-health, 404 on /health.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/new-health" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := &pool.ServerPool{}
	u, _ := url.Parse(srv.URL)
	b := &backend.Backend{URL: u, Weight: 1}
	b.SetAlive(true)
	p.AddBackend(b)

	// Health checker starts with /health path — backend will fail.
	hc := health.NewHealthChecker(p, "/health", 50*time.Millisecond, 2*time.Second,
		health.WithUnhealthyThreshold(1),
		health.WithHealthyThreshold(1),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hc.Start(ctx)

	// Wait for health check to mark backend dead (404 on /health).
	time.Sleep(120 * time.Millisecond)
	if b.IsAlive() {
		t.Fatal("expected backend to be marked dead with /health path returning 404")
	}

	// Update to /new-health path — backend should recover.
	hc.UpdateConfig("/new-health", 50*time.Millisecond, 2*time.Second, 1, 1)

	// Wait for recovery.
	time.Sleep(120 * time.Millisecond)
	if !b.IsAlive() {
		t.Fatal("expected backend to recover after path change to /new-health")
	}
}

func TestUpdateConfig_IntervalChangeIsPickedUp(t *testing.T) {
	var probeCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&probeCount, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &pool.ServerPool{}
	u, _ := url.Parse(srv.URL)
	b := &backend.Backend{URL: u, Weight: 1}
	b.SetAlive(true)
	p.AddBackend(b)

	// Start with a fast interval so we don't need to wait long.
	hc := health.NewHealthChecker(p, "/", 50*time.Millisecond, 2*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hc.Start(ctx)

	// Wait for a few probes.
	time.Sleep(200 * time.Millisecond)
	countBefore := atomic.LoadInt64(&probeCount)
	if countBefore < 1 {
		t.Fatalf("expected at least 1 probe before config change, got %d", countBefore)
	}

	// Change to a much slower interval.
	hc.UpdateConfig("/", 5*time.Second, 2*time.Second, 3, 2)

	// After the change, probe rate should drop dramatically.
	// With 5s interval, at most 1 probe in the next 500ms.
	time.Sleep(500 * time.Millisecond)
	countAfter := atomic.LoadInt64(&probeCount)

	// Should have at most 1 extra probe (the one in progress when we changed).
	extraProbes := countAfter - countBefore
	if extraProbes > 2 {
		t.Errorf("expected at most 2 probes after slowing interval, got %d", extraProbes)
	}
}

func TestHealthChecker_PrunesConsecutiveForRemovedBackends(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &pool.ServerPool{}
	u, _ := url.Parse(srv.URL)
	b := &backend.Backend{URL: u, Weight: 1}
	b.SetAlive(true)
	p.AddBackend(b)

	hc := health.NewHealthChecker(p, "/", 50*time.Millisecond, 2*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hc.Start(ctx)

	// Wait for a couple of health checks.
	time.Sleep(120 * time.Millisecond)

	// Remove backend from pool.
	p.Remove(srv.URL)

	// Wait for pruning tick.
	time.Sleep(120 * time.Millisecond)

	// No panic or deadlock = pass. The consecutive map should have been pruned.
	// We can't directly inspect hc.consecutive (unexported), but the test verifies
	// no runtime errors occur during removal + continued health checking.
}
