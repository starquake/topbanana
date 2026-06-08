// Package database provides database access.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/starquake/topbanana/internal/db"
	"github.com/starquake/topbanana/internal/migrations"
)

// sqliteDriverName is the registered modernc.org/sqlite driver name. Pragma
// validation in [Open] only applies to this driver.
const sqliteDriverName = "sqlite"

// ErrMissingSQLitePragma is returned by [Open] when a sqlite DB_URI is missing
// one of the pragmas the application relies on for correct behaviour. SQLite
// pragmas are per-connection, so they have to ride in the DSN (which the driver
// applies to every pooled connection); a one-off PRAGMA exec on the pool would
// only configure a single connection. Validating the DSN at startup turns a
// silent "FK enforcement off, no busy-timeout" footgun into a clear boot
// failure (#790).
var ErrMissingSQLitePragma = errors.New("DB_URI is missing a required SQLite pragma")

// requiredSQLitePragmas names the pragmas a sqlite DB_URI must carry. These are
// matched against the prefix of each _pragma DSN value (e.g. the value
// "foreign_keys(1)" satisfies "foreign_keys"), mirroring how the driver itself
// reads them. foreign_keys keeps referential integrity enforced; busy_timeout
// stops concurrent writers from failing immediately with SQLITE_BUSY.
//
//nolint:gochecknoglobals // an immutable lookup table, not mutable package state.
var requiredSQLitePragmas = []string{"foreign_keys", "busy_timeout"}

// migrateMu serialises Migrate calls. goose's package-level state (the
// migration registry built lazily from BaseFS) is not safe under concurrent
// goose.Up calls - even when each call holds its own [sql.DB]. The integration
// test suite exposes this by spinning up several test servers in parallel,
// each calling Migrate against its own per-test SQLite file. Serialising the
// migration step is negligible in practice (one call per process boot in
// production) and eliminates the race entirely.
//
// gochecknoglobals would prefer this lived on a struct, but Migrate is the
// package's contract surface and the mutex protects state inside goose, not
// state we own. A constructor-based refactor would push the same mutex onto
// every caller of Migrate without changing the contention shape.
//
//nolint:gochecknoglobals // mutex protects an unavoidable package-level resource (goose globals).
var migrateMu sync.Mutex

// setupGooseOnce guarantees goose's package-level state (BaseFS + Dialect)
// is installed exactly once per process even if SetupGoose is called from
// multiple test setup helpers. Without this, a process that has both a
// TestMain and a per-test setup that both call SetupGoose can race goose's
// own globals against an in-flight Migrate call.
//
//nolint:gochecknoglobals // pairs with SetupGoose to guard the same goose globals.
var setupGooseOnce sync.Once

// SetupGoose installs goose's dialect and BaseFS in its package-level state.
// Idempotent: subsequent calls are no-ops, so it is safe to call from both a
// TestMain and per-test setup helpers without racing goose's globals against
// concurrent [Migrate] calls.
func SetupGoose() {
	setupGooseOnce.Do(func() {
		goose.SetBaseFS(migrations.FS)

		if err := goose.SetDialect("sqlite3"); err != nil {
			panic(err)
		}
	})
}

// Open opens a database connection. For the sqlite driver it first validates
// that the DSN carries the pragmas the application depends on (see
// [validateSQLitePragmas]); an operator who overrides DB_URI without them gets a
// clear boot failure instead of silently losing FK enforcement (#790).
func Open(
	_ context.Context,
	driver, uri string,
	dbMaxOpenConns, dbMaxIdleConns int,
	dbConnMaxLifetime time.Duration,
) (*sql.DB, error) {
	if driver == sqliteDriverName {
		if err := validateSQLitePragmas(uri); err != nil {
			return nil, err
		}
	}

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

// validateSQLitePragmas fails fast when a sqlite DSN omits a pragma in
// [requiredSQLitePragmas]. The query string after the first '?' is parsed the
// same way the driver does (url.ParseQuery, _pragma values prefix-matched
// case-insensitively), so a DSN that satisfies this check carries exactly the
// pragmas the driver will apply to every connection. Augmenting the operator's
// DSN instead would be surprising; a clear error naming the missing pragma lets
// them fix their own configuration (#790).
func validateSQLitePragmas(uri string) error {
	rawQuery := ""
	if _, after, found := strings.Cut(uri, "?"); found {
		rawQuery = after
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return fmt.Errorf("parsing DB_URI query string: %w", err)
	}

	pragmas := values["_pragma"]
	for _, required := range requiredSQLitePragmas {
		if !hasPragma(pragmas, required) {
			return fmt.Errorf("%w: %q (add _pragma=%s(...) to DB_URI)", ErrMissingSQLitePragma, required, required)
		}
	}

	return nil
}

// hasPragma reports whether any _pragma DSN value names the given pragma,
// matching on the prefix before the '(' so "foreign_keys(1)" satisfies
// "foreign_keys". Comparison is case-insensitive and ignores surrounding
// whitespace, mirroring the driver's own pragma handling.
func hasPragma(pragmas []string, name string) bool {
	for _, p := range pragmas {
		if strings.HasPrefix(strings.TrimSpace(strings.ToLower(p)), name) {
			return true
		}
	}

	return false
}

// Migrate runs database migrations against conn. Safe for concurrent callers:
// goose.Up reads goose's package-level state (the migration registry, the
// dialect, the BaseFS) which is not goroutine-safe, so we serialise. See the
// migrateMu comment above for why this is necessary.
func Migrate(conn *sql.DB) error {
	migrateMu.Lock()
	defer migrateMu.Unlock()

	if err := goose.Up(conn, "."); err != nil {
		return fmt.Errorf("error running migrations: %w", err)
	}

	return nil
}

// MustRowsAffected returns the number of rows affected by res, panicking if the driver returns an error.
func MustRowsAffected(res sql.Result) int64 {
	rows, err := res.RowsAffected()
	if err != nil {
		panic(err)
	}

	return rows
}

// ExecTx is a helper to run queries within a transaction.
func ExecTx(ctx context.Context, conn *sql.DB, fn func(*db.Queries) error) error {
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
