//go:build integration

package game_test

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/starquake/topbanana/internal/dbtest"
	. "github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/leaderboard"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// TestCreateGameRace_Integration covers #273: two concurrent
// Service.CreateGame calls for the same (player, quiz) must produce
// exactly one game. Before the fix, the service did a check-then-insert
// without a transaction or a DB-level constraint, so both calls could
// pass the existence check and both insert. The fix denormalises
// quiz_id onto game_participants and adds a UNIQUE INDEX on
// (player_id, quiz_id); the loser of the race surfaces as
// ErrGameAlreadyExists from CreateParticipant.
func TestCreateGameRace_Integration(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)

	qz := &quiz.Quiz{
		Title:             "Race Quiz",
		Slug:              "race-quiz",
		Description:       "for the create-game race test",
		CreatedByPlayerID: seededAdminID,
		Questions: []*quiz.Question{
			{
				Text:     "Q1",
				Position: 1,
				Options: []*quiz.Option{
					{Text: "A", Correct: true},
					{Text: "B"},
				},
			},
		},
	}
	stores := store.New(db, slog.Default())
	if cerr := stores.Quizzes.CreateQuiz(ctx, qz); cerr != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", cerr)
	}

	player, err := stores.Players.CreateAnonymousPlayer(ctx, "anon-race-victim")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	// Drive Service.CreateGame directly so the test can fan out the call
	// without spinning up two HTTP clients (which would serialise via
	// EnsurePlayer's session cookie logic).
	svc := NewService(stores.Games, stores.Quizzes, slog.Default())
	svc.SetLeaderboardPublisher(leaderboard.NewHub())

	// Fire N parallel CreateGame goroutines. With the UNIQUE index in
	// place exactly one should return a game; every other call must
	// return ErrGameAlreadyExists. Without the fix the race used to
	// allow two successes; we keep N at 4 to make the assertion sturdy
	// against flaky scheduling on slow CI runners.
	const parallel = 4
	results := make([]error, parallel)
	games := make([]*Game, parallel)

	var wg sync.WaitGroup
	wg.Add(parallel)
	for i := range parallel {
		go func(idx int) {
			defer wg.Done()
			g, gerr := svc.CreateGame(context.Background(), qz.ID, player.ID)
			games[idx] = g
			results[idx] = gerr
		}(i)
	}
	wg.Wait()

	var successCount, alreadyExistsCount int
	var winnerGameID string
	for i, r := range results {
		switch {
		case r == nil:
			successCount++
			if games[i] == nil {
				t.Errorf("goroutine %d returned nil error but nil game", i)
			} else {
				winnerGameID = games[i].ID
			}
		case errors.Is(r, ErrGameAlreadyExists):
			alreadyExistsCount++
		default:
			t.Errorf("goroutine %d returned unexpected error: %v", i, r)
		}
	}

	if got, want := successCount, 1; got != want {
		t.Errorf("successful CreateGame calls = %d, want %d", got, want)
	}
	if got, want := alreadyExistsCount, parallel-1; got != want {
		t.Errorf("ErrGameAlreadyExists returns = %d, want %d", got, want)
	}

	// Sanity check the DB side too: exactly one game_participants row
	// for (player, quiz). Without the DB-level constraint the row count
	// would track the buggy success count.
	rowCount := countParticipantRows(ctx, t, db, player.ID, qz.ID)
	if got, want := rowCount, 1; got != want {
		t.Errorf("game_participants rows for (player=%d, quiz=%d) = %d, want %d",
			player.ID, qz.ID, got, want)
	}

	if winnerGameID == "" {
		t.Error("no winning goroutine recorded a game ID")
	}
}

// countParticipantRows pulls the row count for a (player_id, quiz_id)
// pair directly from the DB. The UNIQUE INDEX added by the #273
// migration should make this either 0 or 1; the test asserts exactly 1
// after a single successful CreateGame.
func countParticipantRows(ctx context.Context, t *testing.T, db *sql.DB, playerID, quizID int64) int {
	t.Helper()
	row := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM game_participants WHERE player_id = ? AND quiz_id = ?`,
		playerID, quizID,
	)
	var n int
	if err := row.Scan(&n); err != nil {
		t.Fatalf("QueryRow.Scan err = %v, want nil", err)
	}

	return n
}
