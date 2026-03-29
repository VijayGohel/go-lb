package main

import (
	"log/slog"
	"os"
)

// logger is the package-level structured logger. Defaults to slog.Default();
// call initLogger() to switch to a JSON handler writing to stdout.
var logger = slog.Default()

// initLogger sets up a JSON structured logger writing to stdout
// and registers it as the slog default.
func initLogger() {
	logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)
}
