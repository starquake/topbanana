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

	"github.com/starquake/topbanana/internal/db"
	"github.com/starquake/topbanana/internal/must"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/server"
	"github.com/starquake/topbanana/internal/store"
	_ "modernc.org/sqlite"
)

const (
	readHeaderTimeout = 5 * time.Second
	shutdownTimeout   = 5 * time.Second
)

func run(
	ctx context.Context,
	stdout io.Writer,
) error {
	mainCtx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	logger := slog.New(slog.NewTextHandler(stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	conn, err := db.Open(ctx)
	if err != nil {
		return fmt.Errorf("error opening database connection: %w", err)
	}
	defer func() {
		err := conn.Close()
		if err != nil {
			logger.ErrorContext(mainCtx, "error closing database connection", slog.Any("err", err))
		}
	}()

	quizStore := quiz.NewSQLiteStore(conn, logger)

	stores := &store.Stores{
		Quizzes: quizStore,
	}

	srv := server.NewServer(logger, stores)
	httpServer := &http.Server{
		ReadHeaderTimeout: readHeaderTimeout,
		Addr:              net.JoinHostPort("0.0.0.0", "8080"),
		Handler:           srv,
	}
	go func() {
		logger.InfoContext(mainCtx, "listening on "+httpServer.Addr, slog.String("addr", httpServer.Addr))
		err := httpServer.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ErrorContext(mainCtx, "error listening and serving", slog.Any("err", err))
		}
	}()
	var wg sync.WaitGroup
	wg.Go(func() {
		<-mainCtx.Done()
		// make a new context for the Shutdown
		shutdownCtx, cancel := context.WithTimeout(ctx, shutdownTimeout)
		defer cancel()
		must.OK(httpServer.Shutdown(shutdownCtx))
	})
	wg.Wait()

	return nil
}

func main() {
	ctx := context.Background()
	must.OK(run(ctx, os.Stdout))
}
