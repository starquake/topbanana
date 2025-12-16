package quiz_test

import (
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/starquake/topbanana/internal/migrations"
	"github.com/starquake/topbanana/internal/must"
)

func TestMain(m *testing.M) {
	// Configure goose global state exactly once for this package's tests.
	goose.SetBaseFS(migrations.FS)
	must.OK(goose.SetDialect("sqlite3"))

	// Run tests.
	m.Run()
}
