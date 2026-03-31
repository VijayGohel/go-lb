package main

import (
	"testing"

	"github.com/VijayGohel/go-lb/internal/config"
)

func TestMain_ConfigDefaults(t *testing.T) {
	cfg := config.Defaults()
	if cfg.Server.Port != 3030 {
		t.Errorf("default port = %d, want 3030", cfg.Server.Port)
	}
	if cfg.Pool.Algorithm != "round_robin" {
		t.Errorf("default algorithm = %q, want round_robin", cfg.Pool.Algorithm)
	}
}

func TestMain_ConfigLoadCLI(t *testing.T) {
	cfg, err := config.Load("", []string{"--port=8080", "--backends=http://localhost:8081"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("port = %d, want 8080", cfg.Server.Port)
	}
	if len(cfg.Pool.Backends) != 1 {
		t.Fatalf("backends = %d, want 1", len(cfg.Pool.Backends))
	}
	if cfg.Pool.Backends[0].URL != "http://localhost:8081" {
		t.Errorf("backend url = %q, want http://localhost:8081", cfg.Pool.Backends[0].URL)
	}
}

func TestMain_ConfigAlgorithm(t *testing.T) {
	cfg, err := config.Load("", []string{"--backends=http://localhost:8081", "--algorithm=least_connections"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Pool.Algorithm != "least_connections" {
		t.Errorf("algorithm = %q, want least_connections", cfg.Pool.Algorithm)
	}
}

func TestExtractConfigFlag_SpaceForm(t *testing.T) {
	path, rest, err := extractConfigFlag([]string{"--config", "/etc/golb.yaml", "--port=8080"})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/etc/golb.yaml" {
		t.Errorf("path = %q, want /etc/golb.yaml", path)
	}
	if len(rest) != 1 || rest[0] != "--port=8080" {
		t.Errorf("rest = %v, want [--port=8080]", rest)
	}
}

func TestExtractConfigFlag_EqualForm(t *testing.T) {
	path, rest, err := extractConfigFlag([]string{"--config=/etc/golb.yaml", "--port=8080"})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/etc/golb.yaml" {
		t.Errorf("path = %q, want /etc/golb.yaml", path)
	}
	if len(rest) != 1 || rest[0] != "--port=8080" {
		t.Errorf("rest = %v, want [--port=8080]", rest)
	}
}

func TestExtractConfigFlag_MissingValue_ReturnsError(t *testing.T) {
	_, _, err := extractConfigFlag([]string{"--config"})
	if err == nil {
		t.Fatal("expected error when --config has no value")
	}
}

func TestExtractConfigFlag_EmptyValue_ReturnsError(t *testing.T) {
	_, _, err := extractConfigFlag([]string{"--config="})
	if err == nil {
		t.Fatal("expected error when --config= has empty value")
	}
}

func TestExtractConfigFlag_Absent(t *testing.T) {
	path, rest, err := extractConfigFlag([]string{"--port=8080"})
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Errorf("path = %q, want empty", path)
	}
	if len(rest) != 1 || rest[0] != "--port=8080" {
		t.Errorf("rest = %v, want [--port=8080]", rest)
	}
}
