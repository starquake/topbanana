package livesession_test

import (
	"testing"

	"github.com/starquake/topbanana/internal/database"
)

func TestMain(m *testing.M) {
	// Configure goose global state once so the runner's DB-backed tests can
	// open a migrated in-memory database via dbtest.Open.
	database.SetupGoose()

	m.Run()
}
