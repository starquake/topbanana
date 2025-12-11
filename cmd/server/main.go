// Application server is the main server for the application
package main

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
	"sync"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/starquake/topbanana/internal/logging"
	"github.com/starquake/topbanana/internal/migrations"
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

	logger := logging.NewLogger(stdout)

	db := must.Any(sql.Open("sqlite", "./topbanana.sqlite"))
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		return fmt.Errorf("error enabling foreign keys: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode = WAL;"); err != nil {
		return fmt.Errorf("error enabling WAL journal mode: %w", err)
	}
	goose.SetBaseFS(migrations.FS)
	must.OK(goose.SetDialect("sqlite3"))
	must.OK(goose.Up(db, "."))

	quizStore := quiz.NewSQLiteStore(db, logger)

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
		logger.Info(mainCtx, "listening on "+httpServer.Addr, slog.String("addr", httpServer.Addr))
		err := httpServer.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error(mainCtx, "error listening and serving", logging.ErrAttr(err))
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
