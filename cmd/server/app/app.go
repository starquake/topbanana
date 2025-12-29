// Package app contains the main entrypoint for the server.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/db"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/server"
	"github.com/starquake/topbanana/internal/store"
)

const (
	readHeaderTimeout = 5 * time.Second
	shutdownTimeout   = 5 * time.Second
)

// Run starts the application server, connects to the database, runs migrations, and listens for incoming requests.
func Run(
	ctx context.Context,
	getenv func(string) string,
	stdout io.Writer,
) error {
	var err error
	mainCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	logger := slog.New(slog.NewTextHandler(stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var cfg *config.Config
	if cfg, err = config.Parse(getenv); err != nil {
		msg := "error parsing config"
		logger.ErrorContext(ctx, msg, slog.Any("err", err))

		return fmt.Errorf("%s: %w", msg, err)
	}

	conn, err := db.Open(ctx, cfg.DBDriver, cfg.DBURI, cfg.DBMaxOpenConns, cfg.DBMaxIdleConns, cfg.DBConnMaxLifetime)
	if err != nil {
		return fmt.Errorf("error opening database connection: %w", err)
	}
	defer func() {
		conErr := conn.Close()
		if conErr != nil {
			logger.ErrorContext(ctx, "error closing database connection", slog.Any("err", conErr))
		}
	}()

	if err = db.Migrate(conn, cfg.DBDriver); err != nil {
		msg := "error migrating database"
		logger.ErrorContext(ctx, msg, slog.Any("err", err))

		return fmt.Errorf("%s: %w", msg, err)
	}

	quizStore := quiz.NewSQLiteStore(conn, logger)

	stores := &store.Stores{
		Quizzes: quizStore,
	}

	srv := server.NewServer(logger, stores)
	listenConfig := &net.ListenConfig{}
	ln, err := listenConfig.Listen(mainCtx, "tcp", net.JoinHostPort(cfg.Host, cfg.Port))
	if err != nil {
		return fmt.Errorf("error listening on %s:%s: %w", cfg.Host, cfg.Port, err)
	}

	httpServer := &http.Server{
		ReadHeaderTimeout: readHeaderTimeout,
		Handler:           srv,
	}
	go func() {
		logger.InfoContext(ctx, "listening on "+ln.Addr().String(), slog.String("addr", ln.Addr().String()))
		logger.InfoContext(ctx, fmt.Sprintf("visit http://%s/admin/quizzes to manage quizzes", ln.Addr().String()))
		httpErr := httpServer.Serve(ln)
		if httpErr != nil && !errors.Is(httpErr, http.ErrServerClosed) {
			logger.ErrorContext(ctx, "error listening and serving", slog.Any("err", httpErr))
		}
	}()
	var wg sync.WaitGroup
	wg.Go(func() {
		<-mainCtx.Done()
		// make a new context for the Shutdown
		shutdownCtx, shutdownCancel := context.WithTimeout(ctx, shutdownTimeout)
		defer shutdownCancel()
		if shutdownErr := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.ErrorContext(shutdownCtx, "error shutting down server", slog.Any("err", shutdownErr))
		}
	})
	wg.Wait()

	return nil
}
