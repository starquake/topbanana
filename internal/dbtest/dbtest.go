// Package dbtest provides helpers for testing database code.
package dbtest

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

const (
	maxOpenConns    = 1
	maxIdleConns    = 1
	connMaxLifetime = 5 * time.Minute
)

// SetupTestDB creates a temporary SQLite database for testing and returns its DSN and a cleanup function.
func SetupTestDB(t *testing.T) (string, func()) {
	t.Helper()

	if testing.Short() {
		t.Skip("integration: needs a real database")
	}

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
		"file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_txlock=immediate",
		tmpDBPath,
	)

	return dsn, cleanup
}

// templateOnce ensures the migrated template DB is built exactly once per
// process. templateBytes holds the migrated SQLite file contents; every Open
// call writes a per-test copy of these bytes and opens SQLite against the
// copy, so each test still gets a fully isolated database but the ~70
// migrations only run once instead of per call site.
//
//nolint:gochecknoglobals // process-wide cache of the migrated test schema; shared by every Open caller by design.
var (
	templateOnce  sync.Once
	templateBytes []byte
	templateErr   error
)

// buildTemplate runs every migration against a fresh on-disk SQLite database
// and snapshots the result into templateBytes. It runs at most once per process
// (guarded by templateOnce); any failure is stored in templateErr for Open to
// surface on the first call site that triggered the build.
func buildTemplate() {
	dir, err := os.MkdirTemp("", "topbanana-dbtest-template-")
	if err != nil {
		templateErr = fmt.Errorf("create template dir: %w", err)

		return
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "template.sqlite")

	dsn := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_txlock=immediate",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		templateErr = fmt.Errorf("open template db: %w", err)

		return
	}
	defer db.Close()

	if err = goose.Up(db, "."); err != nil {
		templateErr = fmt.Errorf("run migrations on template db: %w", err)

		return
	}

	// Close so WAL/SHM contents are flushed back into the main file before we
	// read it; otherwise the snapshot can miss recently-written pages.
	if err = db.Close(); err != nil {
		templateErr = fmt.Errorf("close template db: %w", err)

		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		templateErr = fmt.Errorf("read template db: %w", err)

		return
	}
	templateBytes = data
}

// Open opens a database connection with migrations applied. The migrated
// schema is cached process-wide, so the first call runs the migrations and
// every later call clones the cached bytes into a per-test file - each test
// still gets an isolated database, but the ~70 migrations run only once.
func Open(t *testing.T) *sql.DB {
	t.Helper()

	if testing.Short() {
		t.Skip("integration: needs a real database")
	}

	templateOnce.Do(buildTemplate)
	if templateErr != nil {
		t.Fatalf("build migrated template db: %v", templateErr)
	}

	path := filepath.Join(t.TempDir(), "test.sqlite")
	if err := os.WriteFile(path, templateBytes, 0o600); err != nil {
		t.Fatalf("write per-test db file: %v", err)
	}

	dsn := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_txlock=immediate",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("error opening SQLite database: %v", err)
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxLifetime(connMaxLifetime)

	return db
}

// OpenUnmigrated opens a database connection without migrations applied. Used
// by the migrations package's own tests, which need to drive goose themselves.
func OpenUnmigrated(t *testing.T) *sql.DB {
	t.Helper()

	if testing.Short() {
		t.Skip("integration: needs a real database")
	}

	db, err := sql.Open(
		"sqlite",
		":memory:?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_txlock=immediate",
	)
	if err != nil {
		t.Fatalf("error opening SQLite database: %v", err)
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxLifetime(connMaxLifetime)

	return db
}
