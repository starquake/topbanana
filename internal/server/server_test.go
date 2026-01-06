package server_test

import (
	"log/slog"
	"testing"

	"github.com/starquake/topbanana/internal/game"
	. "github.com/starquake/topbanana/internal/server"
	"github.com/starquake/topbanana/internal/store"
)

func TestNewServer(t *testing.T) {
	t.Parallel()

	srv := New(slog.New(slog.DiscardHandler), &store.Stores{}, &game.Service{})

	if srv == nil {
		t.Error("srv is nil")
	}
}
