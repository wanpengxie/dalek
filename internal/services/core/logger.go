package core

import (
	"io"
	"log/slog"
	"os"
)

// DefaultLogger returns the default structured logger used by services/runtime.
func DefaultLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// DiscardLogger returns a logger that discards all logs.
func DiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// EnsureLogger guarantees a non-nil logger.
func EnsureLogger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return DiscardLogger()
}
