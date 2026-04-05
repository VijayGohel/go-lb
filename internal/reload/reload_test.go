package reload_test

import (
	"context"
	"net/url"
	"sort"
	"testing"
	"time"

	"github.com/VijayGohel/go-lb/internal/algo"
	"github.com/VijayGohel/go-lb/internal/backend"
	"github.com/VijayGohel/go-lb/internal/config"
	"github.com/VijayGohel/go-lb/internal/health"
	"github.com/VijayGohel/go-lb/internal/pool"
	"github.com/VijayGohel/go-lb/internal/proxy"
	"github.com/VijayGohel/go-lb/internal/reload"
)

func baseCfg() config.Config {
	return config.Config{
		Server: config.ServerConfig{Port: 3030},
		Pool: config.PoolConfig{
			Algorithm: "round_robin",
			Backends: []config.BackendConfig{
				{URL: "http://localhost:8081", Weight: 1},
				{URL: "http://localhost:8082", Weight: 1},
			},
		},
		HealthCheck: config.HealthCheckConfig{
			Path:               "/health",
			Interval:           10 * time.Second,
			Timeout:            2 * time.Second,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}
}

// --- ComputeDiff tests ---

func TestComputeDiff_NoChanges(t *testing.T) {
	cfg := baseCfg()
	diff := reload.ComputeDiff(cfg, cfg)
	if !diff.Empty() {
		t.Error("identical configs should produce empty diff")
	}
}

func TestComputeDiff_BackendsAdded(t *testing.T) {
	old := baseCfg()
	new := baseCfg()
	new.Pool.Backends = append(new.Pool.Backends, config.BackendConfig{URL: "http://localhost:8083", Weight: 1})

	diff := reload.ComputeDiff(old, new)
	if len(diff.BackendsAdded) != 1 {
		t.Fatalf("want 1 added, got %d", len(diff.BackendsAdded))
	}
	if diff.BackendsAdded[0].URL != "http://localhost:8083" {
		t.Errorf("added URL: want http://localhost:8083, got %s", diff.BackendsAdded[0].URL)
	}
	if len(diff.BackendsRemoved) != 0 {
		t.Errorf("want 0 removed, got %d", len(diff.BackendsRemoved))
	}
}

func TestComputeDiff_BackendsRemoved(t *testing.T) {
	old := baseCfg()
	new := baseCfg()
	new.Pool.Backends = new.Pool.Backends[:1] // keep only first

	diff := reload.ComputeDiff(old, new)
	if len(diff.BackendsRemoved) != 1 {
		t.Fatalf("want 1 removed, got %d", len(diff.BackendsRemoved))
	}
	if diff.BackendsRemoved[0] != "http://localhost:8082" {
		t.Errorf("removed URL: want http://localhost:8082, got %s", diff.BackendsRemoved[0])
	}
	if len(diff.BackendsAdded) != 0 {
		t.Errorf("want 0 added, got %d", len(diff.BackendsAdded))
	}
}

func TestComputeDiff_BackendWeightChanged(t *testing.T) {
	old := baseCfg()
	new := baseCfg()
	new.Pool.Backends[0].Weight = 5

	diff := reload.ComputeDiff(old, new)
	if len(diff.BackendsChanged) != 1 {
		t.Fatalf("want 1 changed, got %d", len(diff.BackendsChanged))
	}
	if diff.BackendsChanged[0].Weight != 5 {
		t.Errorf("changed weight: want 5, got %d", diff.BackendsChanged[0].Weight)
	}
}

func TestComputeDiff_AlgorithmChanged(t *testing.T) {
	old := baseCfg()
	new := baseCfg()
	new.Pool.Algorithm = "least_connections"

	diff := reload.ComputeDiff(old, new)
	if !diff.AlgorithmChanged {
		t.Error("AlgorithmChanged should be true")
	}
	if diff.NewAlgorithm != "least_connections" {
		t.Errorf("NewAlgorithm: want least_connections, got %s", diff.NewAlgorithm)
	}
}

func TestComputeDiff_HealthChanged(t *testing.T) {
	old := baseCfg()
	new := baseCfg()
	new.HealthCheck.Path = "/ping"
	new.HealthCheck.Interval = 5 * time.Second

	diff := reload.ComputeDiff(old, new)
	if !diff.HealthChanged {
		t.Error("HealthChanged should be true")
	}
	if diff.NewHealthCheck.Path != "/ping" {
		t.Errorf("NewHealthCheck.Path: want /ping, got %s", diff.NewHealthCheck.Path)
	}
}

// --- Apply tests with real pool/proxy/health ---

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func setupSystem(t *testing.T) (*pool.ServerPool, *proxy.LoadBalancer, *health.HealthChecker) {
	t.Helper()
	p := &pool.ServerPool{}
	a, err := algo.New("round_robin")
	if err != nil {
		t.Fatal(err)
	}
	lb := proxy.New(p, a)

	urls := []string{"http://localhost:8081", "http://localhost:8082"}
	for _, raw := range urls {
		u := mustParseURL(t, raw)
		b := &backend.Backend{URL: u, Weight: 1}
		b.SetAlive(true)
		lb.SetupProxy(b)
		p.AddBackend(b)
	}

	hc := health.NewHealthChecker(p, "/health", 10*time.Second, 2*time.Second,
		health.WithUnhealthyThreshold(3),
		health.WithHealthyThreshold(2),
	)
	return p, lb, hc
}

func TestApply_AddBackend(t *testing.T) {
	p, lb, hc := setupSystem(t)
	// Start health checker so it's running (we won't wait for probes).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hc.Start(ctx)

	applier := reload.NewApplier(p, lb, hc)
	diff := reload.Diff{
		BackendsAdded: []config.BackendConfig{
			{URL: "http://localhost:8083", Weight: 2},
		},
	}
	if err := applier.Apply(diff, config.Config{}); err != nil {
		t.Fatal(err)
	}

	urls := p.BackendURLs()
	found := false
	for _, u := range urls {
		if u == "http://localhost:8083" {
			found = true
			break
		}
	}
	if !found {
		t.Error("backend http://localhost:8083 should have been added")
	}
	if len(urls) != 3 {
		t.Errorf("want 3 backends, got %d", len(urls))
	}
}

func TestApply_RemoveBackend(t *testing.T) {
	p, lb, hc := setupSystem(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hc.Start(ctx)

	applier := reload.NewApplier(p, lb, hc)
	diff := reload.Diff{
		BackendsRemoved: []string{"http://localhost:8082"},
	}
	if err := applier.Apply(diff, config.Config{}); err != nil {
		t.Fatal(err)
	}

	urls := p.BackendURLs()
	if len(urls) != 1 {
		t.Fatalf("want 1 backend, got %d", len(urls))
	}
	if urls[0] != "http://localhost:8081" {
		t.Errorf("remaining backend should be http://localhost:8081, got %s", urls[0])
	}
}

func TestApply_SwapAlgorithm(t *testing.T) {
	p, lb, hc := setupSystem(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hc.Start(ctx)

	applier := reload.NewApplier(p, lb, hc)
	diff := reload.Diff{
		AlgorithmChanged: true,
		NewAlgorithm:     "least_connections",
	}
	if err := applier.Apply(diff, config.Config{}); err != nil {
		t.Fatal(err)
	}
	// Verify the proxy still serves (no panic) by exercising it indirectly.
	// A more thorough test would send real requests; here we just confirm no error.
}

func TestApply_UpdateWeights(t *testing.T) {
	p, lb, hc := setupSystem(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hc.Start(ctx)

	applier := reload.NewApplier(p, lb, hc)
	diff := reload.Diff{
		BackendsChanged: []config.BackendConfig{
			{URL: "http://localhost:8081", Weight: 10},
		},
	}
	if err := applier.Apply(diff, config.Config{}); err != nil {
		t.Fatal(err)
	}

	for _, b := range p.Backends() {
		if b.URL.String() == "http://localhost:8081" && b.Weight != 10 {
			t.Errorf("weight: want 10, got %d", b.Weight)
		}
	}
}

func TestApply_InvalidAlgorithm(t *testing.T) {
	p, lb, hc := setupSystem(t)
	applier := reload.NewApplier(p, lb, hc)
	diff := reload.Diff{
		AlgorithmChanged: true,
		NewAlgorithm:     "does_not_exist",
	}
	if err := applier.Apply(diff, config.Config{}); err == nil {
		t.Error("expected error for unknown algorithm")
	}
}

func TestApply_UpdateHealthCheck(t *testing.T) {
	p, lb, hc := setupSystem(t)
	applier := reload.NewApplier(p, lb, hc)
	diff := reload.Diff{
		HealthChanged: true,
		NewHealthCheck: config.HealthCheckConfig{
			Path:               "/ping",
			Interval:           5 * time.Second,
			Timeout:            1 * time.Second,
			UnhealthyThreshold: 5,
			HealthyThreshold:   3,
		},
	}
	if err := applier.Apply(diff, config.Config{}); err != nil {
		t.Fatal(err)
	}
	// Health checker updated without error — values picked up on next tick.
}

func TestComputeDiff_MultipleChanges(t *testing.T) {
	old := baseCfg()
	new := baseCfg()

	// Remove 8082, add 8083, change weight of 8081, change algo, change health.
	new.Pool.Backends = []config.BackendConfig{
		{URL: "http://localhost:8081", Weight: 5},
		{URL: "http://localhost:8083", Weight: 1},
	}
	new.Pool.Algorithm = "weighted_round_robin"
	new.HealthCheck.Path = "/ready"

	diff := reload.ComputeDiff(old, new)

	if len(diff.BackendsAdded) != 1 || diff.BackendsAdded[0].URL != "http://localhost:8083" {
		t.Errorf("added: want [8083], got %v", diff.BackendsAdded)
	}
	if len(diff.BackendsRemoved) != 1 || diff.BackendsRemoved[0] != "http://localhost:8082" {
		t.Errorf("removed: want [8082], got %v", diff.BackendsRemoved)
	}
	if len(diff.BackendsChanged) != 1 || diff.BackendsChanged[0].Weight != 5 {
		t.Errorf("changed: want [{8081 w=5}], got %v", diff.BackendsChanged)
	}
	if !diff.AlgorithmChanged || diff.NewAlgorithm != "weighted_round_robin" {
		t.Errorf("algo: want weighted_round_robin, got %s (changed=%v)", diff.NewAlgorithm, diff.AlgorithmChanged)
	}
	if !diff.HealthChanged || diff.NewHealthCheck.Path != "/ready" {
		t.Errorf("health: want /ready, got %s (changed=%v)", diff.NewHealthCheck.Path, diff.HealthChanged)
	}
}

func TestBackendURLs(t *testing.T) {
	p := &pool.ServerPool{}
	u1, _ := url.Parse("http://localhost:8081")
	u2, _ := url.Parse("http://localhost:8082")
	p.AddBackend(&backend.Backend{URL: u1, Weight: 1})
	p.AddBackend(&backend.Backend{URL: u2, Weight: 1})

	urls := p.BackendURLs()
	sort.Strings(urls)
	if len(urls) != 2 {
		t.Fatalf("want 2, got %d", len(urls))
	}
	if urls[0] != "http://localhost:8081" || urls[1] != "http://localhost:8082" {
		t.Errorf("unexpected URLs: %v", urls)
	}
}
