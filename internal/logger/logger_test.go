package logger_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/VijayGohel/go-lb/internal/logger"
)

func TestJSONHandler_OutputsValidJSON(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	l.Info("request",
		"request_id", "abc123",
		"backend", "http://localhost:8081",
		"latency_ms", int64(12),
		"status", 200,
	)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("logger output is not valid JSON: %v\noutput: %s", err, buf.String())
	}

	for _, field := range []string{"time", "level", "msg", "request_id", "backend", "latency_ms", "status"} {
		if _, ok := entry[field]; !ok {
			t.Errorf("log entry missing field: %s", field)
		}
	}
	if entry["request_id"] != "abc123" {
		t.Errorf("request_id = %v, want abc123", entry["request_id"])
	}
}

func TestInit_SetsGlobalDefault(t *testing.T) {
	logger.Init()
	slog.Info("test message", "key", "value") // must not panic
}
