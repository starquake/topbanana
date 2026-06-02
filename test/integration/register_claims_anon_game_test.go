//go:build integration

package integration_test

import (
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
)

// TestRegister_PreservesAnonymousGame pins that registering a new account
// from an anonymous session keeps the guest's just-played game. Register
// upgrades the anonymous row in place (ClaimPlayer) rather than
// reattributing it the way the login / Google path does, so the game
// stays attached by identity: the registered account IS the former
// anonymous row, and its game_participants survive. Complements
// TestAnonMigration_Integration, which covers the reattribute path.
func TestRegister_PreservesAnonymousGame(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	stores := setup.Stores

	quizRow := &quiz.Quiz{
		Title: "Register Claim Quiz", Slug: "register-claim-quiz",
		Description:       "Played as a guest, then registered.",
		CreatedByPlayerID: seededAdminID,
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "a", Correct: true}, {Text: "b"}}},
		},
	}
	if err := stores.Quizzes.CreateQuiz(ctx, quizRow); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}

	// Play a game anonymously: a fresh client gets an EnsurePlayer row the
	// first time it touches the API.
	anonClient := authClient(t)
	primeAnonymousPlayer(ctx, t, anonClient, setup.BaseURL)
	anonPlayer := lookupAnonPlayer(ctx, t, stores.Players, "admin", "reg-claim")
	finishGameInt(t, stores.Games, anonPlayer.ID, quizRow)
	requirePlayerGameCount(t, setup.DBURI, anonPlayer.ID, 1)

	// Register through the SAME client so the anonymous session cookie
	// rides along; register upgrades that row into the new account.
	registerForPending(ctx, t, anonClient, setup.BaseURL, "reg-claim", "correct-battery-claim-13")

	// The registered account is the former anonymous row (same id), and
	// the game it played as a guest is still attached.
	registered, err := stores.Players.GetPlayerByDisplayName(ctx, "reg-claim")
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
	}
	if got, want := registered.ID, anonPlayer.ID; got != want {
		t.Errorf("registered player id = %d, want %d (the upgraded anonymous row)", got, want)
	}
	requirePlayerGameCount(t, setup.DBURI, registered.ID, 1)
}
