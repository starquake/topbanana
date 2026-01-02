package store_test

import (
	"database/sql"
	"log/slog"
	"testing"

	. "github.com/starquake/topbanana/internal/store"
)

func TestNew(t *testing.T) {
	t.Parallel()

	conn := &sql.DB{}
	logger := slog.New(slog.DiscardHandler)

	stores := New(conn, logger)

	if stores == nil {
		t.Error("stores is nil")
	}
}
