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
	}
}

// fileConfig mirrors Config for YAML unmarshalling using string durations.
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
		Port int `yaml:"port"`
	} `yaml:"admin"`
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
		applyFileConfig(&cfg, &fc)
	}

	if err := applyCLI(&cfg, args); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyFileConfig(cfg *Config, fc *fileConfig) {
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
		if d, err := time.ParseDuration(fc.HealthCheck.Interval); err == nil {
			cfg.HealthCheck.Interval = d
		}
	}
	if fc.HealthCheck.Timeout != "" {
		if d, err := time.ParseDuration(fc.HealthCheck.Timeout); err == nil {
			cfg.HealthCheck.Timeout = d
		}
	}
	if fc.HealthCheck.UnhealthyThreshold != 0 {
		cfg.HealthCheck.UnhealthyThreshold = fc.HealthCheck.UnhealthyThreshold
	}
	if fc.HealthCheck.HealthyThreshold != 0 {
		cfg.HealthCheck.HealthyThreshold = fc.HealthCheck.HealthyThreshold
	}
	if fc.Admin.Port != 0 {
		cfg.Admin.Port = fc.Admin.Port
	}
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
	)

	fs.IntVar(&port, "port", 0, "Port to listen on")
	fs.StringVar(&backendsRaw, "backends", "", "Comma-separated backend URLs (weight=1)")
	fs.StringVar(&algorithm, "algorithm", "", "Algorithm: round_robin|least_connections|weighted_round_robin")
	fs.StringVar(&healthPath, "health-path", "", "Health check path")
	fs.DurationVar(&healthInterval, "health-interval", 0, "Health check interval")
	fs.DurationVar(&healthTimeout, "health-timeout", 0, "Health check timeout")
	fs.IntVar(&adminPort, "admin-port", 0, "Admin server port (0=disabled)")

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
		}
	})
	return nil
}
