// Application server is the main server for the application
package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/starquake/topbanana/internal/logging"
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
) error {
	mainCtx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	logger := logging.NewLogger()
	db := must.Any(sql.Open("sqlite", "./topbanana.sqlite"))
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
			logger.Error(mainCtx, "error listening and serving", logging.Error("err", err))
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
	must.OK(run(ctx))
}
