package game_test

import (
	"testing"

	"github.com/starquake/topbanana/internal/database"
)

func TestMain(m *testing.M) {
	// Configure goose global state exactly once for this package's tests.
	database.SetupGoose()

	// Run tests.
	m.Run()
}
