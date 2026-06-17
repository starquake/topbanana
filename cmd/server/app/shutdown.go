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
	httpServer := &http.Server{
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		Handler:           srv,
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
		// make a new context for the Shutdown
		// use the root ctx to ensure shutdown has its own timeout even though signalCtx is already canceled
		shutdownCtx, shutdownCancel := context.WithTimeout(ctx, shutdownTimeout)
		defer shutdownCancel()
		shutdownErr := httpServer.Shutdown(shutdownCtx)
		// Drain the detached email-dispatch goroutines AFTER Shutdown stops
		// the listener and BEFORE Run's deferred conn.Close runs, so a
		// dispatch can't write to a closed DB (#740, #741). The bound is
		// detached from ctx via WithoutCancel: at shutdown ctx is already
		// cancelled (signal-driven, and the integration harness cancels the
		// same ctx it passes to Run), so a plain WithTimeout(ctx, ...) would
		// fire instantly and skip the wait. The dispatches carry per-send
		// timeouts longer than shutdownTimeout, so a stuck SMTP must not pin
		// shutdown - draining what it can within the bound and giving up is
		// the right trade.
		drainCtx, drainCancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer drainCancel()
		if drainErr := emailTasks.Wait(drainCtx); drainErr != nil {
			logger.WarnContext(ctx, "gave up waiting for background email dispatches", slog.Any("err", drainErr))
		}
		if shutdownErr != nil {
			logger.ErrorContext(shutdownCtx, "error shutting down server", slog.Any("err", shutdownErr))

			return fmt.Errorf("error shutting down server: %w", shutdownErr)
		}

		return nil
	})

	err := g.Wait()
	if err != nil {
		return fmt.Errorf("error running server: %w", err)
	}

	return nil
}
