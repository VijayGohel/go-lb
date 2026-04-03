package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/VijayGohel/go-lb/internal/admin"
	"github.com/VijayGohel/go-lb/internal/algo"
	"github.com/VijayGohel/go-lb/internal/backend"
	"github.com/VijayGohel/go-lb/internal/circuitbreaker"
	"github.com/VijayGohel/go-lb/internal/config"
	"github.com/VijayGohel/go-lb/internal/health"
	"github.com/VijayGohel/go-lb/internal/logger"
	"github.com/VijayGohel/go-lb/internal/metrics"
	"github.com/VijayGohel/go-lb/internal/middleware"
	"github.com/VijayGohel/go-lb/internal/pool"
	"github.com/VijayGohel/go-lb/internal/proxy"
	"github.com/VijayGohel/go-lb/internal/ratelimit"
	"github.com/VijayGohel/go-lb/internal/reload"
	"github.com/VijayGohel/go-lb/internal/sticky"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// extractConfigFlag pulls --config / -config (both --config=path and --config path forms)
// out of args before passing the remainder to flag.FlagSet.  This is necessary because
// flag.FlagSet would consume --config itself and leave no way for callers to know the path.
// extractConfigFlag pulls --config / -config (both --config=path and --config path forms)
// out of args before passing the remainder to flag.FlagSet.  This is necessary because
// flag.FlagSet would consume --config itself and leave no way for callers to know the path.
// Returns an error when --config is given without a value or with an empty value.
func extractConfigFlag(args []string) (path string, rest []string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--config" || a == "-config" {
			if i+1 >= len(args) {
				err = fmt.Errorf("flag %s requires an argument", a)
				return
			}
			path = args[i+1]
			rest = append(append(rest, args[:i]...), args[i+2:]...)
			return
		}
		if after, ok := strings.CutPrefix(a, "--config="); ok {
			if after == "" {
				err = fmt.Errorf("flag --config= requires a non-empty path")
				return
			}
			path = after
			rest = append(append(rest, args[:i]...), args[i+1:]...)
			return
		}
		if after, ok := strings.CutPrefix(a, "-config="); ok {
			if after == "" {
				err = fmt.Errorf("flag -config= requires a non-empty path")
				return
			}
			path = after
			rest = append(append(rest, args[:i]...), args[i+1:]...)
			return
		}
	}
	return "", args, nil
}

// parseTLSVersion maps a human-readable TLS version string to the crypto/tls constant.
// Trims whitespace, accepts "1.2" and "1.3". Warns and falls back to TLS 1.2 on unknown input.
func parseTLSVersion(s string) uint16 {
	s = strings.TrimSpace(s)
	switch s {
	case "1.3":
		return tls.VersionTLS13
	case "1.2", "":
		return tls.VersionTLS12
	default:
		slog.Warn("unsupported TLS min_version; falling back to TLS 1.2", "value", s)
		return tls.VersionTLS12
	}
}

