package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestJSONHandler_OutputsValidJSON(t *testing.T) {
	var buf bytes.Buffer
	testLogger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	testLogger.Info("request",
		"request_id", "abc123",
		"backend", "http://localhost:8081",
		"latency_ms", int64(12),
		"status", 200,
	)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("logger output is not valid JSON: %v\noutput: %s", err, buf.String())
	}

	required := []string{"time", "level", "msg", "request_id", "backend", "latency_ms", "status"}
	for _, field := range required {
		if _, ok := entry[field]; !ok {
			t.Errorf("log entry missing field: %s\nfull entry: %v", field, entry)
		}
	}

	if entry["request_id"] != "abc123" {
		t.Errorf("request_id = %v, want abc123", entry["request_id"])
	}
	if entry["backend"] != "http://localhost:8081" {
		t.Errorf("backend = %v, want http://localhost:8081", entry["backend"])
	}
}

func TestInitLogger_SetsGlobalDefault(t *testing.T) {
	initLogger()
	// Should not panic
	slog.Info("test message", "key", "value")
}
