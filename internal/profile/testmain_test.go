package profile_test

import (
	"testing"

	"github.com/starquake/topbanana/internal/database"
)

func TestMain(m *testing.M) {
	// Configure goose global state once so dbtest.Open can apply
	// migrations for the real-store password-rotation tests.
	database.SetupGoose()

	m.Run()
}
