package main

import (
	"log/slog"
	"os"
)

// logger is the package-level structured logger. Initialised by initLogger().
var logger *slog.Logger

// initLogger sets up a JSON structured logger writing to stdout
// and registers it as the slog default.
func initLogger() {
	logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)
}
