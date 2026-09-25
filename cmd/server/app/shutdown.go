package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/starquake/topbanana/internal/bgtasks"
	"github.com/starquake/topbanana/internal/request"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	// idleTimeout caps how long a keep-alive connection can sit unused before the
	// server closes it. Without this, idle connections behind a pooled proxy or
	// CDN linger indefinitely and leak file descriptors. 120s is the conventional
	// upper bound; long enough for legitimate keep-alive reuse, short enough to
	// reclaim sockets from stale clients.
	idleTimeout     = 120 * time.Second
	shutdownTimeout = 5 * time.Second
)

func runHTTPServer(
	ctx, signalCtx context.Context,
	ln net.Listener,
	srv http.Handler,
	emailTasks *bgtasks.Tracker,
	logger *slog.Logger,
	writeTimeout time.Duration,
) error {
	// Cancelled as shutdown begins so long-lived streams end instead of pinning
	// Shutdown for its whole timeout; see request.StreamContext.
	shuttingDown, beginShutdown := context.WithCancel(context.WithoutCancel(ctx))
	defer beginShutdown()
	baseCtx := request.WithShutdown(context.WithoutCancel(ctx), shuttingDown)
	httpServer := &http.Server{
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		Handler:           srv,
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
	}

	g, gCtx := errgroup.WithContext(signalCtx)

	g.Go(func() error {
		logger.InfoContext(gCtx, "listening on "+ln.Addr().String(), slog.String("addr", ln.Addr().String()))
		addr := ln.Addr().String()
		logger.InfoContext(gCtx, fmt.Sprintf("visit http://%s/admin to manage quizzes", addr))
		logger.InfoContext(gCtx, fmt.Sprintf("visit http://%s/ to play", addr))
		httpErr := httpServer.Serve(ln)
		if httpErr != nil && !errors.Is(httpErr, http.ErrServerClosed) {
			msg := "error listening and serving"
			logger.ErrorContext(signalCtx, msg, slog.Any("err", httpErr))

			return fmt.Errorf("%s: %w", msg, httpErr)
		}

		return nil
	})

	g.Go(func() error {
		<-gCtx.Done()
		beginShutdown()

		return shutdown(ctx, httpServer, emailTasks, logger)
	})

	err := g.Wait()
	if err != nil {
		return fmt.Errorf("error running server: %w", err)
	}

	return nil
}

// shutdown stops the HTTP server and drains the detached email dispatches
// concurrently, so the two bounds overlap rather than add up against the
// container's stop grace. It returns only once both are done, so the caller
// can close the DB after it (#740, #741, #1351).
//
// Both bounds are detached from ctx: at shutdown ctx is already cancelled
// (signal-driven, and the integration harness cancels the ctx it passes to
// Run), so a plain WithTimeout(ctx, ...) would fire instantly.
func shutdown(ctx context.Context, httpServer *http.Server, emailTasks *bgtasks.Tracker, logger *slog.Logger) error {
	boundCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		if drainErr := emailTasks.Wait(boundCtx); drainErr != nil {
			logger.WarnContext(ctx, "gave up waiting for background email dispatches", slog.Any("err", drainErr))
		}
	}()
	shutdownErr := httpServer.Shutdown(boundCtx)
	<-drained
	if shutdownErr != nil {
		logger.ErrorContext(ctx, "error shutting down server", slog.Any("err", shutdownErr))

		return fmt.Errorf("error shutting down server: %w", shutdownErr)
	}

	return nil
}
