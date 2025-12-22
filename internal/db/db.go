// Package db provides database access.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/starquake/topbanana/internal/migrations"
)

// ErrUnsupportedDriver is returned when the database driver is not supported. We only support sqlite for now.
var ErrUnsupportedDriver = errors.New("unsupported database driver")

// Open opens a database connection.
func Open(
	ctx context.Context,
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

	if err = db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("error pinging database: %w", err)
	}

	db.SetMaxOpenConns(dbMaxOpenConns)
	db.SetMaxIdleConns(dbMaxIdleConns)
	db.SetConnMaxLifetime(dbConnMaxLifetime)

	return db, nil
}

// Migrate runs database migrations.
func Migrate(db *sql.DB, dbDriver string) error {
	var err error

	goose.SetBaseFS(migrations.FS)

	var dialect string
	switch dbDriver {
	case "sqlite", "sqlite3":
		dialect = "sqlite3"
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedDriver, dbDriver)
	}
	if err = goose.SetDialect(dialect); err != nil {
		return fmt.Errorf("error setting dialect: %w", err)
	}
	if err = goose.Up(db, "."); err != nil {
		return fmt.Errorf("error running migrations: %w", err)
	}

	return nil
}
