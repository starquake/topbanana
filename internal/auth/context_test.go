package auth_test

import (
	"context"
	"testing"

	"github.com/starquake/topbanana/internal/auth"
)

func TestPlayerFromContext_RoundTrip(t *testing.T) {
	t.Parallel()

	want := &auth.Player{ID: 7, Username: "alice", Role: auth.RoleAdmin}
	ctx := auth.WithPlayer(t.Context(), want)

	got, ok := auth.PlayerFromContext(ctx)
	if !ok {
		t.Fatal("PlayerFromContext ok = false, want true")
	}
	if got != want {
		t.Errorf("PlayerFromContext = %+v, want %+v", got, want)
	}
}

func TestPlayerFromContext_Missing(t *testing.T) {
	t.Parallel()

	got, ok := auth.PlayerFromContext(context.Background())
	if ok {
		t.Error("PlayerFromContext ok = true, want false")
	}
	if got != nil {
		t.Errorf("PlayerFromContext = %+v, want nil", got)
	}
}
