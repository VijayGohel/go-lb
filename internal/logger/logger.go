package logger

import (
	"log/slog"
	"os"
)

// Init sets up a JSON structured logger writing to stdout
// and registers it as the slog default. Call once at program start.
func Init() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))
}
