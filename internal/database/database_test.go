package database_test

import (
	"context"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/database"
	"github.com/starquake/topbanana/internal/db"
	"github.com/starquake/topbanana/internal/dbtest"
)

func TestValidateSQLitePragmas(t *testing.T) {
	t.Parallel()

	const completeMemoryDSN = ":memory:?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate"

	t.Run("accepts the committed default DSN", func(t *testing.T) {
		t.Parallel()
		// Guards against the required-pragma list drifting away from the
		// shipped default; the app must never reject its own default DB_URI.
		if err := database.ExportValidateSQLitePragmas(config.DBURIDefault); err != nil {
			t.Errorf("err = %v, want nil for config.DBURIDefault", err)
		}
	})

	t.Run("accepts the in-memory DSN form", func(t *testing.T) {
		t.Parallel()
		if err := database.ExportValidateSQLitePragmas(completeMemoryDSN); err != nil {
			t.Errorf("err = %v, want nil for a complete in-memory DSN", err)
		}
	})

	t.Run("accepts pragmas regardless of case", func(t *testing.T) {
		t.Parallel()
		dsn := "file:db.sqlite?_pragma=FOREIGN_KEYS(1)&_pragma=Busy_Timeout(5000)&_txlock=IMMEDIATE"
		if err := database.ExportValidateSQLitePragmas(dsn); err != nil {
			t.Errorf("err = %v, want nil for an upper-case-pragma DSN", err)
		}
	})

	t.Run("rejects a DSN missing foreign_keys", func(t *testing.T) {
		t.Parallel()
		dsn := "file:db.sqlite?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"
		err := database.ExportValidateSQLitePragmas(dsn)
		if got, want := err, database.ErrMissingSQLitePragma; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("rejects a DSN missing busy_timeout", func(t *testing.T) {
		t.Parallel()
		dsn := "file:db.sqlite?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_txlock=immediate"
		err := database.ExportValidateSQLitePragmas(dsn)
		if got, want := err, database.ErrMissingSQLitePragma; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("accepts the name=value pragma form", func(t *testing.T) {
		t.Parallel()
		dsn := "file:db.sqlite?_pragma=foreign_keys%3DON&_pragma=busy_timeout%3D5000&_txlock=immediate"
		if err := database.ExportValidateSQLitePragmas(dsn); err != nil {
			t.Errorf("err = %v, want nil for a name=value DSN", err)
		}
	})

	t.Run("accepts the driver shorthand keys and other enabling values", func(t *testing.T) {
		t.Parallel()
		for _, dsn := range []string{
			"file:db.sqlite?_foreign_keys=1&_busy_timeout=5000&_txlock=immediate",
			"file:db.sqlite?_fk=YES&_busy_timeout=5000&_txlock=immediate",
			"file:db.sqlite?_foreign_keys=true&_busy_timeout=5000&_txlock=immediate",
			"file:db.sqlite?_pragma=foreign_keys('on')&_pragma=busy_timeout(5000)&_txlock=immediate",
			`file:db.sqlite?_pragma=foreign_keys("TRUE")&_pragma=busy_timeout(5000)&_txlock=immediate`,
			"file:db.sqlite?_pragma=foreign_keys%3Dyes&_pragma=busy_timeout(5000)&_txlock=immediate",
		} {
			if err := database.ExportValidateSQLitePragmas(dsn); err != nil {
				t.Errorf("err = %v, want nil for %q", err, dsn)
			}
		}
	})

	for _, tt := range []struct {
		name string
		dsn  string
		want error
	}{
		{
			name: "rejects foreign_keys(0)",
			dsn:  "file:db.sqlite?_pragma=foreign_keys(0)&_pragma=busy_timeout(5000)&_txlock=immediate",
			want: database.ErrDisabledSQLitePragma,
		},
		{
			name: "rejects foreign_keys(-1), which SQLite reads as off",
			dsn:  "file:db.sqlite?_pragma=foreign_keys(-1)&_pragma=busy_timeout(5000)&_txlock=immediate",
			want: database.ErrDisabledSQLitePragma,
		},
		{
			name: "rejects foreign_keys(256), which SQLite reads as off",
			dsn:  "file:db.sqlite?_pragma=foreign_keys(256)&_pragma=busy_timeout(5000)&_txlock=immediate",
			want: database.ErrDisabledSQLitePragma,
		},
		{
			name: "rejects foreign_keys(full), which SQLite reads as off",
			dsn:  "file:db.sqlite?_pragma=foreign_keys(full)&_pragma=busy_timeout(5000)&_txlock=immediate",
			want: database.ErrDisabledSQLitePragma,
		},
		{
			name: "rejects the _fk shorthand set to -1",
			dsn:  "file:db.sqlite?_fk=-1&_pragma=busy_timeout(5000)&_txlock=immediate",
			want: database.ErrDisabledSQLitePragma,
		},
		{
			name: "rejects the _foreign_keys shorthand set to 256",
			dsn:  "file:db.sqlite?_foreign_keys=256&_pragma=busy_timeout(5000)&_txlock=immediate",
			want: database.ErrDisabledSQLitePragma,
		},
		{
			name: "rejects an empty _fk shorthand, which suppresses the pragma",
			dsn:  "file:db.sqlite?_foreign_keys=1&_fk=&_pragma=busy_timeout(5000)&_txlock=immediate",
			want: database.ErrDisabledSQLitePragma,
		},
		{
			name: "rejects foreign_keys(off) alongside foreign_keys(1)",
			dsn:  "file:db.sqlite?_pragma=foreign_keys(1)&_pragma=foreign_keys(off)&_pragma=busy_timeout(5000)&_txlock=immediate",
			want: database.ErrDisabledSQLitePragma,
		},
		{
			name: "rejects busy_timeout(0)",
			dsn:  "file:db.sqlite?_pragma=foreign_keys(1)&_pragma=busy_timeout(0)&_txlock=immediate",
			want: database.ErrDisabledSQLitePragma,
		},
		{
			name: "rejects a non-numeric busy_timeout",
			dsn:  "file:db.sqlite?_pragma=foreign_keys(1)&_pragma=busy_timeout(soon)&_txlock=immediate",
			want: database.ErrDisabledSQLitePragma,
		},
		{
			name: "rejects a DSN missing _txlock",
			dsn:  "file:db.sqlite?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
			want: database.ErrMissingSQLitePragma,
		},
		{
			name: "rejects _txlock=deferred",
			dsn:  "file:db.sqlite?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=deferred",
			want: database.ErrDisabledSQLitePragma,
		},
		{
			name: "rejects the _fk shorthand switching foreign keys off",
			dsn:  "file:db.sqlite?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate&_fk=0",
			want: database.ErrDisabledSQLitePragma,
		},
		{
			name: "rejects the _timeout shorthand set to zero",
			dsn:  "file:db.sqlite?_pragma=foreign_keys(1)&_timeout=0&_txlock=immediate",
			want: database.ErrDisabledSQLitePragma,
		},
		{
			name: "rejects a pragma that only shares the prefix",
			dsn:  "file:db.sqlite?_pragma=foreign_keys_x(1)&_pragma=busy_timeout(5000)&_txlock=immediate",
			want: database.ErrMissingSQLitePragma,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := database.ExportValidateSQLitePragmas(tt.dsn)
			if got, want := err, tt.want; !errors.Is(got, want) {
				t.Errorf("err = %v, want %v", got, want)
			}
		})
	}

	t.Run("rejects a DSN with no query string at all", func(t *testing.T) {
		t.Parallel()
		err := database.ExportValidateSQLitePragmas("file:db.sqlite")
		if got, want := err, database.ErrMissingSQLitePragma; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

// TestOpen_AcceptedForeignKeysValuesEnableEnforcement pins that every
// foreign_keys value validation accepts really switches enforcement on.
func TestOpen_AcceptedForeignKeysValuesEnableEnforcement(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("integration: needs a real database")
	}

	for _, tt := range []struct {
		query  string
		accept bool
	}{
		{query: "_pragma=foreign_keys(1)", accept: true},
		{query: "_pragma=foreign_keys(ON)", accept: true},
		{query: "_pragma=foreign_keys('true')", accept: true},
		{query: "_pragma=foreign_keys%3Dyes", accept: true},
		{query: "_fk=1", accept: true},
		{query: "_fk=on", accept: true},
		{query: "_foreign_keys=TRUE", accept: true},
		{query: "_foreign_keys=yes", accept: true},
		{query: "_pragma=foreign_keys(0)"},
		{query: "_pragma=foreign_keys(-1)"},
		{query: "_pragma=foreign_keys(256)"},
		{query: "_pragma=foreign_keys(full)"},
		{query: "_fk=off"},
		{query: "_foreign_keys=no"},
	} {
		t.Run(tt.query, func(t *testing.T) {
			t.Parallel()

			dsn := ":memory:?" + tt.query + "&_pragma=busy_timeout(5000)&_txlock=immediate"
			conn, err := database.Open(t.Context(), "sqlite", dsn, 1, 1, 0)
			if !tt.accept {
				if got, want := err, database.ErrDisabledSQLitePragma; !errors.Is(got, want) {
					t.Errorf("Open err = %v, want %v", got, want)
				}

				return
			}
			if err != nil {
				t.Fatalf("Open err = %v, want nil", err)
			}
			t.Cleanup(func() {
				if cerr := conn.Close(); cerr != nil {
					t.Errorf("conn.Close err = %v", cerr)
				}
			})

			var foreignKeys int
			if err = conn.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
				t.Fatalf("PRAGMA foreign_keys err = %v", err)
			}
			if got, want := foreignKeys, 1; got != want {
				t.Errorf("PRAGMA foreign_keys = %d, want %d", got, want)
			}
		})
	}
}

// TestExecTx_PanicReleasesTransaction pins that a panicking fn does not leave
// its transaction holding the pool's only connection.
func TestExecTx_PanicReleasesTransaction(t *testing.T) {
	t.Parallel()

	conn := dbtest.OpenUnmigrated(t)
	t.Cleanup(func() {
		if cerr := conn.Close(); cerr != nil {
			t.Errorf("conn.Close err = %v", cerr)
		}
	})

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("recover() = nil, want the fn panic")
			}
		}()
		// fn panics, so ExecTx never returns an error to check.
		_ = database.ExecTx(t.Context(), conn, func(*db.Queries) error {
			panic("boom")
		})
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := database.ExecTx(ctx, conn, func(*db.Queries) error { return nil }); err != nil {
		t.Errorf("ExecTx after panic err = %v, want nil", err)
	}
}
