package health

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/VijayGohel/go-lb/internal/backend"
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
	return hc
}

// CheckBackend performs a single HTTP health probe and updates alive state via thresholds.
func (hc *HealthChecker) CheckBackend(ctx context.Context, b *backend.Backend) {
	target := b.URL.String() + hc.path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		hc.recordFailure(b)
		return
	}
	resp, err := hc.client.Do(req)
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
	if hc.consecutive[key] >= hc.healthyThreshold && !b.IsAlive() {
		b.SetAlive(true)
		hc.consecutive[key] = 0
		slog.Info("backend_up", "backend", key)
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
	if -hc.consecutive[key] >= hc.unhealthyThreshold && b.IsAlive() {
		b.SetAlive(false)
		hc.consecutive[key] = 0
		slog.Warn("backend_down", "backend", key, "error", "unhealthy threshold reached")
	}
}

// Start runs health checks on all pool backends every interval until ctx is cancelled.
func (hc *HealthChecker) Start(ctx context.Context) {
	ticker := time.NewTicker(hc.interval)
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
		case <-ctx.Done():
			return
		}
	}
}
