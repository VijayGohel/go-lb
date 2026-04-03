package config

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the resolved, merged configuration.
type Config struct {
	Server         ServerConfig
	Pool           PoolConfig
	HealthCheck    HealthCheckConfig
	Admin          AdminConfig
	RateLimit      RateLimitConfig
	Metrics        MetricsConfig
	CircuitBreaker CircuitBreakerConfig
	TLS            TLSConfig
	StickySession  StickySessionConfig
}

type ServerConfig struct {
	Port int
}

type PoolConfig struct {
	Algorithm string
	Backends  []BackendConfig
}

type BackendConfig struct {
	URL    string
	Weight int
}

type HealthCheckConfig struct {
	Path               string
	Interval           time.Duration
	Timeout            time.Duration
	UnhealthyThreshold int
	HealthyThreshold   int
}

type AdminConfig struct {
	Port int
}

type RateLimitConfig struct {
	Enabled           bool    `yaml:"enabled"`             // default false
	RequestsPerSecond float64 `yaml:"requests_per_second"` // default 1000
	Burst             int     `yaml:"burst"`               // default 200
	PerIP             bool    `yaml:"per_ip"`              // default true
}

type MetricsConfig struct {
	Enabled bool `yaml:"enabled"` // default true
	Port    int  `yaml:"port"`    // default 0 = use admin port
}

type CircuitBreakerConfig struct {
	Enabled          bool          `yaml:"enabled"`           // default false
	FailureThreshold int           `yaml:"failure_threshold"` // default 5
	SuccessThreshold int           `yaml:"success_threshold"` // default 2
	Timeout          time.Duration `yaml:"timeout"`           // default 30s
}

type TLSConfig struct {
	Enabled    bool   `yaml:"enabled"`     // default false
	CertFile   string `yaml:"cert_file"`   // path to PEM certificate
	KeyFile    string `yaml:"key_file"`    // path to PEM private key
	MinVersion string `yaml:"min_version"` // "1.2" or "1.3"; default "1.2"
}

type StickySessionConfig struct {
	Enabled    bool          `yaml:"enabled"`     // default false
	CookieName string        `yaml:"cookie_name"` // default "golb_backend"
	TTL        time.Duration `yaml:"ttl"`         // default 1h
}

