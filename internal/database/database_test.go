package database_test

import (
	"errors"
	"testing"

	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/database"
)

func TestValidateSQLitePragmas(t *testing.T) {
	t.Parallel()

	const completeMemoryDSN = ":memory:?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"

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
		dsn := "file:db.sqlite?_pragma=FOREIGN_KEYS(1)&_pragma=Busy_Timeout(5000)"
		if err := database.ExportValidateSQLitePragmas(dsn); err != nil {
			t.Errorf("err = %v, want nil for an upper-case-pragma DSN", err)
		}
	})

	t.Run("rejects a DSN missing foreign_keys", func(t *testing.T) {
		t.Parallel()
		dsn := "file:db.sqlite?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
		err := database.ExportValidateSQLitePragmas(dsn)
		if got, want := err, database.ErrMissingSQLitePragma; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("rejects a DSN missing busy_timeout", func(t *testing.T) {
		t.Parallel()
		dsn := "file:db.sqlite?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
		err := database.ExportValidateSQLitePragmas(dsn)
		if got, want := err, database.ErrMissingSQLitePragma; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("rejects a DSN with no query string at all", func(t *testing.T) {
		t.Parallel()
		err := database.ExportValidateSQLitePragmas("file:db.sqlite")
		if got, want := err, database.ErrMissingSQLitePragma; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}
