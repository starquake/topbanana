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
			"file:db.sqlite?_pragma=foreign_keys(2)&_pragma=busy_timeout(5000)&_txlock=immediate",
			"file:db.sqlite?_pragma=foreign_keys('on')&_pragma=busy_timeout(5000)&_txlock=immediate",
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
