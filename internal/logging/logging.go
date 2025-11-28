// Package logging provides logging functionality.
package logging

import (
	"context"
	"log/slog"
	"os"
)

// Logger is a logging implementation.
type Logger struct {
	logger *slog.Logger
}

// Attr is a logging attribute.
type Attr = slog.Attr

// NewLogger creates a new Logger.
func NewLogger() *Logger {
	return &Logger{slog.New(slog.NewTextHandler(os.Stdout, nil))}
}

// Info logs an info message.
func (l *Logger) Info(ctx context.Context, msg string, attrs ...Attr) {
	args := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		args = append(args, attr)
	}
	l.logger.InfoContext(ctx, msg, args...)
}

// Error logs an error message.
func (l *Logger) Error(ctx context.Context, msg string, attrs ...Attr) {
	args := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		args = append(args, attr)
	}
	l.logger.ErrorContext(ctx, msg, args...)
}

// Debug logs a debug message.
func (l *Logger) Debug(ctx context.Context, msg string, attrs ...Attr) {
	args := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		args = append(args, attr)
	}
	l.logger.DebugContext(ctx, msg, args...)
}

// String creates a new attribute with the given key and value.
func String(key, value string) Attr {
	return slog.String(key, value)
}

// ErrAttr creates a new attribute with the key "err" and the given error value.
func ErrAttr(value error) Attr { return slog.Any("err", value) }
