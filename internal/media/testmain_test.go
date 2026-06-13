package media_test

import (
	"testing"

	"github.com/starquake/topbanana/internal/database"
)

func TestMain(m *testing.M) {
	// Configure goose global state once so dbtest.Open can apply the
	// embedded migrations for this package's integration tests.
	database.SetupGoose()

	m.Run()
}
