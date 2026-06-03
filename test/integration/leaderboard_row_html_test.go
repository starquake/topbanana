package integration_test

import (
	"strings"
	"testing"
)

// TestLeaderboardRow_NoInlinePencil pins #317: the inline "Set my name"
// pencil button has been removed from the leaderboard row. The two
// remaining claim affordances (pre-leaderboard auto-modal and the
// start-screen "Set your name" link) cover the same need without
// crowding the row.
func TestLeaderboardRow_NoInlinePencil(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	body := getBody(ctx, t, srv.BaseURL+"/client/")

	for _, banned := range []string{
		`aria-label="Set my name"`,
		`title="Set my name"`,
	} {
		if strings.Contains(body, banned) {
			t.Errorf("/client/ HTML still contains %q — the inline leaderboard pencil should be removed (#317)", banned)
		}
	}
}
