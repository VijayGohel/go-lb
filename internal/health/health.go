package health

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/VijayGohel/go-lb/internal/backend"
	"github.com/VijayGohel/go-lb/internal/metrics"
	"github.com/VijayGohel/go-lb/internal/pool"
)

// HealthChecker probes backend health on a configurable interval.
// Consecutive counters track direction changes to implement unhealthy/healthy thresholds.
type HealthChecker struct {
	pool               *pool.ServerPool
	path               string
	interval           time.Duration
	timeout            time.Duration
	unhealthyThreshold int
	healthyThreshold   int
	client             *http.Client
	mu                 sync.Mutex
	consecutive        map[string]int // +ve = consecutive successes, -ve = consecutive failures
}

// Option configures a HealthChecker.
type Option func(*HealthChecker)

// WithUnhealthyThreshold sets the number of consecutive failures before marking a backend dead.
func WithUnhealthyThreshold(n int) Option {
	return func(hc *HealthChecker) { hc.unhealthyThreshold = n }
}

// WithHealthyThreshold sets the number of consecutive successes before marking a dead backend alive.
func WithHealthyThreshold(n int) Option {
	return func(hc *HealthChecker) { hc.healthyThreshold = n }
}

// NewHealthChecker creates a HealthChecker with defaults: interval=10s, timeout=2s,
// unhealthyThreshold=3, healthyThreshold=2. Callers may override via Option functions.
func NewHealthChecker(p *pool.ServerPool, path string, interval, timeout time.Duration, opts ...Option) *HealthChecker {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	hc := &HealthChecker{
		pool:               p,
		path:               path,
		interval:           interval,
		timeout:            timeout,
		unhealthyThreshold: 3,
		healthyThreshold:   2,
		client:             &http.Client{Timeout: timeout},
		consecutive:        make(map[string]int),
	}
	for _, o := range opts {
		o(hc)
	}
	if hc.unhealthyThreshold < 1 {
		hc.unhealthyThreshold = 1
	}
	if hc.healthyThreshold < 1 {
		hc.healthyThreshold = 1
	}
	return hc
}

// CheckBackend performs a single HTTP health probe and updates alive state via thresholds.
func (hc *HealthChecker) CheckBackend(ctx context.Context, b *backend.Backend) {
	// Copy mutable config fields under lock to avoid races with UpdateConfig.
	hc.mu.Lock()
	path := hc.path
	client := hc.client
	hc.mu.Unlock()

	target := b.URL.String() + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		hc.recordFailure(b)
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		hc.recordFailure(b)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		hc.recordFailure(b)
		return
	}
	hc.recordSuccess(b)
}

func (hc *HealthChecker) recordSuccess(b *backend.Backend) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	key := b.URL.String()
	if hc.consecutive[key] < 0 {
		hc.consecutive[key] = 0 // direction changed
	}
	hc.consecutive[key]++
	if hc.consecutive[key] >= hc.healthyThreshold {
		if !b.IsAlive() {
			b.SetAlive(true)
			slog.Info("backend_up", "backend", key)
		}
		// Always emit the metric regardless of prior IsAlive value.
		// This handles cases where the backend was already marked alive
		// via another pathway (e.g. admin enable).
		metrics.SetBackendUp(key, true)
		hc.consecutive[key] = 0 // reset after threshold; also caps growth for always-healthy backends
	}
}

func (hc *HealthChecker) recordFailure(b *backend.Backend) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	key := b.URL.String()
	if hc.consecutive[key] > 0 {
		hc.consecutive[key] = 0 // direction changed
	}
	hc.consecutive[key]--
	if -hc.consecutive[key] >= hc.unhealthyThreshold {
		if b.IsAlive() {
			b.SetAlive(false)
			slog.Warn("backend_down", "backend", key, "error", "unhealthy threshold reached")
		}
		// Always emit the metric regardless of prior IsAlive value.
		// This handles cases where the backend was already marked dead
		// via another pathway (e.g. proxy error handler).
		metrics.SetBackendUp(key, false)
		hc.consecutive[key] = 0 // reset after threshold; also caps growth for always-failing backends
	}
}

// UpdateConfig updates health check parameters at runtime (used by hot reload).
func (hc *HealthChecker) UpdateConfig(path string, interval, timeout time.Duration, unhealthyThreshold, healthyThreshold int) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hc.path = path
	if interval > 0 {
		hc.interval = interval
	}
	if timeout > 0 {
		hc.timeout = timeout
		hc.client = &http.Client{Timeout: timeout}
	}
	if unhealthyThreshold < 1 {
		unhealthyThreshold = 1
	}
	if healthyThreshold < 1 {
		healthyThreshold = 1
	}
	hc.unhealthyThreshold = unhealthyThreshold
	hc.healthyThreshold = healthyThreshold
}

// Start runs health checks on all pool backends every interval until ctx is cancelled.
// It picks up interval changes from UpdateConfig by resetting the ticker each loop.
func (hc *HealthChecker) Start(ctx context.Context) {
	hc.mu.Lock()
	currentInterval := hc.interval
	hc.mu.Unlock()

	ticker := time.NewTicker(currentInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			var wg sync.WaitGroup
			for _, b := range hc.pool.Backends() {
				wg.Add(1)
				go func(b *backend.Backend) {
					defer wg.Done()
					hc.CheckBackend(ctx, b)
				}(b)
			}
			wg.Wait()

			// Pick up interval changes from UpdateConfig.
			hc.mu.Lock()
			if hc.interval != currentInterval {
				currentInterval = hc.interval
				ticker.Reset(currentInterval)
			}
			// Prune stale consecutive entries for backends no longer in the pool.
			activeURLs := make(map[string]bool, len(hc.pool.Backends()))
			for _, b := range hc.pool.Backends() {
				activeURLs[b.URL.String()] = true
			}
			for key := range hc.consecutive {
				if !activeURLs[key] {
					delete(hc.consecutive, key)
				}
			}
			hc.mu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}
