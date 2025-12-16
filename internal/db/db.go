// Package db provides database access.
package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
	"github.com/starquake/topbanana/internal/migrations"
	"github.com/starquake/topbanana/internal/must"
)

// Open opens a database connection.
func Open(ctx context.Context) (*sql.DB, error) {
	db := must.Any(sql.Open("sqlite", "./topbanana.sqlite"))
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		return nil, fmt.Errorf("error enabling foreign keys: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode = WAL;"); err != nil {
		return nil, fmt.Errorf("error enabling WAL journal mode: %w", err)
	}
	goose.SetBaseFS(migrations.FS)
	must.OK(goose.SetDialect("sqlite3"))
	must.OK(goose.Up(db, "."))

	return db, nil
}
