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
	Server      ServerConfig
	Pool        PoolConfig
	HealthCheck HealthCheckConfig
	Admin       AdminConfig
	RateLimit   RateLimitConfig
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
	return nil
}

func applyCLI(cfg *Config, args []string) error {
	fs := flag.NewFlagSet("golb", flag.ContinueOnError)

	var (
		port           int
		backendsRaw    string
		algorithm      string
		healthPath     string
		healthInterval time.Duration
		healthTimeout  time.Duration
		adminPort      int
		rlEnabled      bool
		rlRPS          float64
		rlBurst        int
		rlPerIP        bool
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
		}
	})
	return nil
}
