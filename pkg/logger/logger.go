package logger

import (
	"log/slog"
	"os"
)

// New creates a structured JSON logger writing to stdout.
// In production use JSON format; in development use human-readable text.
func New(env string) *slog.Logger {
	var handler slog.Handler

	opts := &slog.HandlerOptions{Level: slog.LevelDebug}

	if env == "production" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}
