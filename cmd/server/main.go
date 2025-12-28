// Application server is the main server for the application
package main

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

	_ "modernc.org/sqlite"

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

func run(
	ctx context.Context,
	getenv func(string) string,
	stdout io.Writer,
) error {
	var err error
	mainCtx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	logger := slog.New(slog.NewTextHandler(stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var cfg *config.Config
	if cfg, err = config.Parse(getenv); err != nil {
		msg := "error parsing config"
		logger.ErrorContext(mainCtx, msg, slog.Any("err", err))

		return fmt.Errorf("%s: %w", msg, err)
	}

	conn, err := db.Open(
		ctx,
		cfg.DBDriver,
		cfg.DBURI,
		cfg.DBMaxOpenConns,
		cfg.DBMaxIdleConns,
		cfg.DBConnMaxLifetime,
	)
	if err != nil {
		return fmt.Errorf("error opening database connection: %w", err)
	}
	defer func() {
		conErr := conn.Close()
		if conErr != nil {
			logger.ErrorContext(mainCtx, "error closing database connection", slog.Any("err", conErr))
		}
	}()

	if err = db.Migrate(conn, cfg.DBDriver); err != nil {
		msg := "error migrating database"
		logger.ErrorContext(mainCtx, msg, slog.Any("err", err))

		return fmt.Errorf("%s: %w", msg, err)
	}

	quizStore := quiz.NewSQLiteStore(conn, logger)

	stores := &store.Stores{
		Quizzes: quizStore,
	}

	srv := server.NewServer(logger, stores)
	httpServer := &http.Server{
		ReadHeaderTimeout: readHeaderTimeout,
		Addr:              net.JoinHostPort(cfg.Host, cfg.Port),
		Handler:           srv,
	}
	go func() {
		logger.InfoContext(mainCtx, "listening on "+httpServer.Addr, slog.String("addr", httpServer.Addr))
		logger.InfoContext(mainCtx, fmt.Sprintf("visit http://%s/admin/quizzes to manage quizzes", httpServer.Addr))
		httpErr := httpServer.ListenAndServe()
		if httpErr != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ErrorContext(mainCtx, "error listening and serving", slog.Any("err", httpErr))
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

func main() {
	ctx := context.Background()
	if err := run(ctx, os.Getenv, os.Stdout); err != nil {
		if _, err2 := fmt.Fprintf(os.Stderr, "error: %v\n", err); err2 != nil {
			panic(err2)
		}

		os.Exit(1)
	}
}
