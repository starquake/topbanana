package db_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/starquake/topbanana/internal/db"
	_ "modernc.org/sqlite"
)

func TestOpen(t *testing.T) {
	t.Parallel()
	// The function hardcodes ./topbanana.sqlite, so we ensure it's cleaned up
	// This is a bit ugly but it will be improved when configuration is added.
	const dbFile = "./topbanana.sqlite"
	t.Cleanup(func() {
		_ = os.Remove(dbFile)
		_ = os.Remove(dbFile + "-shm")
		_ = os.Remove(dbFile + "-wal")
	})

	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		database, err := db.Open(ctx)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if database == nil {
			t.Fatal("expected database connection, got nil")
		}
		defer func(database *sql.DB) {
			err := database.Close()
			if err != nil {
				t.Errorf("failed to close database: %v", err)
			}
		}(database)

		// Verify connection is alive
		if err := database.PingContext(ctx); err != nil {
			t.Errorf("failed to ping database: %v", err)
		}
	})

	t.Run("context canceled", func(t *testing.T) {
		t.Parallel()
		// Clean up from previous run if necessary
		_ = os.Remove(dbFile)

		cancelCtx, cancel := context.WithCancel(ctx)
		cancel() // Cancel immediately

		database, err := db.Open(cancelCtx)
		if err == nil {
			if database != nil {
				err := database.Close()
				if err != nil {
					t.Fatalf("failed to close database: %v", err)
				}
			}
			t.Fatal("expected error due to canceled context, got nil")
		}
	})
}
