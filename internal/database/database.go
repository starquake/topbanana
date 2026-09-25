// Package database provides database access.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
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
// one of the settings the application relies on for correct behaviour. SQLite
// pragmas are per-connection, so they have to ride in the DSN (which the driver
// applies to every pooled connection); a one-off PRAGMA exec on the pool would
// only configure a single connection. Validating the DSN at startup turns a
// silent "FK enforcement off, no busy-timeout" footgun into a clear boot
// failure (#790).
var ErrMissingSQLitePragma = errors.New("DB_URI is missing a required SQLite pragma")

// ErrDisabledSQLitePragma is returned by [Open] when a sqlite DB_URI carries a
// required setting with a value that switches it off, such as foreign_keys(0).
var ErrDisabledSQLitePragma = errors.New("DB_URI disables a required SQLite pragma")

// requiredSQLitePragmas lists the pragmas a sqlite DB_URI must enable, with the
// driver's shorthand DSN keys for each.
//
//nolint:gochecknoglobals // an immutable lookup table, not mutable package state.
var requiredSQLitePragmas = []struct {
	name      string
	shorthand []string
	enabled   func(value string) bool
}{
	{name: "foreign_keys", shorthand: []string{"_foreign_keys", "_fk"}, enabled: func(v string) bool {
		// SQLite reads odd spellings such as foreign_keys(256) and (-1) as off.
		switch v {
		case "1", "on", "true", "yes":
			return true
		}

		return false
	}},
	{name: "busy_timeout", shorthand: []string{"_busy_timeout", "_timeout"}, enabled: func(v string) bool {
		n, err := strconv.Atoi(v)

		return err == nil && n > 0
	}},
}

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

// validateSQLitePragmas fails fast when a sqlite DSN omits or disables a
// pragma in [requiredSQLitePragmas] or lacks _txlock=immediate, without which a
// read-then-write transaction fails with SQLITE_BUSY instead of waiting (#790).
func validateSQLitePragmas(uri string) error {
	rawQuery := ""
	if _, after, found := strings.Cut(uri, "?"); found {
		rawQuery = after
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return fmt.Errorf("parsing DB_URI query string: %w", err)
	}

	pragmas := parsePragmas(values["_pragma"])
	for _, required := range requiredSQLitePragmas {
		settings := pragmas[required.name]
		for _, key := range required.shorthand {
			for _, v := range values[key] {
				settings = append(settings, strings.ToLower(strings.TrimSpace(v)))
			}
		}
		if len(settings) == 0 {
			return fmt.Errorf(
				"%w: %q (add _pragma=%s(...) to DB_URI)",
				ErrMissingSQLitePragma,
				required.name,
				required.name,
			)
		}
		for _, v := range settings {
			if !required.enabled(v) {
				return fmt.Errorf("%w: %s(%s)", ErrDisabledSQLitePragma, required.name, v)
			}
		}
	}

	switch txlock := values.Get("_txlock"); {
	case txlock == "":
		return fmt.Errorf("%w: _txlock (add _txlock=immediate to DB_URI)", ErrMissingSQLitePragma)
	case !strings.EqualFold(txlock, "immediate"):
		return fmt.Errorf("%w: _txlock=%s, want immediate", ErrDisabledSQLitePragma, txlock)
	}

	return nil
}

// parsePragmas maps each lower-cased pragma name in the _pragma DSN values to
// the values it is set to, accepting both the name(value) and name=value forms
// the driver passes through to SQLite.
func parsePragmas(raw []string) map[string][]string {
	pragmas := make(map[string][]string, len(raw))
	for _, p := range raw {
		p = strings.ToLower(strings.TrimSpace(p))
		i := strings.IndexAny(p, "(=")
		if i < 0 {
			pragmas[p] = append(pragmas[p], "")

			continue
		}
		name := strings.TrimSpace(p[:i])
		value := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(p[i+1:]), ")"))
		value = strings.Trim(value, `'"`)
		pragmas[name] = append(pragmas[name], value)
	}

	return pragmas
}

// Migrate runs database migrations against conn, which must be held to one
// open connection (see [OpenMigrated]). Safe for concurrent callers: goose.Up
// reads goose's package-level state, so we serialise (see migrateMu).
func Migrate(conn *sql.DB) error {
	migrateMu.Lock()
	defer migrateMu.Unlock()

	if err := goose.Up(conn, "."); err != nil {
		return fmt.Errorf("error running migrations: %w", err)
	}

	return nil
}

// OpenMigrated opens a database like [Open] and migrates it while the pool is
// held to one connection, since the NO TRANSACTION migrations spread PRAGMA,
// BEGIN and a temp table over separate statements (#1347). The given pool
// limits apply once migration succeeds.
func OpenMigrated(
	ctx context.Context,
	driver, uri string,
	dbMaxOpenConns, dbMaxIdleConns int,
	dbConnMaxLifetime time.Duration,
) (*sql.DB, error) {
	conn, err := Open(ctx, driver, uri, 1, 1, 0)
	if err != nil {
		return nil, err
	}
	if err = Migrate(conn); err != nil {
		if cerr := conn.Close(); cerr != nil {
			return nil, errors.Join(err, fmt.Errorf("error closing database: %w", cerr))
		}

		return nil, err
	}

	conn.SetMaxOpenConns(dbMaxOpenConns)
	conn.SetMaxIdleConns(dbMaxIdleConns)
	conn.SetConnMaxLifetime(dbConnMaxLifetime)

	return conn, nil
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
	// Releases the transaction if fn panics; a no-op after Commit or Rollback.
	defer func() { _ = tx.Rollback() }()
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
