package main_test

import (
	"testing"

	"github.com/starquake/topbanana/internal/database"
)

func TestMain(m *testing.M) {
	// Configure goose global state exactly once so dbtest's migrated template
	// build (goose.Up against the embedded migrations FS) succeeds.
	database.SetupGoose()

	m.Run()
}
