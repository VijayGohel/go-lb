package reload

import (
	"fmt"
	"log/slog"
	"net/url"

	"github.com/VijayGohel/go-lb/internal/algo"
	"github.com/VijayGohel/go-lb/internal/backend"
	"github.com/VijayGohel/go-lb/internal/config"
	"github.com/VijayGohel/go-lb/internal/health"
	"github.com/VijayGohel/go-lb/internal/metrics"
	"github.com/VijayGohel/go-lb/internal/pool"
	"github.com/VijayGohel/go-lb/internal/proxy"
)

// Diff captures the differences between two configs relevant for hot-reload.
type Diff struct {
	BackendsAdded    []config.BackendConfig
	BackendsRemoved  []string               // URLs
	BackendsChanged  []config.BackendConfig // weight changed
	AlgorithmChanged bool
	NewAlgorithm     string
	HealthChanged    bool
	NewHealthCheck   config.HealthCheckConfig
	TLSChanged       bool
}

// Empty returns true when the diff contains no actionable changes.
func (d Diff) Empty() bool {
	return len(d.BackendsAdded) == 0 &&
		len(d.BackendsRemoved) == 0 &&
		len(d.BackendsChanged) == 0 &&
		!d.AlgorithmChanged &&
		!d.HealthChanged &&
		!d.TLSChanged
}

// ComputeDiff compares old and new configs and returns the differences.
func ComputeDiff(old, new config.Config) Diff {
	var d Diff

	// Build maps for backend comparison.
	oldBackends := make(map[string]config.BackendConfig, len(old.Pool.Backends))
	for _, b := range old.Pool.Backends {
		oldBackends[b.URL] = b
	}
	newBackends := make(map[string]config.BackendConfig, len(new.Pool.Backends))
	for _, b := range new.Pool.Backends {
		newBackends[b.URL] = b
	}

	// Backends added or weight changed.
	for u, nb := range newBackends {
		ob, exists := oldBackends[u]
		if !exists {
			d.BackendsAdded = append(d.BackendsAdded, nb)
		} else if ob.Weight != nb.Weight {
			d.BackendsChanged = append(d.BackendsChanged, nb)
		}
	}

	// Backends removed.
	for u := range oldBackends {
		if _, exists := newBackends[u]; !exists {
			d.BackendsRemoved = append(d.BackendsRemoved, u)
		}
	}

	// Algorithm change.
	if old.Pool.Algorithm != new.Pool.Algorithm {
		d.AlgorithmChanged = true
		d.NewAlgorithm = new.Pool.Algorithm
	}

	// Health check change.
	oh := old.HealthCheck
	nh := new.HealthCheck
	if oh.Path != nh.Path ||
		oh.Interval != nh.Interval ||
		oh.Timeout != nh.Timeout ||
		oh.UnhealthyThreshold != nh.UnhealthyThreshold ||
		oh.HealthyThreshold != nh.HealthyThreshold {
		d.HealthChanged = true
		d.NewHealthCheck = nh
	}

	// TLS change detection.
	if old.TLS != new.TLS {
		d.TLSChanged = true
	}

	return d
}

// Applier applies a Diff to the running load balancer components.
type Applier struct {
	pool   *pool.ServerPool
	proxy  *proxy.LoadBalancer
	health *health.HealthChecker
}

// NewApplier creates an Applier wired to the running pool, proxy, and health checker.
func NewApplier(p *pool.ServerPool, lb *proxy.LoadBalancer, hc *health.HealthChecker) *Applier {
	return &Applier{pool: p, proxy: lb, health: hc}
}

// Apply applies the diff to the running system. It pre-validates all changes
// before mutating any state, so invalid reloads don't partially apply.
func (a *Applier) Apply(diff Diff, newCfg config.Config) error {
	// --- Pre-validation phase (no mutations) ---

	// Validate all new backend URLs.
	type parsedBackend struct {
		url    *url.URL
		weight int
	}
	var toAdd []parsedBackend
	for _, bc := range diff.BackendsAdded {
		u, err := url.Parse(bc.URL)
		if err != nil {
			return fmt.Errorf("reload: invalid backend URL %q: %w", bc.URL, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("reload: backend URL must use http or https scheme: %s", bc.URL)
		}
		if u.Host == "" {
			return fmt.Errorf("reload: backend URL must include a host: %s", bc.URL)
		}
		toAdd = append(toAdd, parsedBackend{url: u, weight: bc.Weight})
	}

	// Validate algorithm if changed.
	var newAlgo algo.Algorithm
	if diff.AlgorithmChanged {
		var err error
		newAlgo, err = algo.New(diff.NewAlgorithm)
		if err != nil {
			return fmt.Errorf("reload: %w", err)
		}
	}

	// --- Mutation phase (all validated, safe to apply) ---

	// Add new backends.
	for i, bc := range diff.BackendsAdded {
		b := &backend.Backend{URL: toAdd[i].url, Weight: toAdd[i].weight}
		a.proxy.SetupProxy(b)
		b.SetAlive(true)
		a.pool.AddBackend(b)
		metrics.SetBackendUp(bc.URL, true)
		slog.Info("reload: added backend", "url", bc.URL, "weight", bc.Weight)
	}

	// Remove old backends.
	for _, rawURL := range diff.BackendsRemoved {
		a.pool.Remove(rawURL)
		metrics.SetBackendUp(rawURL, false)
		slog.Info("reload: removed backend", "url", rawURL)
	}

	// Update changed backend weights (O(n+m) via map lookup).
	if len(diff.BackendsChanged) > 0 {
		byURL := make(map[string]*backend.Backend, len(a.pool.Backends()))
		for _, b := range a.pool.Backends() {
			byURL[b.URL.String()] = b
		}
		for _, bc := range diff.BackendsChanged {
			if b, ok := byURL[bc.URL]; ok {
				b.SetWeight(bc.Weight)
				slog.Info("reload: updated backend weight", "url", bc.URL, "weight", bc.Weight)
			}
		}
	}

	// Swap algorithm if changed.
	if diff.AlgorithmChanged {
		a.proxy.SetAlgorithm(newAlgo)
		slog.Info("reload: algorithm changed", "algorithm", diff.NewAlgorithm)
	}

	// Update health checker if changed.
	if diff.HealthChanged {
		hc := diff.NewHealthCheck
		a.health.UpdateConfig(hc.Path, hc.Interval, hc.Timeout, hc.UnhealthyThreshold, hc.HealthyThreshold)
		slog.Info("reload: health check updated",
			"path", hc.Path,
			"interval", hc.Interval,
			"timeout", hc.Timeout,
		)
	}

	// TLS changes cannot be applied at runtime — warn the operator.
	if diff.TLSChanged {
		slog.Warn("reload: TLS configuration changes detected but require a full restart to take effect")
	}

	slog.Info("reload: complete",
		"added", len(diff.BackendsAdded),
		"removed", len(diff.BackendsRemoved),
		"changed", len(diff.BackendsChanged),
		"algo_changed", diff.AlgorithmChanged,
		"health_changed", diff.HealthChanged,
	)
	return nil
}
