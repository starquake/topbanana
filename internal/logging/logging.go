// Package logging provides logging functionality.
package logging

import (
	"context"
	"io"
	"log/slog"
)

// Logger is a logging implementation.
type Logger struct {
	logger *slog.Logger
}

// Attr is a logging attribute.
type Attr = slog.Attr

// Level is a logging level.
type Level = slog.Level

// A Leveler provides a logging level.
type Leveler = slog.Leveler

const (
	// LevelDebug is used for a debug level log message.
	LevelDebug = slog.LevelDebug
	// LevelInfo is used for an info level log message.
	LevelInfo = slog.LevelInfo
	// LevelWarn is used for a warning level log message.
	LevelWarn = slog.LevelWarn
	// LevelError is used for an error level log message.
	LevelError = slog.LevelError
)

// NewLogger creates a new Logger with the default level.
func NewLogger(w io.Writer) *Logger {
	return &Logger{slog.New(slog.NewTextHandler(w, nil))}
}

// NewLoggerWithLevel creates a new Logger with the given level.
func NewLoggerWithLevel(w io.Writer, l Leveler) *Logger {
	opts := &slog.HandlerOptions{Level: l}

	return &Logger{slog.New(slog.NewTextHandler(w, opts))}
}

// Debug logs a debug message.
func (l *Logger) Debug(ctx context.Context, msg string, attrs ...Attr) {
	args := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		args = append(args, attr)
	}
	l.logger.DebugContext(ctx, msg, args...)
}

// Info logs an info message.
func (l *Logger) Info(ctx context.Context, msg string, attrs ...Attr) {
	args := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		args = append(args, attr)
	}
	l.logger.InfoContext(ctx, msg, args...)
}

// Warn logs a warning message.
func (l *Logger) Warn(ctx context.Context, msg string, attrs ...Attr) {
	args := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		args = append(args, attr)
	}
	l.logger.WarnContext(ctx, msg, args...)
}

// Error logs an error message.
func (l *Logger) Error(ctx context.Context, msg string, attrs ...Attr) {
	args := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		args = append(args, attr)
	}
	l.logger.ErrorContext(ctx, msg, args...)
}

// String creates a new attribute with the given key and value.
func String(key, value string) Attr {
	return slog.String(key, value)
}

// ErrAttr creates a new attribute with the key "err" and the given error value.
func ErrAttr(value error) Attr { return slog.Any("err", value) }
