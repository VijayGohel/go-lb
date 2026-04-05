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

// pollUntil polls condition every 10ms until it returns true or deadline expires.
func pollUntil(t *testing.T, deadline time.Duration, desc string, cond func() bool) {
	t.Helper()
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-timer.C:
			t.Fatalf("timed out waiting for: %s", desc)
			return
		case <-tick.C:
			if cond() {
				return
			}
		}
	}
}

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

	hc := health.NewHealthChecker(p, "/health", 50*time.Millisecond, 2*time.Second,
		health.WithUnhealthyThreshold(1),
		health.WithHealthyThreshold(1),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hc.Start(ctx)

	// Poll until backend is marked dead (404 on /health).
	pollUntil(t, 2*time.Second, "backend marked dead", func() bool {
		return !b.IsAlive()
	})

	// Update to /new-health path — backend should recover.
	hc.UpdateConfig("/new-health", 50*time.Millisecond, 2*time.Second, 1, 1)

	// Poll until backend recovers.
	pollUntil(t, 2*time.Second, "backend recovered after path change", func() bool {
		return b.IsAlive()
	})
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

	// Start with a fast interval.
	hc := health.NewHealthChecker(p, "/", 50*time.Millisecond, 2*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hc.Start(ctx)

	// Poll until at least 1 probe fires.
	pollUntil(t, 2*time.Second, "at least 1 probe", func() bool {
		return atomic.LoadInt64(&probeCount) >= 1
	})

	countBefore := atomic.LoadInt64(&probeCount)

	// Change to a much slower interval (5s).
	hc.UpdateConfig("/", 5*time.Second, 2*time.Second, 3, 2)

	// After the change, probe rate should drop dramatically.
	// Allow up to 2 extra probes: the in-progress one + one that may fire
	// before the ticker resets.
	time.Sleep(500 * time.Millisecond)
	countAfter := atomic.LoadInt64(&probeCount)

	extraProbes := countAfter - countBefore
	if extraProbes > 2 {
		t.Errorf("expected at most 2 probes after slowing interval to 5s, got %d", extraProbes)
	}
}

// TestHealthChecker_ContinuesAfterBackendRemoval verifies that the health
// checker does not panic, deadlock, or crash when a backend is removed from
// the pool while health checking is active.
func TestHealthChecker_ContinuesAfterBackendRemoval(t *testing.T) {
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

	hc := health.NewHealthChecker(p, "/", 50*time.Millisecond, 2*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hc.Start(ctx)

	// Wait for at least one health check probe to actually fire.
	pollUntil(t, 2*time.Second, "at least one health check probe", func() bool {
		return atomic.LoadInt64(&probeCount) >= 1
	})

	// Remove backend from pool.
	p.Remove(srv.URL)

	// Health checker should continue without errors for several more ticks.
	time.Sleep(200 * time.Millisecond)

	// Verify no backends remain.
	if len(p.Backends()) != 0 {
		t.Errorf("expected empty pool after removal, got %d backends", len(p.Backends()))
	}
}
