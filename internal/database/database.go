// Package database provides database access.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/starquake/topbanana/internal/db"
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
	var conn *sql.DB
	conn, err = sql.Open(driver, uri)
	if err != nil {
		return nil, fmt.Errorf("error opening database: %w", err)
	}

	conn.SetMaxOpenConns(dbMaxOpenConns)
	conn.SetMaxIdleConns(dbMaxIdleConns)
	conn.SetConnMaxLifetime(dbConnMaxLifetime)

	return conn, nil
}

// Migrate runs database migrations.
func Migrate(conn *sql.DB) error {
	var err error

	if err = goose.Up(conn, "."); err != nil {
		return fmt.Errorf("error running migrations: %w", err)
	}

	return nil
}

// ExecTx is a helper to run queries within a transaction.
func ExecTx(conn *sql.DB, ctx context.Context, fn func(*db.Queries) error) error {
	var err error
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	q := db.New(tx)
	err = fn(q)
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("transaction failed: %w (rollback error: %w)", err, rbErr)
		}

		return err
	}

	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("transaction failed: %w", err)
	}

	return nil
}
