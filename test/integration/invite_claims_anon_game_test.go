package integration_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/quiz"
)

// TestAcceptInvite_PreservesAnonymousGame pins #617: a guest who plays a
// game and then accepts an invite in the same browser keeps that game.
// Invite-accept creates a fresh credentialled row (unlike register's
// in-place upgrade), so the anonymous row's games are reattributed onto
// the new account, matching the login / Google paths.
func TestAcceptInvite_PreservesAnonymousGame(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	stores := setup.Stores

	quizRow := &quiz.Quiz{
		Title: "Invite Claim Quiz", Slug: "invite-claim-quiz",
		Published:         true,
		Description:       "Played as a guest, then accepted an invite.",
		CreatedByPlayerID: seededAdminID,
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "a", Correct: true}, {Text: "b"}}},
		},
	}
	if err := stores.Quizzes.CreateQuiz(ctx, quizRow); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}

	// Play a game anonymously through one client.
	client := authClient(t)
	primeAnonymousPlayer(ctx, t, client, setup.BaseURL)
	anonPlayer := lookupAnonPlayer(ctx, t, stores.Players, "admin")
	finishGameInt(t, stores.Games, anonPlayer.ID, quizRow)
	requirePlayerGameCount(t, setup.DBURI, anonPlayer.ID, 1)

	// Accept an invite through the SAME client so the guest session rides
	// along; the new account should inherit the just-played game.
	raw := mintInvite(ctx, t, stores.Invites, "invite-anon@example.test", time.Now().Add(time.Hour))
	resp := postAcceptInvite(ctx, t, client, setup.BaseURL, raw, "Invited Player")
	defer resp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("accept-invite status = %d, want %d", got, want)
	}

	invited, err := stores.Players.GetPlayerByDisplayName(ctx, "Invited Player")
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
	}
	if invited.ID == anonPlayer.ID {
		t.Fatalf("invited player id = %d, want a fresh row distinct from the anonymous row", invited.ID)
	}

	// The just-played game moved off the anonymous row onto the new account.
	requirePlayerGameCount(t, setup.DBURI, anonPlayer.ID, 0)
	requirePlayerGameCount(t, setup.DBURI, invited.ID, 1)
}
