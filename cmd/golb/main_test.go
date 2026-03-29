package main

import (
	"testing"
)

func TestParseFlags_DefaultPort(t *testing.T) {
	cfg := parseFlags([]string{"--backends=http://localhost:8081"})
	if cfg.port != 3030 {
		t.Errorf("default port = %d, want 3030", cfg.port)
	}
}

func TestParseFlags_DefaultHealthPath(t *testing.T) {
	cfg := parseFlags([]string{"--backends=http://localhost:8081"})
	if cfg.healthPath != "/health" {
		t.Errorf("default healthPath = %q, want /health", cfg.healthPath)
	}
}

func TestParseFlags_CustomPort(t *testing.T) {
	cfg := parseFlags([]string{"--backends=http://localhost:8081", "--port=8080"})
	if cfg.port != 8080 {
		t.Errorf("port = %d, want 8080", cfg.port)
	}
}

func TestParseFlags_MultipleBackends(t *testing.T) {
	cfg := parseFlags([]string{"--backends=http://localhost:8081,http://localhost:8082"})
	if len(cfg.backends) != 2 {
		t.Fatalf("backends count = %d, want 2", len(cfg.backends))
	}
}
