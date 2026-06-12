// Package util provides shared utilities including structured logging.
package util

import (
	"log/slog"
	"os"
)

var (
	// Logger is the shared structured logger for the wukong application.
	// It defaults to JSON format at INFO level for production-friendly
	// observability. CLI mode may override to text format.
	Logger *slog.Logger
)

func init() {
	// Default: text format at INFO level for readability in terminal
	Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// SetDebugMode switches the logger to debug level for verbose output.
func SetDebugMode() {
	Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
}

// SetQuietMode switches the logger to warn level for minimal output.
func SetQuietMode() {
	Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))
}
