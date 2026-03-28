package main

import (
	"context"
	"net/http"
	"time"
)

// HealthChecker probes backend health on a configurable interval.
type HealthChecker struct {
	pool     *ServerPool
	path     string
	interval time.Duration
	timeout  time.Duration
	client   *http.Client
}

// NewHealthChecker creates a HealthChecker. interval is how often to probe;
// timeout is the per-probe HTTP deadline.
func NewHealthChecker(pool *ServerPool, path string, interval, timeout time.Duration) *HealthChecker {
	return &HealthChecker{
		pool:     pool,
		path:     path,
		interval: interval,
		timeout:  timeout,
		client:   &http.Client{Timeout: timeout},
	}
}

// checkBackend performs a single HTTP health probe and updates Backend.Alive.
func (hc *HealthChecker) checkBackend(b *Backend) {
	target := b.URL.String() + hc.path
	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, target, nil,
	)
	if err != nil {
		wasAlive := b.IsAlive()
		b.SetAlive(false)
		if wasAlive {
			logger.Warn("backend_down", "backend", b.URL.String(), "error", err.Error())
		}
		return
	}

	resp, err := hc.client.Do(req)
	if err != nil || resp.StatusCode >= 500 {
		wasAlive := b.IsAlive()
		b.SetAlive(false)
		if wasAlive {
			errMsg := "non-2xx response"
			if err != nil {
				errMsg = err.Error()
			}
			logger.Warn("backend_down", "backend", b.URL.String(), "error", errMsg)
		}
		return
	}
	resp.Body.Close()

	wasAlive := b.IsAlive()
	b.SetAlive(true)
	if !wasAlive {
		logger.Info("backend_up", "backend", b.URL.String())
	}
}

// Start runs health checks on all pool backends every interval until ctx is cancelled.
func (hc *HealthChecker) Start(ctx context.Context) {
	ticker := time.NewTicker(hc.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			for _, b := range hc.pool.backends {
				go hc.checkBackend(b)
			}
		case <-ctx.Done():
			return
		}
	}
}
