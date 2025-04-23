package cron

import (
	"io"
	"log/slog"
)

// DefaultLogger is used by Cron if none is specified.
var DefaultLogger Logger = slog.Default()

// DiscardLogger can be used by callers to discard all log messages.
var DiscardLogger Logger = slog.New(slog.NewTextHandler(io.Discard, nil))

// Logger is the interface used in this package for logging, so that any backend can be plugged in.
type Logger interface {
	// Debug logs a debug message with optional key-value pairs
	Debug(msg string, keysAndValues ...any)
	// Info logs an informational message with optional key-value pairs
	Info(msg string, keysAndValues ...any)
	// Warn logs a warning message with optional key-value pairs
	Warn(msg string, keysAndValues ...any)
	// Error logs an error message with optional key-value pairs
	Error(msg string, keysAndValues ...any)
}
