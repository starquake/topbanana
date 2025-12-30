// Package dbtest provides helpers for testing database code.
package dbtest

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/pressly/goose/v3"
)

// SetupTestDB creates a temporary SQLite database for testing and returns its DSN and a cleanup function.
func SetupTestDB(t *testing.T) (string, func()) {
	t.Helper()

	tmpDB, err := os.CreateTemp(t.TempDir(), "topbanana-test-*.sqlite")
	if err != nil {
		t.Fatalf("failed to create temp db: %v", err)
	}
	tmpDBPath := tmpDB.Name()
	err = tmpDB.Close()
	if err != nil {
		t.Fatalf("failed to close temp db: %v", err)
	}

	cleanup := func() {
		err = os.Remove(tmpDBPath)
		if err != nil {
			t.Errorf("failed to remove temp db: %s", err)
		}
	}

	dsn := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)",
		tmpDBPath,
	)

	return dsn, cleanup
}

// Open opens a database connection with migrations applied.
func Open(t *testing.T) *sql.DB {
	t.Helper()

	db := OpenUnmigrated(t)

	err := goose.Up(db, ".")
	if err != nil {
		t.Fatalf("error running migrations: %v", err)
	}

	return db
}

// OpenUnmigrated opens a database connection without migrations applied.
func OpenUnmigrated(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("error opening SQLite database: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), "PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("error enabling foreign keys: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	return db
}
