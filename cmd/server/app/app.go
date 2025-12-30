// Package app contains the main entrypoint for the server.
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/db"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/server"
	"github.com/starquake/topbanana/internal/store"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 10 * time.Second
	shutdownTimeout   = 5 * time.Second
)

// Run starts the application server, connects to the database, runs migrations, and listens for incoming requests.
func Run(
	ctx context.Context,
	getenv func(string) string,
	stdout io.Writer,
	ln net.Listener,
) error {
	var err error
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	logger := slog.New(slog.NewTextHandler(stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg, err := config.Parse(getenv)
	if err != nil {
		msg := "error parsing config"
		logger.ErrorContext(signalCtx, msg, slog.Any("err", err))

		return fmt.Errorf("%s: %w", msg, err)
	}

	conn, err := setupDB(signalCtx, cfg, logger)
	if err != nil {
		return err
	}
	defer func() {
		conErr := conn.Close()
		if conErr != nil {
			logger.ErrorContext(signalCtx, "error closing database connection", slog.Any("err", conErr))
		}
	}()

	srv := server.NewServer(logger, &store.Stores{
		Quizzes: quiz.NewSQLiteStore(conn, logger),
	})
	if ln == nil {
		ln, err = listener(signalCtx, cfg, logger)
		if err != nil {
			return fmt.Errorf("error creating listener: %w", err)
		}
	} else {
		logger.InfoContext(signalCtx, "listener overridden")
	}

	return runHTTPServer(ctx, signalCtx, ln, srv, logger)
}

func runHTTPServer(ctx, signalCtx context.Context, ln net.Listener, srv http.Handler, logger *slog.Logger) error {
	httpServer := &http.Server{
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		Handler:           srv,
	}

	g, gCtx := errgroup.WithContext(signalCtx)

	g.Go(func() error {
		logger.InfoContext(gCtx, "listening on "+ln.Addr().String(), slog.String("addr", ln.Addr().String()))
		logger.InfoContext(gCtx, fmt.Sprintf("visit http://%s/admin/quizzes to manage quizzes", ln.Addr().String()))
		httpErr := httpServer.Serve(ln)
		if httpErr != nil && !errors.Is(httpErr, http.ErrServerClosed) {
			msg := "error listening and serving"
			logger.ErrorContext(signalCtx, msg, slog.Any("err", httpErr))

			return fmt.Errorf("%v: %w", msg, httpErr)
		}

		return nil
	})

	g.Go(func() error {
		<-gCtx.Done()
		// make a new context for the Shutdown
		// use the root ctx to ensure shutdown has its own timeout even though signalCtx is already canceled
		shutdownCtx, shutdownCancel := context.WithTimeout(ctx, shutdownTimeout)
		defer shutdownCancel()
		if shutdownErr := httpServer.Shutdown(shutdownCtx); shutdownErr != nil {
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

func setupDB(signalCtx context.Context, cfg *config.Config, logger *slog.Logger) (*sql.DB, error) {
	conn, err := db.Open(
		signalCtx,
		cfg.DBDriver,
		cfg.DBURI,
		cfg.DBMaxOpenConns,
		cfg.DBMaxIdleConns,
		cfg.DBConnMaxLifetime,
	)
	if err != nil {
		logger.ErrorContext(signalCtx, "error opening database connection", slog.Any("err", err))

		return nil, fmt.Errorf("error opening database connection: %w", err)
	}

	if err = db.Migrate(conn, cfg.DBDriver); err != nil {
		msg := "error migrating database"
		logger.ErrorContext(signalCtx, msg, slog.Any("err", err))

		return nil, fmt.Errorf("%s: %w", msg, err)
	}

	return conn, nil
}

func listener(ctx context.Context, cfg *config.Config, logger *slog.Logger) (net.Listener, error) {
	logger.InfoContext(ctx, "creating listener based on config")
	listenConfig := &net.ListenConfig{}
	ln, err := listenConfig.Listen(ctx, "tcp", net.JoinHostPort(cfg.Host, cfg.Port))
	if err != nil {
		logger.ErrorContext(ctx, "error listening on "+cfg.Host+":"+cfg.Port, slog.Any("err", err))

		return nil, fmt.Errorf("error listening on %s:%s: %w", cfg.Host, cfg.Port, err)
	}

	return ln, nil
}
