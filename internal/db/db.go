// Package db provides database access.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/starquake/topbanana/internal/migrations"
)

// SetupGoose configures global settings for goose.
// Used to prevent race conditions.
func SetupGoose() {
	goose.SetBaseFS(migrations.FS)

	if err := goose.SetDialect("sqlite3"); err != nil {
		panic(err)
	}
}

// Open opens a database connection.
func Open(
	_ context.Context,
	driver, uri string,
	dbMaxOpenConns, dbMaxIdleConns int,
	dbConnMaxLifetime time.Duration,
) (*sql.DB, error) {
	var err error
	var db *sql.DB
	db, err = sql.Open(driver, uri)
	if err != nil {
		return nil, fmt.Errorf("error opening database: %w", err)
	}

	db.SetMaxOpenConns(dbMaxOpenConns)
	db.SetMaxIdleConns(dbMaxIdleConns)
	db.SetConnMaxLifetime(dbConnMaxLifetime)

	return db, nil
}

// Migrate runs database migrations.
func Migrate(db *sql.DB) error {
	var err error

	if err = goose.Up(db, "."); err != nil {
		return fmt.Errorf("error running migrations: %w", err)
	}

	return nil
}
