package server_test

import (
	"testing"

	"github.com/starquake/topbanana/internal/database"
)

func TestMain(m *testing.M) {
	// Configure goose global state once so dbtest.Open can run migrations.
	database.SetupGoose()

	m.Run()
}
