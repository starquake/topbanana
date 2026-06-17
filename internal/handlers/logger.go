package handlers

import (
	"context"
	"log/slog"
)

// loggerCtxKey is the unexported context-key type for the request-scoped
// logger. A struct (not a string) so no other package can collide with it.
type loggerCtxKey struct{}

// WithLogger returns a copy of ctx carrying logger as the request-scoped
// logger. A middleware binds the stable correlation attrs (a request id, the
// player once known) on logger via [slog.Logger.With] and stashes it here, so
// every downstream line inherits them without restating them.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerCtxKey{}, logger)
}

// LoggerFromContext returns the request-scoped logger stashed by WithLogger,
// or [slog.Default] for a context that was never annotated (a handler invoked
// outside the middleware chain, e.g. in a unit test).
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerCtxKey{}).(*slog.Logger); ok {
		return l
	}

	return slog.Default()
}
