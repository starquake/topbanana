package quiz_test

import (
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/starquake/topbanana/internal/migrations"
)

func TestMain(m *testing.M) {
	// Configure goose global state exactly once for this package's tests.
	// Maybe move this to a test helper if this is used in another package?
	goose.SetBaseFS(migrations.FS)
	err := goose.SetDialect("sqlite3")
	if err != nil {
		panic(err)
	}

	// Run tests.
	m.Run()
}