// Defaults returns a Config pre-filled with production defaults.
func Defaults() Config {
	return Config{
		Server: ServerConfig{Port: 3030},
		Pool:   PoolConfig{Algorithm: "round_robin"},
		HealthCheck: HealthCheckConfig{
			Path:               "/health",
			Interval:           10 * time.Second,
			Timeout:            2 * time.Second,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
		Admin: AdminConfig{Port: 9090},
		RateLimit: RateLimitConfig{
			Enabled:           false,
			RequestsPerSecond: 1000,
			Burst:             200,
			PerIP:             true,
		},
		Metrics: MetricsConfig{
			Enabled: true,
			Port:    0,
		},
		CircuitBreaker: CircuitBreakerConfig{
			Enabled:          false,
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          30 * time.Second,
		},
		TLS: TLSConfig{
			Enabled:    false,
			MinVersion: "1.2",
		},
		StickySession: StickySessionConfig{
			Enabled:    false,
			CookieName: "golb_backend",
			TTL:        1 * time.Hour,
		},
	}
}

// fileConfig mirrors Config for YAML unmarshalling using string durations.
// Admin.Port is a pointer so that an explicit 0 (disable admin) can be
// distinguished from the field being absent (keep default).
type fileConfig struct {
	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`
	Pool struct {
		Algorithm string `yaml:"algorithm"`
		Backends  []struct {
			URL    string `yaml:"url"`
			Weight int    `yaml:"weight"`
		} `yaml:"backends"`
	} `yaml:"pool"`
	HealthCheck struct {
		Path               string `yaml:"path"`
		Interval           string `yaml:"interval"`
		Timeout            string `yaml:"timeout"`
		UnhealthyThreshold int    `yaml:"unhealthy_threshold"`
		HealthyThreshold   int    `yaml:"healthy_threshold"`
	} `yaml:"health_check"`
	Admin struct {
		Port *int `yaml:"port"`
	} `yaml:"admin"`
	RateLimit struct {
		Enabled           *bool    `yaml:"enabled"`
		RequestsPerSecond *float64 `yaml:"requests_per_second"`
		Burst             *int     `yaml:"burst"`
		PerIP             *bool    `yaml:"per_ip"`
	} `yaml:"rate_limit"`
	Metrics struct {
		Enabled *bool `yaml:"enabled"`
		Port    *int  `yaml:"port"`
	} `yaml:"metrics"`
	CircuitBreaker struct {
		Enabled          *bool  `yaml:"enabled"`
		FailureThreshold *int   `yaml:"failure_threshold"`
		SuccessThreshold *int   `yaml:"success_threshold"`
		Timeout          string `yaml:"timeout"`
	} `yaml:"circuit_breaker"`
	TLS struct {
		Enabled    *bool  `yaml:"enabled"`
		CertFile   string `yaml:"cert_file"`
		KeyFile    string `yaml:"key_file"`
		MinVersion string `yaml:"min_version"`
	} `yaml:"tls"`
	StickySession struct {
		Enabled    *bool  `yaml:"enabled"`
		CookieName string `yaml:"cookie_name"`
		TTL        string `yaml:"ttl"`
	} `yaml:"sticky_sessions"`
}

// Load reads the YAML file at path (if non-empty), then applies CLI overrides.
// CLI values only override when explicitly set on the command line.
func Load(path string, args []string) (Config, error) {
	cfg := Defaults()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("reading config file: %w", err)
		}
		var fc fileConfig
		if err := yaml.Unmarshal(data, &fc); err != nil {
			return Config{}, fmt.Errorf("parsing config file: %w", err)
		}
		if err := applyFileConfig(&cfg, &fc); err != nil {
			return Config{}, err
		}
	}

	if err := applyCLI(&cfg, args); err != nil {
		return Config{}, err
	}
	if cfg.CircuitBreaker.Enabled {
		if cfg.CircuitBreaker.FailureThreshold <= 0 {
			return Config{}, fmt.Errorf("invalid circuit_breaker.failure_threshold %d: must be > 0", cfg.CircuitBreaker.FailureThreshold)
		}
		if cfg.CircuitBreaker.SuccessThreshold <= 0 {
			return Config{}, fmt.Errorf("invalid circuit_breaker.success_threshold %d: must be > 0", cfg.CircuitBreaker.SuccessThreshold)
		}
		if cfg.CircuitBreaker.Timeout <= 0 {
			return Config{}, fmt.Errorf("invalid circuit_breaker.timeout %s: must be > 0", cfg.CircuitBreaker.Timeout)
		}
	}
	return cfg, nil
}

func applyFileConfig(cfg *Config, fc *fileConfig) error {
	if fc.Server.Port != 0 {
		cfg.Server.Port = fc.Server.Port
	}
	if fc.Pool.Algorithm != "" {
		cfg.Pool.Algorithm = fc.Pool.Algorithm
	}
	if len(fc.Pool.Backends) > 0 {
		cfg.Pool.Backends = make([]BackendConfig, len(fc.Pool.Backends))
		for i, b := range fc.Pool.Backends {
			w := b.Weight
			if w <= 0 {
				w = 1
			}
			cfg.Pool.Backends[i] = BackendConfig{URL: b.URL, Weight: w}
		}
	}
	if fc.HealthCheck.Path != "" {
		cfg.HealthCheck.Path = fc.HealthCheck.Path
	}
	if fc.HealthCheck.Interval != "" {
		d, err := time.ParseDuration(fc.HealthCheck.Interval)
		if err != nil {
			return fmt.Errorf("invalid health_check.interval %q: %w", fc.HealthCheck.Interval, err)
		}
		cfg.HealthCheck.Interval = d
	}
	if fc.HealthCheck.Timeout != "" {
		d, err := time.ParseDuration(fc.HealthCheck.Timeout)
		if err != nil {
			return fmt.Errorf("invalid health_check.timeout %q: %w", fc.HealthCheck.Timeout, err)
		}
		cfg.HealthCheck.Timeout = d
	}
	if fc.HealthCheck.UnhealthyThreshold != 0 {
		cfg.HealthCheck.UnhealthyThreshold = fc.HealthCheck.UnhealthyThreshold
	}
	if fc.HealthCheck.HealthyThreshold != 0 {
		cfg.HealthCheck.HealthyThreshold = fc.HealthCheck.HealthyThreshold
	}
	if fc.Admin.Port != nil {
		cfg.Admin.Port = *fc.Admin.Port
	}
	if fc.RateLimit.Enabled != nil {
		cfg.RateLimit.Enabled = *fc.RateLimit.Enabled
	}
	if fc.RateLimit.RequestsPerSecond != nil {
		cfg.RateLimit.RequestsPerSecond = *fc.RateLimit.RequestsPerSecond
	}
	if fc.RateLimit.Burst != nil {
		cfg.RateLimit.Burst = *fc.RateLimit.Burst
	}
	if fc.RateLimit.PerIP != nil {
		cfg.RateLimit.PerIP = *fc.RateLimit.PerIP
	}
	if fc.Metrics.Enabled != nil {
		cfg.Metrics.Enabled = *fc.Metrics.Enabled
	}
	if fc.Metrics.Port != nil {
		cfg.Metrics.Port = *fc.Metrics.Port
	}
	if fc.CircuitBreaker.Enabled != nil {
		cfg.CircuitBreaker.Enabled = *fc.CircuitBreaker.Enabled
	}
	if fc.CircuitBreaker.FailureThreshold != nil {
		cfg.CircuitBreaker.FailureThreshold = *fc.CircuitBreaker.FailureThreshold
	}
	if fc.CircuitBreaker.SuccessThreshold != nil {
		cfg.CircuitBreaker.SuccessThreshold = *fc.CircuitBreaker.SuccessThreshold
	}
	if fc.CircuitBreaker.Timeout != "" {
		d, err := time.ParseDuration(fc.CircuitBreaker.Timeout)
		if err != nil {
			return fmt.Errorf("invalid circuit_breaker.timeout %q: %w", fc.CircuitBreaker.Timeout, err)
		}
		cfg.CircuitBreaker.Timeout = d
	}
	// TLS
	if fc.TLS.Enabled != nil {
		cfg.TLS.Enabled = *fc.TLS.Enabled
	}
	if fc.TLS.CertFile != "" {
		cfg.TLS.CertFile = fc.TLS.CertFile
	}
	if fc.TLS.KeyFile != "" {
		cfg.TLS.KeyFile = fc.TLS.KeyFile
	}
	if fc.TLS.MinVersion != "" {
		v := strings.TrimSpace(fc.TLS.MinVersion)
		if v != "1.2" && v != "1.3" {
			return fmt.Errorf("invalid tls.min_version %q: must be \"1.2\" or \"1.3\"", fc.TLS.MinVersion)
		}
		cfg.TLS.MinVersion = v
	}
	// Sticky Sessions
	if fc.StickySession.Enabled != nil {
		cfg.StickySession.Enabled = *fc.StickySession.Enabled
	}
	if fc.StickySession.CookieName != "" {
		cfg.StickySession.CookieName = fc.StickySession.CookieName
	}
	if fc.StickySession.TTL != "" {
		d, err := time.ParseDuration(fc.StickySession.TTL)
		if err != nil {
			return fmt.Errorf("invalid sticky_sessions.ttl %q: %w", fc.StickySession.TTL, err)
		}
		cfg.StickySession.TTL = d
	}
	return nil
}

func applyCLI(cfg *Config, args []string) error {
	fs := flag.NewFlagSet("golb", flag.ContinueOnError)

	var (
		port               int
		backendsRaw        string
		algorithm          string
		healthPath         string
		healthInterval     time.Duration
		healthTimeout      time.Duration
		adminPort          int
		rlEnabled          bool
		rlRPS              float64
		rlBurst            int
		rlPerIP            bool
		metricsEnabled     bool
		metricsPort        int
		cbEnabled          bool
		cbFailureThreshold int
		cbSuccessThreshold int
		cbTimeout          time.Duration
		tlsEnabled         bool
		tlsCert            string
		tlsKey             string
		tlsMinVersion      string
		stickyEnabled      bool
		stickyCookieName   string
		stickyTTL          time.Duration
	)

	fs.IntVar(&port, "port", 0, "Port to listen on")
	fs.StringVar(&backendsRaw, "backends", "", "Comma-separated backend URLs (weight=1)")
	fs.StringVar(&algorithm, "algorithm", "", "Algorithm: round_robin|least_connections|weighted_round_robin")
	fs.StringVar(&healthPath, "health-path", "", "Health check path")
	fs.DurationVar(&healthInterval, "health-interval", 0, "Health check interval")
	fs.DurationVar(&healthTimeout, "health-timeout", 0, "Health check timeout")
	fs.IntVar(&adminPort, "admin-port", 0, "Admin server port (0=disabled)")
	fs.BoolVar(&rlEnabled, "rl-enabled", cfg.RateLimit.Enabled, "Enable rate limiting")
	fs.Float64Var(&rlRPS, "rl-rps", cfg.RateLimit.RequestsPerSecond, "Rate limit: requests per second")
	fs.IntVar(&rlBurst, "rl-burst", cfg.RateLimit.Burst, "Rate limit: burst size")
	fs.BoolVar(&rlPerIP, "rl-per-ip", cfg.RateLimit.PerIP, "Rate limit: per-IP mode")
	fs.BoolVar(&metricsEnabled, "metrics-enabled", cfg.Metrics.Enabled, "Enable Prometheus metrics")
	fs.IntVar(&metricsPort, "metrics-port", cfg.Metrics.Port, "Dedicated metrics port (0=use admin port)")
	fs.BoolVar(&cbEnabled, "cb-enabled", cfg.CircuitBreaker.Enabled, "Enable circuit breaker")
	fs.IntVar(&cbFailureThreshold, "cb-failure-threshold", cfg.CircuitBreaker.FailureThreshold, "Circuit breaker failure threshold")
	fs.IntVar(&cbSuccessThreshold, "cb-success-threshold", cfg.CircuitBreaker.SuccessThreshold, "Circuit breaker success threshold")
	fs.DurationVar(&cbTimeout, "cb-timeout", cfg.CircuitBreaker.Timeout, "Circuit breaker open timeout")
	fs.BoolVar(&tlsEnabled, "tls-enabled", cfg.TLS.Enabled, "Enable TLS termination")
	fs.StringVar(&tlsCert, "tls-cert", cfg.TLS.CertFile, "Path to TLS certificate PEM file")
	fs.StringVar(&tlsKey, "tls-key", cfg.TLS.KeyFile, "Path to TLS private key PEM file")
	fs.StringVar(&tlsMinVersion, "tls-min-version", cfg.TLS.MinVersion, "Minimum TLS version: 1.2 or 1.3")
	fs.BoolVar(&stickyEnabled, "sticky-enabled", cfg.StickySession.Enabled, "Enable sticky sessions")
	fs.StringVar(&stickyCookieName, "sticky-cookie-name", cfg.StickySession.CookieName, "Sticky session cookie name")
	fs.DurationVar(&stickyTTL, "sticky-ttl", cfg.StickySession.TTL, "Sticky session cookie TTL")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parsing CLI flags: %w", err)
	}

	// Only override when the flag was explicitly provided on the command line.
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "port":
			cfg.Server.Port = port
		case "backends":
			cfg.Pool.Backends = nil
			for _, raw := range strings.Split(backendsRaw, ",") {
				if raw = strings.TrimSpace(raw); raw != "" {
					cfg.Pool.Backends = append(cfg.Pool.Backends, BackendConfig{URL: raw, Weight: 1})
				}
			}
		case "algorithm":
			cfg.Pool.Algorithm = algorithm
		case "health-path":
			cfg.HealthCheck.Path = healthPath
		case "health-interval":
			cfg.HealthCheck.Interval = healthInterval
		case "health-timeout":
			cfg.HealthCheck.Timeout = healthTimeout
		case "admin-port":
			cfg.Admin.Port = adminPort
		case "rl-enabled":
			cfg.RateLimit.Enabled = rlEnabled
		case "rl-rps":
			cfg.RateLimit.RequestsPerSecond = rlRPS
		case "rl-burst":
			cfg.RateLimit.Burst = rlBurst
		case "rl-per-ip":
			cfg.RateLimit.PerIP = rlPerIP
		case "metrics-enabled":
			cfg.Metrics.Enabled = metricsEnabled
		case "metrics-port":
			cfg.Metrics.Port = metricsPort
		case "cb-enabled":
			cfg.CircuitBreaker.Enabled = cbEnabled
		case "cb-failure-threshold":
			cfg.CircuitBreaker.FailureThreshold = cbFailureThreshold
		case "cb-success-threshold":
			cfg.CircuitBreaker.SuccessThreshold = cbSuccessThreshold
		case "cb-timeout":
			cfg.CircuitBreaker.Timeout = cbTimeout
		case "tls-enabled":
			cfg.TLS.Enabled = tlsEnabled
		case "tls-cert":
			cfg.TLS.CertFile = tlsCert
		case "tls-key":
			cfg.TLS.KeyFile = tlsKey
		case "tls-min-version":
			cfg.TLS.MinVersion = tlsMinVersion
		case "sticky-enabled":
			cfg.StickySession.Enabled = stickyEnabled
		case "sticky-cookie-name":
			cfg.StickySession.CookieName = stickyCookieName
		case "sticky-ttl":
			cfg.StickySession.TTL = stickyTTL
		}
	})
	return nil
}
