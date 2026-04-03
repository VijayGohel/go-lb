package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/VijayGohel/go-lb/internal/config"
)

func TestDefaults(t *testing.T) {
	cfg := config.Defaults()
	if cfg.Server.Port != 3030 {
		t.Errorf("default port: want 3030, got %d", cfg.Server.Port)
	}
	if cfg.Pool.Algorithm != "round_robin" {
		t.Errorf("default algorithm: want round_robin, got %s", cfg.Pool.Algorithm)
	}
	if cfg.HealthCheck.Path != "/health" {
		t.Errorf("default health path: want /health, got %s", cfg.HealthCheck.Path)
	}
	if cfg.HealthCheck.Interval != 10*time.Second {
		t.Errorf("default interval: want 10s, got %s", cfg.HealthCheck.Interval)
	}
	if cfg.HealthCheck.Timeout != 2*time.Second {
		t.Errorf("default timeout: want 2s, got %s", cfg.HealthCheck.Timeout)
	}
	if cfg.HealthCheck.UnhealthyThreshold != 3 {
		t.Errorf("default unhealthy_threshold: want 3, got %d", cfg.HealthCheck.UnhealthyThreshold)
	}
	if cfg.HealthCheck.HealthyThreshold != 2 {
		t.Errorf("default healthy_threshold: want 2, got %d", cfg.HealthCheck.HealthyThreshold)
	}
	if cfg.Admin.Port != 9090 {
		t.Errorf("default admin port: want 9090, got %d", cfg.Admin.Port)
	}
}

func writeYAML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "golb-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func TestLoad_YAMLOnly(t *testing.T) {
	path := writeYAML(t, `
server:
  port: 4040
pool:
  algorithm: least_connections
  backends:
    - url: http://localhost:8081
      weight: 3
    - url: http://localhost:8082
      weight: 1
health_check:
  path: /ping
  interval: 5s
  timeout: 1s
  unhealthy_threshold: 5
  healthy_threshold: 3
admin:
  port: 8080
`)
	cfg, err := config.Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 4040 {
		t.Errorf("port: want 4040, got %d", cfg.Server.Port)
	}
	if cfg.Pool.Algorithm != "least_connections" {
		t.Errorf("algorithm: want least_connections, got %s", cfg.Pool.Algorithm)
	}
	if len(cfg.Pool.Backends) != 2 {
		t.Fatalf("backends: want 2, got %d", len(cfg.Pool.Backends))
	}
	if cfg.Pool.Backends[0].URL != "http://localhost:8081" {
		t.Errorf("backend[0] url: want http://localhost:8081, got %s", cfg.Pool.Backends[0].URL)
	}
	if cfg.Pool.Backends[0].Weight != 3 {
		t.Errorf("backend[0] weight: want 3, got %d", cfg.Pool.Backends[0].Weight)
	}
	if cfg.HealthCheck.Interval != 5*time.Second {
		t.Errorf("interval: want 5s, got %s", cfg.HealthCheck.Interval)
	}
	if cfg.Admin.Port != 8080 {
		t.Errorf("admin port: want 8080, got %d", cfg.Admin.Port)
	}
}

