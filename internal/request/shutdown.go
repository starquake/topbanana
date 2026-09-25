package request

import "context"

type shutdownKey struct{}

// WithShutdown returns a copy of ctx carrying shutdown, a context the server
// cancels when it begins a graceful shutdown.
func WithShutdown(ctx, shutdown context.Context) context.Context {
	return context.WithValue(ctx, shutdownKey{}, shutdown)
}

// StreamContext derives the context a long-lived response should run under from
// its request context: it ends when the client disconnects or when the server
// begins shutting down, since [http.Server.Shutdown] waits for handlers but
// never cancels them.
func StreamContext(reqCtx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(reqCtx)
	shutdown, ok := reqCtx.Value(shutdownKey{}).(context.Context)
	if !ok {
		return ctx, cancel
	}
	stop := context.AfterFunc(shutdown, cancel)

	return ctx, func() {
		stop()
		cancel()
	}
}