func main() {
	// Extract --config / -config (both --config=path and --config path forms).
	cfgPath, args, err := extractConfigFlag(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		os.Exit(1)
	}

	cfg, err := config.Load(cfgPath, args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		os.Exit(1)
	}

	logger.Init()

	if len(cfg.Pool.Backends) == 0 {
		slog.Error("no backends configured — use --backends or a config file")
		os.Exit(1)
	}

	// Validate TLS cert/key paths early, before starting any services.
	if cfg.TLS.Enabled {
		if cfg.TLS.CertFile == "" || cfg.TLS.KeyFile == "" {
			slog.Error("TLS enabled but cert or key path is empty")
			os.Exit(1)
		}
		if _, err := os.Stat(cfg.TLS.CertFile); err != nil {
			slog.Error("TLS cert file not accessible", "path", cfg.TLS.CertFile, "error", err)
			os.Exit(1)
		}
		if _, err := os.Stat(cfg.TLS.KeyFile); err != nil {
			slog.Error("TLS key file not accessible", "path", cfg.TLS.KeyFile, "error", err)
			os.Exit(1)
		}
	}

	// Validate sticky session config.
	if cfg.StickySession.Enabled {
		if strings.TrimSpace(cfg.StickySession.CookieName) == "" {
			slog.Error("sticky sessions enabled but cookie_name is empty")
			os.Exit(1)
		}
		if cfg.StickySession.TTL <= 0 {
			slog.Error("sticky sessions enabled but TTL must be > 0", "ttl", cfg.StickySession.TTL)
			os.Exit(1)
		}
	}

	a, err := algo.New(cfg.Pool.Algorithm)
	if err != nil {
		slog.Error("invalid algorithm", "error", err)
		os.Exit(1)
	}

	p := &pool.ServerPool{}

	var proxyOpts []proxy.Option
	if cfg.CircuitBreaker.Enabled {
		cbReg := circuitbreaker.NewRegistry(circuitbreaker.Config{
			FailureThreshold: cfg.CircuitBreaker.FailureThreshold,
			SuccessThreshold: cfg.CircuitBreaker.SuccessThreshold,
			Timeout:          cfg.CircuitBreaker.Timeout,
		})
		proxyOpts = append(proxyOpts, proxy.WithCircuitBreaker(cbReg))
		slog.Info("circuit breaker enabled",
			"failure_threshold", cfg.CircuitBreaker.FailureThreshold,
			"success_threshold", cfg.CircuitBreaker.SuccessThreshold,
			"timeout", cfg.CircuitBreaker.Timeout,
		)
	}
	if cfg.StickySession.Enabled {
		sa := sticky.New(
			cfg.StickySession.CookieName,
			cfg.StickySession.TTL,
			sticky.WithSecure(cfg.TLS.Enabled),
		)
		proxyOpts = append(proxyOpts, proxy.WithStickySession(sa))
		slog.Info("sticky sessions enabled",
			"cookie", cfg.StickySession.CookieName,
			"ttl", cfg.StickySession.TTL,
		)
	}
	lb := proxy.New(p, a, proxyOpts...)

	for _, bc := range cfg.Pool.Backends {
		u, err := url.Parse(bc.URL)
		if err != nil {
			slog.Error("invalid backend URL", "url", bc.URL, "error", err)
			os.Exit(1)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			slog.Error("backend URL must use http or https scheme", "url", bc.URL)
			os.Exit(1)
		}
		if u.Host == "" {
			slog.Error("backend URL must include a host", "url", bc.URL)
			os.Exit(1)
		}
		b := &backend.Backend{URL: u, Weight: bc.Weight}
		b.SetAlive(true)
		lb.SetupProxy(b)
		p.AddBackend(b)
		slog.Info("registered backend", "url", u.String(), "weight", bc.Weight)
	}

	// Validate metrics endpoint availability.
	if cfg.Metrics.Enabled && cfg.Metrics.Port == 0 && cfg.Admin.Port == 0 {
		slog.Error("metrics enabled but no endpoint available: metrics.port and admin.port are both 0")
		os.Exit(1)
	}

	// Initialize Prometheus metrics and emit initial backend_up=1 for all backends.
	if cfg.Metrics.Enabled {
		metrics.Init()
		for _, b := range p.Backends() {
			metrics.SetBackendUp(b.URL.String(), true)
		}
		slog.Info("prometheus metrics enabled")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hc := health.NewHealthChecker(p,
		cfg.HealthCheck.Path,
		cfg.HealthCheck.Interval,
		cfg.HealthCheck.Timeout,
		health.WithUnhealthyThreshold(cfg.HealthCheck.UnhealthyThreshold),
		health.WithHealthyThreshold(cfg.HealthCheck.HealthyThreshold),
	)
	go hc.Start(ctx)

	var handler http.Handler = lb
	if cfg.RateLimit.Enabled {
		rl, err := ratelimit.New(cfg.RateLimit.RequestsPerSecond, cfg.RateLimit.Burst, cfg.RateLimit.PerIP)
		if err != nil {
			slog.Error("invalid rate limit config", "error", err)
			os.Exit(1)
		}
		defer rl.Stop()
		handler = middleware.Chain(handler, rl.Middleware())
		slog.Info("rate limiter enabled",
			"rps", cfg.RateLimit.RequestsPerSecond,
			"burst", cfg.RateLimit.Burst,
			"per_ip", cfg.RateLimit.PerIP,
		)
	}

	mainSrv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: handler,
	}

	var adminSrv *http.Server
	if cfg.Admin.Port != 0 {
		adminMux := http.NewServeMux()
		adminMux.Handle("/", admin.New(p, lb).Handler())
		// Serve /metrics on the admin port when no dedicated metrics port is configured.
		if cfg.Metrics.Enabled && cfg.Metrics.Port == 0 {
			adminMux.Handle("/metrics", promhttp.Handler())
		}
		adminSrv = &http.Server{
			Addr:    fmt.Sprintf(":%d", cfg.Admin.Port),
			Handler: adminMux,
		}
		go func() {
			slog.Info("admin server started", "port", cfg.Admin.Port)
			if err := adminSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("admin server error", "error", err)
			}
		}()
	}

	// Start a dedicated metrics server when a separate port is configured.
	var metricsSrv *http.Server
	if cfg.Metrics.Enabled && cfg.Metrics.Port != 0 {
		metricsMux := http.NewServeMux()
		metricsMux.Handle("/metrics", promhttp.Handler())
		metricsSrv = &http.Server{
			Addr:    fmt.Sprintf(":%d", cfg.Metrics.Port),
			Handler: metricsMux,
		}
		go func() {
			slog.Info("metrics server started", "port", cfg.Metrics.Port)
			if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("metrics server error", "error", err)
			}
		}()
	}

	// Set up hot reload applier for SIGHUP.
	applier := reload.NewApplier(p, lb, hc)
	currentCfg := cfg

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for sig := range quit {
			if sig == syscall.SIGHUP {
				if cfgPath == "" {
					slog.Warn("SIGHUP received but no config file was specified; ignoring")
					continue
				}
				slog.Info("SIGHUP received, reloading config", "path", cfgPath)
				newCfg, err := config.LoadFile(cfgPath)
				if err != nil {
					slog.Error("config reload failed", "error", err)
					continue
				}
				diff := reload.ComputeDiff(currentCfg, newCfg)
				if err := applier.Apply(diff, newCfg); err != nil {
					slog.Error("config apply failed", "error", err)
					continue
				}
				currentCfg = newCfg
				slog.Info("config reloaded successfully")
				continue
			}
			// SIGINT / SIGTERM → graceful shutdown
			break
		}
		slog.Info("shutting down gracefully")
		cancel()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutCancel()
		if metricsSrv != nil {
			if err := metricsSrv.Shutdown(shutCtx); err != nil {
				slog.Error("metrics shutdown error", "error", err)
			}
		}
		if adminSrv != nil {
			if err := adminSrv.Shutdown(shutCtx); err != nil {
				slog.Error("admin shutdown error", "error", err)
			}
		}
		if err := mainSrv.Shutdown(shutCtx); err != nil {
			slog.Error("shutdown error", "error", err)
		}
	}()

	slog.Info("golb started",
		"port", cfg.Server.Port,
		"algorithm", cfg.Pool.Algorithm,
		"backends", len(p.Backends()),
		"tls", cfg.TLS.Enabled,
	)
	if cfg.TLS.Enabled {
		mainSrv.TLSConfig = &tls.Config{MinVersion: parseTLSVersion(cfg.TLS.MinVersion)}
		if err := mainSrv.ListenAndServeTLS(cfg.TLS.CertFile, cfg.TLS.KeyFile); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	} else {
		if err := mainSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}
}