func TestLoad_CLIOverridesYAML(t *testing.T) {
	path := writeYAML(t, `
server:
  port: 4040
pool:
  algorithm: least_connections
  backends:
    - url: http://localhost:8081
`)
	cfg, err := config.Load(path, []string{"--port=9999", "--algorithm=round_robin"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 9999 {
		t.Errorf("CLI --port should override YAML: want 9999, got %d", cfg.Server.Port)
	}
	if cfg.Pool.Algorithm != "round_robin" {
		t.Errorf("CLI --algorithm should override YAML: want round_robin, got %s", cfg.Pool.Algorithm)
	}
}

func TestLoad_CLIBackendsOverrideYAML(t *testing.T) {
	path := writeYAML(t, `
pool:
  backends:
    - url: http://localhost:8081
`)
	cfg, err := config.Load(path, []string{"--backends=http://localhost:9091,http://localhost:9092"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Pool.Backends) != 2 {
		t.Fatalf("want 2 backends from CLI, got %d", len(cfg.Pool.Backends))
	}
	if cfg.Pool.Backends[0].URL != "http://localhost:9091" {
		t.Errorf("backend[0]: want http://localhost:9091, got %s", cfg.Pool.Backends[0].URL)
	}
}

func TestLoad_NoYAML_CLIOnly(t *testing.T) {
	cfg, err := config.Load("", []string{"--port=7070", "--backends=http://localhost:8081"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 7070 {
		t.Errorf("want port 7070, got %d", cfg.Server.Port)
	}
	if len(cfg.Pool.Backends) != 1 {
		t.Fatalf("want 1 backend, got %d", len(cfg.Pool.Backends))
	}
}

func TestLoad_MissingYAMLFile_ReturnsError(t *testing.T) {
	_, err := config.Load(filepath.Join(t.TempDir(), "nonexistent.yaml"), nil)
	if err == nil {
		t.Fatal("expected error for missing YAML file")
	}
}

func TestDefaults_Metrics(t *testing.T) {
	cfg := config.Defaults()
	if !cfg.Metrics.Enabled {
		t.Error("default metrics.enabled: want true, got false")
	}
	if cfg.Metrics.Port != 0 {
		t.Errorf("default metrics.port: want 0, got %d", cfg.Metrics.Port)
	}
}

func TestLoad_MetricsYAML(t *testing.T) {
	path := writeYAML(t, `
metrics:
  enabled: false
  port: 9191
`)
	cfg, err := config.Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Metrics.Enabled {
		t.Error("metrics.enabled: want false, got true")
	}
	if cfg.Metrics.Port != 9191 {
		t.Errorf("metrics.port: want 9191, got %d", cfg.Metrics.Port)
	}
}

func TestLoad_MetricsCLIOverrides(t *testing.T) {
	path := writeYAML(t, `
metrics:
  enabled: true
  port: 9191
`)
	cfg, err := config.Load(path, []string{"--metrics-enabled=false", "--metrics-port=7070"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Metrics.Enabled {
		t.Error("CLI --metrics-enabled should override YAML: want false, got true")
	}
	if cfg.Metrics.Port != 7070 {
		t.Errorf("CLI --metrics-port should override YAML: want 7070, got %d", cfg.Metrics.Port)
	}
}

func TestLoad_UnsetCLIFlagsDoNotOverrideYAML(t *testing.T) {
	// Only --port is set; --algorithm must remain from YAML.
	path := writeYAML(t, `
pool:
  algorithm: least_connections
`)
	cfg, err := config.Load(path, []string{"--port=5050"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Pool.Algorithm != "least_connections" {
		t.Errorf("unset CLI flag should not override YAML: got %s", cfg.Pool.Algorithm)
	}
}

func TestDefaults_CircuitBreaker(t *testing.T) {
	cfg := config.Defaults()
	if cfg.CircuitBreaker.Enabled {
		t.Error("default cb.enabled: want false, got true")
	}
	if cfg.CircuitBreaker.FailureThreshold != 5 {
		t.Errorf("default cb.failure_threshold: want 5, got %d", cfg.CircuitBreaker.FailureThreshold)
	}
	if cfg.CircuitBreaker.SuccessThreshold != 2 {
		t.Errorf("default cb.success_threshold: want 2, got %d", cfg.CircuitBreaker.SuccessThreshold)
	}
	if cfg.CircuitBreaker.Timeout != 30*time.Second {
		t.Errorf("default cb.timeout: want 30s, got %s", cfg.CircuitBreaker.Timeout)
	}
}

func TestLoad_CircuitBreakerYAML(t *testing.T) {
	yaml := []byte("circuit_breaker:\n  enabled: true\n  failure_threshold: 10\n  success_threshold: 3\n  timeout: 1m\n")
	dir := t.TempDir()
	f := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(f, yaml, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CircuitBreaker.Enabled {
		t.Error("cb.enabled from YAML: want true")
	}
	if cfg.CircuitBreaker.FailureThreshold != 10 {
		t.Errorf("cb.failure_threshold: want 10, got %d", cfg.CircuitBreaker.FailureThreshold)
	}
	if cfg.CircuitBreaker.Timeout != time.Minute {
		t.Errorf("cb.timeout: want 1m, got %s", cfg.CircuitBreaker.Timeout)
	}
}

func TestLoad_CircuitBreakerCLIOverrides(t *testing.T) {
	cfg, err := config.Load("", []string{
		"--backends=http://localhost:8081",
		"--cb-enabled=true",
		"--cb-failure-threshold=8",
		"--cb-timeout=45s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CircuitBreaker.Enabled {
		t.Error("cb.enabled from CLI: want true")
	}
	if cfg.CircuitBreaker.FailureThreshold != 8 {
		t.Errorf("cb.failure_threshold from CLI: want 8, got %d", cfg.CircuitBreaker.FailureThreshold)
	}
	if cfg.CircuitBreaker.Timeout != 45*time.Second {
		t.Errorf("cb.timeout from CLI: want 45s, got %s", cfg.CircuitBreaker.Timeout)
	}
}

func TestLoad_CircuitBreakerInvalidThreshold_ReturnsError(t *testing.T) {
	yaml := []byte("circuit_breaker:\n  enabled: true\n  failure_threshold: 0\n")
	dir := t.TempDir()
	f := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(f, yaml, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(f, nil)
	if err == nil {
		t.Error("expected error for failure_threshold=0 when enabled")
	}
}
