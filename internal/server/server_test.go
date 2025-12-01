package server_test

import (
	"io"
	"testing"

	"github.com/starquake/topbanana/internal/logging"
	"github.com/starquake/topbanana/internal/server"
	"github.com/starquake/topbanana/internal/store"
)

func TestNewServer(t *testing.T) {
	t.Parallel()

	logger := logging.NewLogger(io.Discard)
	stores := &store.Stores{}

	srv := server.NewServer(logger, stores)

	if srv == nil {
		t.Error("srv is nil")
	}
}
