package server_test

import (
	"log/slog"
	"testing"

	. "github.com/starquake/topbanana/internal/server"
	"github.com/starquake/topbanana/internal/store"
)

func TestNewServer(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	stores := &store.Stores{}

	srv := New(logger, stores)

	if srv == nil {
		t.Error("srv is nil")
	}
}
