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
type HealthChecker struct {
	pool     *pool.ServerPool
	path     string
	interval time.Duration
	timeout  time.Duration
	client   *http.Client
}

// NewHealthChecker creates a HealthChecker. interval must be > 0; defaults to 10s if not.
// timeout is the per-probe HTTP deadline.
func NewHealthChecker(p *pool.ServerPool, path string, interval, timeout time.Duration) *HealthChecker {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return &HealthChecker{
		pool:     p,
		path:     path,
		interval: interval,
		timeout:  timeout,
		client:   &http.Client{Timeout: timeout},
	}
}

// CheckBackend performs a single HTTP health probe and updates the backend's alive state.
// Only 2xx responses are considered healthy.
func (hc *HealthChecker) CheckBackend(ctx context.Context, b *backend.Backend) {
	target := b.URL.String() + hc.path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		wasAlive := b.IsAlive()
		b.SetAlive(false)
		if wasAlive {
			slog.Warn("backend_down", "backend", b.URL.String(), "error", err.Error())
		}
		return
	}

	resp, err := hc.client.Do(req)
	if err != nil {
		wasAlive := b.IsAlive()
		b.SetAlive(false)
		if wasAlive {
			slog.Warn("backend_down", "backend", b.URL.String(), "error", err.Error())
		}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		wasAlive := b.IsAlive()
		b.SetAlive(false)
		if wasAlive {
			slog.Warn("backend_down", "backend", b.URL.String(), "error", "non-2xx response")
		}
		return
	}

	wasAlive := b.IsAlive()
	b.SetAlive(true)
	if !wasAlive {
		slog.Info("backend_up", "backend", b.URL.String())
	}
}

// Start runs health checks on all pool backends every interval until ctx is cancelled.
// Probes run concurrently per tick; Start waits for all probes before the next tick.
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
