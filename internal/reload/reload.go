package reload

import (
	"log/slog"
	"net/url"

	"github.com/VijayGohel/go-lb/internal/algo"
	"github.com/VijayGohel/go-lb/internal/backend"
	"github.com/VijayGohel/go-lb/internal/config"
	"github.com/VijayGohel/go-lb/internal/health"
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
}

// Empty returns true when the diff contains no actionable changes.
func (d Diff) Empty() bool {
	return len(d.BackendsAdded) == 0 &&
		len(d.BackendsRemoved) == 0 &&
		len(d.BackendsChanged) == 0 &&
		!d.AlgorithmChanged &&
		!d.HealthChanged
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

// Apply applies the diff to the running system. It returns an error if any
// critical step fails (e.g. invalid backend URL, unknown algorithm).
func (a *Applier) Apply(diff Diff, newCfg config.Config) error {
	// Add new backends.
	for _, bc := range diff.BackendsAdded {
		u, err := url.Parse(bc.URL)
		if err != nil {
			return err
		}
		b := &backend.Backend{URL: u, Weight: bc.Weight}
		a.proxy.SetupProxy(b)
		b.SetAlive(true)
		a.pool.AddBackend(b)
		slog.Info("reload: added backend", "url", bc.URL, "weight", bc.Weight)
	}

	// Remove old backends.
	for _, rawURL := range diff.BackendsRemoved {
		a.pool.Remove(rawURL)
		slog.Info("reload: removed backend", "url", rawURL)
	}

	// Update changed backend weights.
	for _, bc := range diff.BackendsChanged {
		for _, b := range a.pool.Backends() {
			if b.URL.String() == bc.URL {
				b.Weight = bc.Weight
				slog.Info("reload: updated backend weight", "url", bc.URL, "weight", bc.Weight)
				break
			}
		}
	}

	// Swap algorithm if changed.
	if diff.AlgorithmChanged {
		newAlgo, err := algo.New(diff.NewAlgorithm)
		if err != nil {
			return err
		}
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

	// Warn about TLS changes (require restart).
	// TLS is not yet in Config, but when it is, this is where we'd warn.
	// Placeholder for future TLS support.

	slog.Info("reload: complete",
		"added", len(diff.BackendsAdded),
		"removed", len(diff.BackendsRemoved),
		"changed", len(diff.BackendsChanged),
		"algo_changed", diff.AlgorithmChanged,
		"health_changed", diff.HealthChanged,
	)
	return nil
}
