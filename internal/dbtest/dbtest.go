// Package dbtest provides helpers for testing database code.
package dbtest

import (
	"context"
	"database/sql"
	"errors"
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

// SetupTestDB returns a DSN to a per-test SQLite database file that already
// has every migration applied, plus a cleanup that removes the file. The
// migrated schema is built once per process via [buildTemplate] and the
// per-test file is a byte-for-byte copy of that template, so a [database.Migrate]
// call against the returned DSN finds nothing to run and returns in
// sub-millisecond time. Each test still gets a fully isolated database.
func SetupTestDB(t *testing.T) (string, func()) {
	t.Helper()

	if testing.Short() {
		t.Skip("integration: needs a real database")
	}

	templateOnce.Do(buildTemplate)
	if templateErr != nil {
		t.Fatalf("build migrated template db: %v", templateErr)
	}

	tmpDB, err := os.CreateTemp(t.TempDir(), "topbanana-test-*.sqlite")
	if err != nil {
		t.Fatalf("failed to create temp db: %v", err)
	}
	tmpDBPath := tmpDB.Name()
	if err = tmpDB.Close(); err != nil {
		t.Fatalf("failed to close temp db: %v", err)
	}
	if err = os.WriteFile(tmpDBPath, templateBytes, 0o600); err != nil {
		t.Fatalf("failed to seed temp db from template: %v", err)
	}

	cleanup := func() {
		if rerr := os.Remove(tmpDBPath); rerr != nil {
			t.Errorf("failed to remove temp db: %s", rerr)
		}
	}

	dsn := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_txlock=immediate",
		tmpDBPath,
	)

	return dsn, cleanup
}

// templateOnce ensures the migrated template DB is built exactly once per
// process. templateBytes holds the migrated SQLite file contents; SetupTestDB
// and Open both write a per-test copy of these bytes and open SQLite against
// the copy, so each test still gets a fully isolated database but the ~70
// migrations only run once instead of per call site.
//
//nolint:gochecknoglobals // process-wide cache of the migrated test schema; shared by every caller by design.
var (
	templateOnce  sync.Once
	templateBytes []byte
	templateErr   error
)

// buildTemplate runs every migration against a fresh on-disk SQLite database
// and snapshots the result into templateBytes. It runs at most once per process
// (guarded by templateOnce); any failure is stored in templateErr for the
// triggering call site (SetupTestDB or Open) to surface.
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
	db.SetMaxOpenConns(1)

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

// UnmigratedDSN returns a DSN for an empty on-disk SQLite database with no
// migrations applied, for tests that migrate a file database themselves.
func UnmigratedDSN(t *testing.T) string {
	t.Helper()

	if testing.Short() {
		t.Skip("integration: needs a real database")
	}

	return fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_txlock=immediate",
		filepath.Join(t.TempDir(), "unmigrated.sqlite"),
	)
}

// QueryRecorder is a sqlc DBTX that records the SQL and arguments of a query or
// exec instead of running it, so a test can EXPLAIN the exact generated statement.
type QueryRecorder struct {
	*sql.DB

	Query string
	Args  []any
}

// ExecContext records query and args and returns [errors.ErrUnsupported].
func (r *QueryRecorder) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	r.Query, r.Args = query, args

	return nil, errors.ErrUnsupported
}

// QueryContext records query and args and returns [errors.ErrUnsupported].
func (r *QueryRecorder) QueryContext(_ context.Context, query string, args ...any) (*sql.Rows, error) {
	r.Query, r.Args = query, args

	return nil, errors.ErrUnsupported
}

// QueryPlan returns the detail column of each EXPLAIN QUERY PLAN row for query
// bound to args.
func QueryPlan(t *testing.T, db *sql.DB, query string, args ...any) []string {
	t.Helper()

	rows, err := db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN %q err = %v", query, err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			t.Errorf("rows.Close err = %v", cerr)
		}
	}()

	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err = rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan plan row err = %v", err)
		}
		plan = append(plan, detail)
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("plan rows err = %v", err)
	}

	return plan
}
