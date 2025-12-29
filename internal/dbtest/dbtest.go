// Package dbtest provides helpers for testing database code.
package dbtest

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"
)

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
