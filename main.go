package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type config struct {
	backends       []string
	port           int
	healthPath     string
	healthInterval time.Duration
	healthTimeout  time.Duration
}

// parseFlags parses the given args into a config.
// Extracted from main() for testability.
func parseFlags(args []string) config {
	fs := flag.NewFlagSet("golb", flag.ContinueOnError)
	var backendList string
	var cfg config

	fs.StringVar(&backendList, "backends", "", "Comma-separated backend URLs")
	fs.IntVar(&cfg.port, "port", 3030, "Port to listen on")
	fs.StringVar(&cfg.healthPath, "health-path", "/health", "Health check path")
	fs.DurationVar(&cfg.healthInterval, "health-interval", 10*time.Second, "Health check interval")
	fs.DurationVar(&cfg.healthTimeout, "health-timeout", 2*time.Second, "Health check timeout")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if backendList != "" {
		for _, b := range strings.Split(backendList, ",") {
			if b = strings.TrimSpace(b); b != "" {
				cfg.backends = append(cfg.backends, b)
			}
		}
	}
	return cfg
}

func main() {
	cfg := parseFlags(os.Args[1:])
	initLogger()

	if len(cfg.backends) == 0 {
		logger.Error("no backends provided — use --backends=http://host:port,...")
		os.Exit(1)
	}

	for _, rawURL := range cfg.backends {
		u, err := url.Parse(rawURL)
		if err != nil {
			logger.Error("invalid backend URL", "url", rawURL, "error", err)
			os.Exit(1)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			logger.Error("backend URL must use http or https scheme", "url", rawURL)
			os.Exit(1)
		}
		if u.Host == "" {
			logger.Error("backend URL must include a host", "url", rawURL)
			os.Exit(1)
		}
		b := &Backend{URL: u}
		b.SetAlive(true)
		setupProxy(b, &serverPool)
		serverPool.AddBackend(b)
		logger.Info("registered backend", "url", u.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hc := NewHealthChecker(&serverPool, cfg.healthPath, cfg.healthInterval, cfg.healthTimeout)
	go hc.Start(ctx)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.port),
		Handler: http.HandlerFunc(lb),
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		logger.Info("shutting down gracefully")
		cancel()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutCancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			logger.Error("shutdown error", "error", err)
		}
	}()

	logger.Info("golb started", "port", cfg.port, "backends", len(serverPool.backends))
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
