package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/VijayGohel/go-lb/internal/admin"
	"github.com/VijayGohel/go-lb/internal/algo"
	"github.com/VijayGohel/go-lb/internal/backend"
	"github.com/VijayGohel/go-lb/internal/config"
	"github.com/VijayGohel/go-lb/internal/health"
	"github.com/VijayGohel/go-lb/internal/logger"
	"github.com/VijayGohel/go-lb/internal/pool"
	"github.com/VijayGohel/go-lb/internal/proxy"
)

func main() {
	// Determine config file path from first arg if it looks like a --config= flag.
	cfgPath := ""
	args := os.Args[1:]
	for i, a := range args {
		if len(a) > 9 && a[:9] == "--config=" {
			cfgPath = a[9:]
			args = append(args[:i], args[i+1:]...)
			break
		}
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

	a, err := algo.New(cfg.Pool.Algorithm)
	if err != nil {
		slog.Error("invalid algorithm", "error", err)
		os.Exit(1)
	}

	p := &pool.ServerPool{}
	lb := proxy.New(p, a)

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

	mainSrv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: lb,
	}

	var adminSrv *http.Server
	if cfg.Admin.Port != 0 {
		adminSrv = admin.New(p, lb).Start(ctx, fmt.Sprintf(":%d", cfg.Admin.Port))
		go func() {
			slog.Info("admin server started", "port", cfg.Admin.Port)
			if err := adminSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("admin server error", "error", err)
			}
		}()
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		slog.Info("shutting down gracefully")
		cancel()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutCancel()
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
	)
	if err := mainSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
