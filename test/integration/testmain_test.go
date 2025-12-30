package integration_test

import (
	"testing"

	"github.com/starquake/topbanana/internal/db"
)

func TestMain(m *testing.M) {
	// Configure goose global state exactly once for this package's tests.
	db.SetupGoose()

	// Run tests.
	m.Run()
}
