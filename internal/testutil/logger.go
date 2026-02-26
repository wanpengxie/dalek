package testutil

import (
	"bytes"
	"io"
	"log/slog"
)

// NewSlogBufferLogger creates a JSON slog logger writing to an in-memory buffer.
func NewSlogBufferLogger(level slog.Level) (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: level,
	}))
	return logger, &buf
}

// NewDiscardSlogLogger creates a slog logger that drops all logs.
func NewDiscardSlogLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
