package game_test

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"

	"github.com/starquake/topbanana/internal/dbtest"
	. "github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/leaderboard"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// seededAdminID is the id of the admin row inserted by migration
// 20260111110308_add_admin_player.sql. Quiz fixtures attribute
// themselves to this admin so the NOT NULL created_by_player_id
// column from migration 20260520200000 (#281) is satisfied.
const seededAdminID int64 = 1

func newTestQuiz(t *testing.T) *quiz.Quiz {
	t.Helper()

	return &quiz.Quiz{
		Title:             "Flurpsydurpsy",
		Slug:              "flurpsydurpsy",
		CreatedByPlayerID: seededAdminID,
		Published:         true,
		Questions: []*quiz.Question{
			{
				Text:     "What is the capital of France?",
				Position: 10,
				Options: []*quiz.Option{
					{Text: "Paris", Correct: true},
					{Text: "London"},
				},
			},
			{
				Text:     "What is the capital of Germany?",
				Position: 20,
				Options: []*quiz.Option{
					{Text: "Berlin", Correct: true},
					{Text: "Hamburg"},
				},
			},
			{
				Text:     "What is the capital of Spain?",
				Position: 30,
				Options: []*quiz.Option{
					{Text: "Madrid", Correct: true},
					{Text: "Barcelona"},
				},
			},
		},
	}
}

// assertBoundaryWindow pins the #548 auto-advance contract at the
// service layer: a round-boundary item carries a non-zero
// StartedAt/ExpiredAt window exactly one quiz-default answer duration
// (timeLimitSeconds) long, for both phases.
func assertBoundaryWindow(t *testing.T, item *Item, timeLimitSeconds int) {
	t.Helper()
	if item.StartedAt.IsZero() {
		t.Error("item.StartedAt is zero, want a populated timestamp")
	}
	if item.ExpiredAt.IsZero() {
		t.Error("item.ExpiredAt is zero, want a populated timestamp")
	}
	want := time.Duration(timeLimitSeconds) * time.Second
	if got := item.ExpiredAt.Sub(item.StartedAt); got != want {
		t.Errorf("item window ExpiredAt-StartedAt = %v, want %v (quiz default)", got, want)
	}
}

func newTestGame(t *testing.T, qz *quiz.Quiz) *Game {
	t.Helper()

	return &Game{
		QuizID: qz.ID,
	}
}

func TestService_GetGameForPlayerOnQuiz(t *testing.T) {
	t.Parallel()

	t.Run("returns existing game with quiz populated", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		const playerID = int64(1)
		created, err := svc.CreateGame(ctx, testQuiz.ID, playerID, false)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		resumed, err := svc.GetGameForPlayerOnQuiz(ctx, playerID, testQuiz.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := resumed.ID, created.ID; got != want {
			t.Errorf("resumed.ID = %q, want %q", got, want)
		}
		if resumed.Quiz == nil {
			t.Fatal("resumed.Quiz is nil, want populated quiz")
		}
		if got, want := resumed.Quiz.ID, testQuiz.ID; got != want {
			t.Errorf("resumed.Quiz.ID = %d, want %d", got, want)
		}
		// IsCompleted should work because Quiz is populated.
		if got, want := resumed.IsCompleted(), false; got != want {
			t.Errorf("IsCompleted() = %v, want %v (no questions issued yet)", got, want)
		}
	})

	t.Run("returns ErrGameNotFound when player has no game", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		_, err := svc.GetGameForPlayerOnQuiz(ctx, 999, testQuiz.ID)
		if got, want := err, ErrGameNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("returns ErrQuizNotFound when quiz missing", func(t *testing.T) {
		t.Parallel()

		svc := NewService(stubStore{}, stubQuizStore{
			getQuiz: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return nil, quiz.ErrQuizNotFound
			},
		}, slog.New(slog.DiscardHandler))

		_, err := svc.GetGameForPlayerOnQuiz(t.Context(), 1, 999)
		if got, want := err, quiz.ErrQuizNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestService_GetQuiz(t *testing.T) {
	t.Parallel()

	t.Run("returns the seeded quiz", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		got, err := svc.GetQuiz(ctx, testQuiz.ID)
		if err != nil {
			t.Fatalf("GetQuiz err = %v, want nil", err)
		}
		if got == nil {
			t.Fatal("GetQuiz returned nil quiz, want the seeded quiz")
		}
		if want := testQuiz.ID; got.ID != want {
			t.Errorf("quiz ID = %d, want %d", got.ID, want)
		}
		if want := testQuiz.Title; got.Title != want {
			t.Errorf("quiz Title = %q, want %q", got.Title, want)
		}
	})

	t.Run("wraps the store error with a get-quiz prefix", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())
		svc := NewService(gameStore, quizStore, slog.Default())

		if err := db.Close(); err != nil {
			t.Fatalf("closing test DB err = %v, want nil", err)
		}

		_, err := svc.GetQuiz(ctx, 1)
		if err == nil {
			t.Fatal("GetQuiz err = nil, want a wrapped store error")
		}
		if got, want := err.Error(), "get quiz"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestService_CreateGame_RejectsSecondAttempt(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)

	quizStore := store.NewQuizStore(db, slog.Default())
	gameStore := store.NewGameStore(db, slog.Default())

	testQuiz := newTestQuiz(t)
	if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
		t.Fatalf("failed to create quiz: %v", err)
	}

	svc := NewService(gameStore, quizStore, slog.Default())

	const playerID = int64(1)
	if _, err := svc.CreateGame(ctx, testQuiz.ID, playerID, false); err != nil {
		t.Fatalf("failed to create initial game: %v", err)
	}

	// Second attempt for the same (player, quiz) must be rejected.
	_, err := svc.CreateGame(ctx, testQuiz.ID, playerID, false)
	if got, want := err, ErrGameAlreadyExists; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestService_CreateGame_RejectsUnpublishedDraft(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)

	quizStore := store.NewQuizStore(db, slog.Default())
	gameStore := store.NewGameStore(db, slog.Default())

	// A draft (Published false) is not playable by a real player (#1192): the
	// service surfaces ErrQuizNotFound so a draft stays indistinguishable from a
	// missing quiz.
	draft := newTestQuiz(t)
	draft.Published = false
	if err := quizStore.CreateQuiz(ctx, draft); err != nil {
		t.Fatalf("failed to create draft quiz: %v", err)
	}

	svc := NewService(gameStore, quizStore, slog.Default())

	_, err := svc.CreateGame(ctx, draft.ID, int64(1), false)
	if got, want := err, quiz.ErrQuizNotFound; !errors.Is(got, want) {
		t.Errorf("CreateGame(draft, normal) err = %v, want %v", got, want)
	}
}

func TestService_CreateGame_Preview(t *testing.T) {
	t.Parallel()

	const playerID = int64(1)

	t.Run("owner previews a draft, re-preview resets the prior game", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)
		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		draft := newTestQuiz(t)
		draft.Published = false
		if err := quizStore.CreateQuiz(ctx, draft); err != nil {
			t.Fatalf("failed to create draft quiz: %v", err)
		}
		svc := NewService(gameStore, quizStore, slog.Default())

		first, err := svc.CreateGame(ctx, draft.ID, playerID, true)
		if err != nil {
			t.Fatalf("preview CreateGame err = %v, want nil", err)
		}
		if !first.Preview {
			t.Error("first preview game Preview = false, want true")
		}
		if got := previewFlagOf(t, db, first.ID); !got {
			t.Error("games.is_preview = 0 for a preview game, want 1")
		}

		// Re-previewing resets the prior game (a new game id) despite the
		// one-attempt UNIQUE(player_id, quiz_id) index.
		second, err := svc.CreateGame(ctx, draft.ID, playerID, true)
		if err != nil {
			t.Fatalf("re-preview CreateGame err = %v, want nil", err)
		}
		if got, want := second.ID, first.ID; got == want {
			t.Errorf("re-preview game ID = %q, want a fresh id (prior reset)", got)
		}
		if _, err = gameStore.GetGame(ctx, first.ID); !errors.Is(err, ErrGameNotFound) {
			t.Errorf("prior preview game still exists, GetGame err = %v, want ErrGameNotFound", err)
		}
	})

	t.Run("preview does not bump the quiz play count", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)
		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		draft := newTestQuiz(t)
		draft.Published = false
		if err := quizStore.CreateQuiz(ctx, draft); err != nil {
			t.Fatalf("failed to create draft quiz: %v", err)
		}
		svc := NewService(gameStore, quizStore, slog.Default())

		g, err := svc.CreateGame(ctx, draft.ID, playerID, true)
		if err != nil {
			t.Fatalf("preview CreateGame err = %v, want nil", err)
		}
		// Issue every question so the final one triggers the (guarded)
		// play-count bump, which must be a no-op for a preview game.
		for {
			_, nerr := svc.GetNextQuestion(ctx, g.ID, playerID)
			if errors.Is(nerr, ErrNoMoreQuestions) {
				break
			}
			if nerr != nil {
				t.Fatalf("GetNextQuestion err = %v, want nil", nerr)
			}
		}

		played, err := quizStore.GetQuiz(ctx, draft.ID)
		if err != nil {
			t.Fatalf("GetQuiz err = %v, want nil", err)
		}
		if got, want := played.PlayCount, int64(0); got != want {
			t.Errorf("play_count after preview = %d, want %d", got, want)
		}
	})

	t.Run("preview on a published quiz is rejected and keeps the real game", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)
		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		published := newTestQuiz(t) // Published: true
		if err := quizStore.CreateQuiz(ctx, published); err != nil {
			t.Fatalf("failed to create published quiz: %v", err)
		}
		svc := NewService(gameStore, quizStore, slog.Default())

		// The owner has a real game on the published quiz.
		realGame, err := svc.CreateGame(ctx, published.ID, playerID, false)
		if err != nil {
			t.Fatalf("real CreateGame err = %v, want nil", err)
		}

		// A preview on the published quiz must be refused, and it must NOT
		// hard-delete the existing real game (that was a data-loss bug).
		if _, err = svc.CreateGame(ctx, published.ID, playerID, true); !errors.Is(err, ErrPreviewNotAllowed) {
			t.Errorf("preview on published err = %v, want %v", err, ErrPreviewNotAllowed)
		}
		if _, err = gameStore.GetGame(ctx, realGame.ID); err != nil {
			t.Errorf("real game gone after refused preview: GetGame err = %v, want nil", err)
		}
	})

	t.Run("preview then publish then real play resets the preview game", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)
		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		draft := newTestQuiz(t)
		draft.Published = false
		if err := quizStore.CreateQuiz(ctx, draft); err != nil {
			t.Fatalf("failed to create draft quiz: %v", err)
		}
		svc := NewService(gameStore, quizStore, slog.Default())

		preview, err := svc.CreateGame(ctx, draft.ID, playerID, true)
		if err != nil {
			t.Fatalf("preview CreateGame err = %v, want nil", err)
		}

		if err = quizStore.SetQuizPublished(ctx, draft.ID, true); err != nil {
			t.Fatalf("SetQuizPublished err = %v, want nil", err)
		}

		// The leftover preview game must not block the owner's real attempt.
		realGame, err := svc.CreateGame(ctx, draft.ID, playerID, false)
		if err != nil {
			t.Fatalf("real CreateGame after preview err = %v, want nil", err)
		}
		if realGame.Preview {
			t.Error("real game Preview = true, want false")
		}
		if _, err = gameStore.GetGame(ctx, preview.ID); !errors.Is(err, ErrGameNotFound) {
			t.Errorf("preview game still exists after real play: GetGame err = %v, want ErrGameNotFound", err)
		}
	})

	t.Run("preview of a live quiz is rejected", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)
		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		live := newTestQuiz(t)
		live.Published = false
		live.Mode = quiz.ModeLive
		if err := quizStore.CreateQuiz(ctx, live); err != nil {
			t.Fatalf("failed to create live quiz: %v", err)
		}
		if err := quizStore.SetQuizMode(ctx, live.ID, quiz.ModeLive); err != nil {
			t.Fatalf("SetQuizMode(live) err = %v, want nil", err)
		}
		svc := NewService(gameStore, quizStore, slog.Default())

		_, err := svc.CreateGame(ctx, live.ID, playerID, true)
		if got, want := err, ErrPreviewNotAllowed; !errors.Is(got, want) {
			t.Errorf("preview live CreateGame err = %v, want %v", got, want)
		}
	})
}

func previewFlagOf(t *testing.T, db *sql.DB, gameID string) bool {
	t.Helper()
	var n int64
	if err := db.QueryRowContext(
		t.Context(), "SELECT is_preview FROM games WHERE id = ?", gameID,
	).Scan(&n); err != nil {
		t.Fatalf("read is_preview err = %v, want nil", err)
	}

	return n != 0
}

func TestService_ResetGamesForPlayerOnQuiz(t *testing.T) {
	t.Parallel()

	t.Run("reset clears existing game and lets a fresh CreateGame succeed", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		const playerID = int64(1)
		if _, err := svc.CreateGame(ctx, testQuiz.ID, playerID, false); err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		// Sanity check: GetGameForPlayerOnQuiz finds the game first.
		if _, err := svc.GetGameForPlayerOnQuiz(ctx, playerID, testQuiz.ID); err != nil {
			t.Fatalf("expected game to exist before reset: %v", err)
		}

		if err := svc.ResetGamesForPlayerOnQuiz(ctx, playerID, testQuiz.ID); err != nil {
			t.Fatalf("ResetGamesForPlayerOnQuiz err = %v, want nil", err)
		}

		_, err := svc.GetGameForPlayerOnQuiz(ctx, playerID, testQuiz.ID)
		if got, want := err, ErrGameNotFound; !errors.Is(got, want) {
			t.Errorf("after reset, err = %v, want %v", got, want)
		}

		if _, err = svc.CreateGame(ctx, testQuiz.ID, playerID, false); err != nil {
			t.Errorf("CreateGame after reset err = %v, want nil", err)
		}
	})

	t.Run("idempotent - calling reset twice is fine", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		const playerID = int64(1)
		if _, err := svc.CreateGame(ctx, testQuiz.ID, playerID, false); err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		if err := svc.ResetGamesForPlayerOnQuiz(ctx, playerID, testQuiz.ID); err != nil {
			t.Fatalf("first reset err = %v, want nil", err)
		}
		if err := svc.ResetGamesForPlayerOnQuiz(ctx, playerID, testQuiz.ID); err != nil {
			t.Errorf("second reset err = %v, want nil (reset must be idempotent)", err)
		}
	})

	t.Run("returns ErrQuizNotFound when quiz missing", func(t *testing.T) {
		t.Parallel()

		svc := NewService(stubStore{}, stubQuizStore{
			quizExists: func(_ context.Context, _ int64) (bool, error) {
				return false, nil
			},
		}, slog.New(slog.DiscardHandler))

		err := svc.ResetGamesForPlayerOnQuiz(t.Context(), 1, 999)
		if got, want := err, quiz.ErrQuizNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestService_SubmitAnswer(t *testing.T) {
	t.Parallel()

	t.Run("rejects option from a different question", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		g, err := svc.CreateGame(ctx, testQuiz.ID, 1, false)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		gq, err := svc.GetNextQuestion(ctx, g.ID, 1)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		// Use a correct option from the second question (different from the active question).
		wrongQuestionOption := testQuiz.Questions[1].Options[0] // Berlin, Correct: true, but for question 2

		_, err = svc.SubmitAnswer(ctx, g.ID, 1, gq.QuizQuestion.ID, wrongQuestionOption.ID, time.Time{})
		if got, want := err, ErrOptionNotInQuestion; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("accepts option belonging to the active question", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		g, err := svc.CreateGame(ctx, testQuiz.ID, 1, false)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		gq, err := svc.GetNextQuestion(ctx, g.ID, 1)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		correctOption := testQuiz.Questions[0].Options[0] // Paris, Correct: true

		_, err = svc.SubmitAnswer(ctx, g.ID, 1, gq.QuizQuestion.ID, correctOption.ID, time.Time{})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("rejects an answer that arrives after the window closes", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())
		// Negative reveal delay issues the question already expired.
		svc.SetRevealDelay(-time.Hour)

		g, err := svc.CreateGame(ctx, testQuiz.ID, 1, false)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		gq, err := svc.GetNextQuestion(ctx, g.ID, 1)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		correctOption := testQuiz.Questions[0].Options[0] // Paris, Correct: true

		_, err = svc.SubmitAnswer(ctx, g.ID, 1, gq.QuizQuestion.ID, correctOption.ID, time.Time{})
		if got, want := err, ErrAnswerWindowClosed; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestService_GetResults(t *testing.T) {
	t.Parallel()

	t.Run("returns player with highest score as winner", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		g, err := svc.CreateGame(ctx, testQuiz.ID, 1, false)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		gq, err := svc.GetNextQuestion(ctx, g.ID, 1)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		insertPlayer2 := `INSERT INTO players (id, display_name, email, created_at) VALUES (2, 'player2', 'player2@test.com', CURRENT_TIMESTAMP)`
		if _, err = db.ExecContext(ctx, insertPlayer2); err != nil {
			t.Fatalf("failed to insert player 2: %v", err)
		}
		// Participant gate (#272): player 2 needs an explicit
		// participant row, otherwise SubmitAnswer rejects them as a
		// non-participant. The bug-fix for #272 made the gate strict;
		// pre-fix this test inadvertently relied on the missing check.
		if err = gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: g.ID, PlayerID: 2, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("failed to create participant for player 2: %v", err)
		}

		correctOption := testQuiz.Questions[0].Options[0] // Paris, Correct: true
		wrongOption := testQuiz.Questions[0].Options[1]   // London, Correct: false

		if _, err = svc.SubmitAnswer(ctx, g.ID, 1, gq.QuizQuestion.ID, correctOption.ID, time.Time{}); err != nil {
			t.Fatalf("failed to submit answer for player 1: %v", err)
		}
		if _, err = svc.SubmitAnswer(ctx, g.ID, 2, gq.QuizQuestion.ID, wrongOption.ID, time.Time{}); err != nil {
			t.Fatalf("failed to submit answer for player 2: %v", err)
		}

		results, err := svc.GetResults(ctx, g.ID, 1)
		if err != nil {
			t.Fatalf("failed to get results: %v", err)
		}

		if got, want := results.Winner, int64(1); got != want {
			t.Errorf("Winner = %v, want %v", got, want)
		}
	})

	t.Run("returns no winner on tie", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		g, err := svc.CreateGame(ctx, testQuiz.ID, 1, false)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		gq, err := svc.GetNextQuestion(ctx, g.ID, 1)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		insertPlayer2 := `INSERT INTO players (id, display_name, email, created_at) VALUES (2, 'player2', 'player2@test.com', CURRENT_TIMESTAMP)`
		if _, err = db.ExecContext(ctx, insertPlayer2); err != nil {
			t.Fatalf("failed to insert player 2: %v", err)
		}
		// Participant gate (#272): player 2 needs an explicit
		// participant row, otherwise SubmitAnswer rejects them as a
		// non-participant. The bug-fix for #272 made the gate strict;
		// pre-fix this test inadvertently relied on the missing check.
		if err = gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: g.ID, PlayerID: 2, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("failed to create participant for player 2: %v", err)
		}

		wrongOption := testQuiz.Questions[0].Options[1] // London, Correct: false

		if _, err = svc.SubmitAnswer(ctx, g.ID, 1, gq.QuizQuestion.ID, wrongOption.ID, time.Time{}); err != nil {
			t.Fatalf("failed to submit answer for player 1: %v", err)
		}
		if _, err = svc.SubmitAnswer(ctx, g.ID, 2, gq.QuizQuestion.ID, wrongOption.ID, time.Time{}); err != nil {
			t.Fatalf("failed to submit answer for player 2: %v", err)
		}

		results, err := svc.GetResults(ctx, g.ID, 1)
		if err != nil {
			t.Fatalf("failed to get results: %v", err)
		}

		if got, want := results.Winner, int64(0); got != want {
			t.Errorf("Winner = %v, want %v (expected no winner on tie)", got, want)
		}
	})

	t.Run("sole all-wrong player is not the winner", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		g, err := svc.CreateGame(ctx, testQuiz.ID, 1, false)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		gq, err := svc.GetNextQuestion(ctx, g.ID, 1)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		wrongOption := testQuiz.Questions[0].Options[1] // London, Correct: false
		if _, err = svc.SubmitAnswer(ctx, g.ID, 1, gq.QuizQuestion.ID, wrongOption.ID, time.Time{}); err != nil {
			t.Fatalf("failed to submit wrong answer: %v", err)
		}

		results, err := svc.GetResults(ctx, g.ID, 1)
		if err != nil {
			t.Fatalf("failed to get results: %v", err)
		}

		if got, want := results.Winner, int64(0); got != want {
			t.Errorf("Winner = %v, want %v (a 0-score all-wrong run has no winner)", got, want)
		}
		if got, want := results.PlayerScores[1], 0; got != want {
			t.Errorf("PlayerScores[1] = %v, want %v", got, want)
		}
	})

	t.Run("skips answers whose option was deleted without panicking", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()

		// A finished game with one answer that references option 99,
		// which GetOptionsByIDs no longer returns (the option row was
		// deleted out from under the answer). The score loop must skip
		// it rather than dereference a nil Option.
		gameWithDanglingAnswer := &Game{
			ID:           "game-deleted-opt",
			Participants: []*Participant{{PlayerID: 1}},
			Questions: []*Question{
				{
					ID: 1,
					Answers: []*Answer{
						{PlayerID: 1, OptionID: 99, AnsweredAt: time.Now()},
					},
				},
			},
		}

		gs := stubStore{
			getGame: func(_ context.Context, _ string) (*Game, error) {
				return gameWithDanglingAnswer, nil
			},
		}
		qs := stubQuizStore{
			getOptionsByIDs: func(_ context.Context, _ []int64) ([]*quiz.Option, error) {
				return nil, nil
			},
		}

		svc := NewService(gs, qs, slog.Default())

		results, err := svc.GetResults(ctx, "game-deleted-opt", 1)
		if err != nil {
			t.Fatalf("GetResults err = %v, want nil", err)
		}
		if got, want := results.PlayerScores[1], 0; got != want {
			t.Errorf("PlayerScores[1] = %d, want %d", got, want)
		}
	})
}

func TestService_GetNextQuestion(t *testing.T) {
	t.Parallel()

	t.Run("first question", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()

		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)

		var err error
		err = quizStore.CreateQuiz(ctx, testQuiz)
		if err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		testGame := newTestGame(t, testQuiz)
		err = gameStore.CreateGame(ctx, testGame)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}
		// Participant gate (#272): the service rejects callers that
		// aren't on the participant list. These tests bypass the
		// service's CreateGame, so the participant row has to be
		// seeded explicitly here.
		if err = gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("failed to create participant: %v", err)
		}

		service := NewService(gameStore, quizStore, slog.Default())
		gq, err := service.GetNextQuestion(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		if gq == nil {
			t.Fatal("expected gq to be non-nil")

			return
		}

		if cmp.Diff(gq.QuizQuestion, testQuiz.Questions[0]) != "" {
			t.Errorf("got qs: %+v, want %+v", gq.QuizQuestion, testQuiz.Questions[0])
		}
	})

	t.Run("second question", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()

		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)

		var err error
		err = quizStore.CreateQuiz(ctx, testQuiz)
		if err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		testGame := newTestGame(t, testQuiz)
		err = gameStore.CreateGame(ctx, testGame)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}
		// Participant gate (#272): the service rejects callers that
		// aren't on the participant list. These tests bypass the
		// service's CreateGame, so the participant row has to be
		// seeded explicitly here.
		if err = gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("failed to create participant: %v", err)
		}

		err = gameStore.CreateQuestion(ctx, &Question{GameID: testGame.ID, QuestionID: testQuiz.Questions[0].ID}, false)
		if err != nil {
			t.Fatalf("failed to create game question: %v", err)
		}

		service := NewService(gameStore, quizStore, slog.Default())
		gq, err := service.GetNextQuestion(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		if gq == nil {
			t.Fatal("expected gq to be non-nil")

			return
		}

		if cmp.Diff(gq.QuizQuestion, testQuiz.Questions[1]) != "" {
			t.Errorf("got qs: %+v, want %+v", gq.QuizQuestion, testQuiz.Questions[1])
		}
	})

	t.Run("started_at sits in the future to honour the reveal delay", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		// Participant gate (#272): seed the participant directly since
		// these tests bypass Service.CreateGame.
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		service := NewService(gameStore, quizStore, slog.Default())
		issuedAt := time.Now()
		gq, err := service.GetNextQuestion(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("GetNextQuestion err = %v, want nil", err)
		}

		// StartedAt must be at least 2 seconds in the future relative
		// to issuedAt - the 3s reveal delay (#247) gives the player
		// time to read the question before the answer window opens.
		// 2s lower bound is forgiving of clock granularity on the
		// test machine; the production constant is 3s.
		if got, lower := gq.StartedAt.Sub(issuedAt), 2*time.Second; got < lower {
			t.Errorf("StartedAt - issuedAt = %v, want >= %v (reveal delay)", got, lower)
		}
		// ExpiredAt sits one answer window further. The window is 10s
		// (the unexported defaultExpiration constant); duplicated here
		// as a literal so this external_test file doesn't have to
		// reach into package internals.
		if got, want := gq.ExpiredAt.Sub(gq.StartedAt), 10*time.Second; got != want {
			t.Errorf("ExpiredAt - StartedAt = %v, want %v", got, want)
		}
	})

	t.Run("back-to-back calls return the same in-flight question", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		// Participant gate (#272): seed the participant directly since
		// these tests bypass Service.CreateGame.
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		service := NewService(gameStore, quizStore, slog.Default())
		first, err := service.GetNextQuestion(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("first GetNextQuestion err = %v, want nil", err)
		}

		// Second call without submitting an answer must return the same
		// game_questions row - same ID, same timing anchors - so a
		// mid-question reload doesn't skip the question.
		second, err := service.GetNextQuestion(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("second GetNextQuestion err = %v, want nil", err)
		}
		if got, want := second.ID, first.ID; got != want {
			t.Errorf("second.ID = %d, want %d (resume must hand back same row)", got, want)
		}
		if got, want := second.QuizQuestion.ID, first.QuizQuestion.ID; got != want {
			t.Errorf("second.QuizQuestion.ID = %d, want %d", got, want)
		}
		if got, want := second.StartedAt, first.StartedAt; !got.Equal(want) {
			t.Errorf("second.StartedAt = %v, want %v (timing anchor must not reset)", got, want)
		}
		if got, want := second.ExpiredAt, first.ExpiredAt; !got.Equal(want) {
			t.Errorf("second.ExpiredAt = %v, want %v", got, want)
		}

		// And no extra game_questions row was inserted.
		g, err := gameStore.GetGame(ctx, testGame.ID)
		if err != nil {
			t.Fatalf("GetGame err = %v, want nil", err)
		}
		if got, want := len(g.Questions), 1; got != want {
			t.Errorf("game_questions count = %d, want %d (resume must not insert)", got, want)
		}
	})

	t.Run("advance after an expired unanswered question", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		// Seed an unanswered game_question whose answer window has
		// already closed - the timeout path leaves rows like this.
		// The advance branch must move past it instead of pinning the
		// player on the expired question.
		past := time.Now().Add(-1 * time.Minute)
		expired := &Question{
			GameID:     testGame.ID,
			QuestionID: testQuiz.Questions[0].ID,
			StartedAt:  past,
			ExpiredAt:  past.Add(10 * time.Second),
		}
		if err := gameStore.CreateQuestion(ctx, expired, false); err != nil {
			t.Fatalf("CreateQuestion err = %v, want nil", err)
		}

		service := NewService(gameStore, quizStore, slog.Default())
		gq, err := service.GetNextQuestion(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("GetNextQuestion err = %v, want nil", err)
		}
		if got, want := gq.QuizQuestion.ID, testQuiz.Questions[1].ID; got != want {
			t.Errorf("advanced to QuizQuestion.ID = %d, want %d (expired Q1 must not pin)", got, want)
		}
	})

	t.Run("SetRevealDelay shrinks the reveal-to-answer gap", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		// Participant gate (#272): seed the participant directly since
		// these tests bypass Service.CreateGame.
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		// Sub-second reveal mirrors the e2e config: shorter than the
		// default 3s but still leaves the reveal phase observable.
		service := NewService(gameStore, quizStore, slog.Default())
		service.SetRevealDelay(200 * time.Millisecond)
		issuedAt := time.Now()
		gq, err := service.GetNextQuestion(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("GetNextQuestion err = %v, want nil", err)
		}

		// StartedAt should sit close to (issuedAt + 200ms). Generous
		// upper bound to absorb scheduler jitter on busy CI runners.
		if got, upper := gq.StartedAt.Sub(issuedAt), 1*time.Second; got >= upper {
			t.Errorf("StartedAt - issuedAt = %v, want < %v (override should shrink reveal)", got, upper)
		}
	})
}

// firstRoundID returns the id of the only round a freshly created quiz
// has - the default "Round 1" the store stamps on CreateQuiz (#444).
func firstRoundID(t *testing.T, qz *quiz.Quiz, quizStore *store.QuizStore) int64 {
	t.Helper()
	rounds, err := quizStore.ListRoundsByQuiz(t.Context(), qz.ID)
	if err != nil {
		t.Fatalf("ListRoundsByQuiz err = %v, want nil", err)
	}
	if len(rounds) == 0 {
		t.Fatal("quiz has no rounds, want at least the default")
	}

	return rounds[0].ID
}

// giveRoundSummary stamps a summary on an existing round so its boundary
// fires during play. The play iterator skips a round whose summary is
// empty (#444), so boundary-emission tests must author one first.
func giveRoundSummary(t *testing.T, quizStore *store.QuizStore, roundID int64, summary string) {
	t.Helper()
	round, err := quizStore.GetRound(t.Context(), roundID)
	if err != nil {
		t.Fatalf("GetRound err = %v, want nil", err)
	}
	round.Summary = summary
	if err := quizStore.UpdateRound(t.Context(), round); err != nil {
		t.Fatalf("UpdateRound err = %v, want nil", err)
	}
}

func TestService_GetNext(t *testing.T) {
	t.Parallel()

	t.Run("returns the first question of the first round", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())
		item, err := svc.GetNext(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("GetNext err = %v, want nil", err)
		}
		if got, want := item.Type, ItemTypeQuestion; got != want {
			t.Errorf("item.Type = %q, want %q", got, want)
		}
		if got, want := item.Question.QuizQuestion.ID, testQuiz.Questions[0].ID; got != want {
			t.Errorf("item.Question.QuizQuestion.ID = %d, want %d", got, want)
		}
	})

	t.Run("emits the results boundary after every question in the round is issued", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}
		roundID := firstRoundID(t, testQuiz, quizStore)
		giveRoundSummary(t, quizStore, roundID, "Round one wrapped up")

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		// Issue every question and mark the intro phase seen so the only
		// remaining item is the round's results boundary.
		for _, q := range testQuiz.Questions {
			if err := gameStore.CreateQuestion(
				ctx, &Question{GameID: testGame.ID, QuestionID: q.ID}, false,
			); err != nil {
				t.Fatalf("CreateQuestion err = %v, want nil", err)
			}
		}
		if err := gameStore.MarkRoundSeen(ctx, testGame.ID, roundID, RoundPhaseIntro); err != nil {
			t.Fatalf("MarkRoundSeen (intro) err = %v, want nil", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())
		item, err := svc.GetNext(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("GetNext err = %v, want nil", err)
		}
		if got, want := item.Type, ItemTypeRoundBoundary; got != want {
			t.Errorf("item.Type = %q, want %q", got, want)
		}
		if got, want := item.Phase, RoundPhaseResults; got != want {
			t.Errorf("item.Phase = %q, want %q", got, want)
		}
		if got, want := item.Round.ID, roundID; got != want {
			t.Errorf("item.Round.ID = %d, want %d", got, want)
		}
		if got, want := item.Total, len(testQuiz.Questions); got != want {
			t.Errorf("item.Total = %d, want %d", got, want)
		}
		if got, want := item.RoundQuestions, len(testQuiz.Questions); got != want {
			t.Errorf("item.RoundQuestions = %d, want %d", got, want)
		}
		assertBoundaryWindow(t, item, testQuiz.TimeLimitSeconds)
	})

	t.Run("emits the intro boundary before the round's first question", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}
		roundID := firstRoundID(t, testQuiz, quizStore)
		giveRoundSummary(t, quizStore, roundID, "Round one ahead")

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())
		item, err := svc.GetNext(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("GetNext err = %v, want nil", err)
		}
		if got, want := item.Type, ItemTypeRoundBoundary; got != want {
			t.Errorf("item.Type = %q, want %q", got, want)
		}
		if got, want := item.Phase, RoundPhaseIntro; got != want {
			t.Errorf("item.Phase = %q, want %q", got, want)
		}
		if got, want := item.Round.Summary, "Round one ahead"; got != want {
			t.Errorf("item.Round.Summary = %q, want %q", got, want)
		}
		assertBoundaryWindow(t, item, testQuiz.TimeLimitSeconds)
	})

	t.Run("skips a round boundary the player has already seen", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}
		roundID := firstRoundID(t, testQuiz, quizStore)
		giveRoundSummary(t, quizStore, roundID, "Round one wrapped up")

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}
		for _, q := range testQuiz.Questions {
			if err := gameStore.CreateQuestion(
				ctx, &Question{GameID: testGame.ID, QuestionID: q.ID}, false,
			); err != nil {
				t.Fatalf("CreateQuestion err = %v, want nil", err)
			}
		}
		if err := gameStore.MarkRoundSeen(ctx, testGame.ID, roundID, RoundPhaseIntro); err != nil {
			t.Fatalf("MarkRoundSeen (intro) err = %v, want nil", err)
		}
		if err := gameStore.MarkRoundSeen(ctx, testGame.ID, roundID, RoundPhaseResults); err != nil {
			t.Fatalf("MarkRoundSeen (results) err = %v, want nil", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())
		_, err := svc.GetNext(ctx, testGame.ID, 1)
		if got, want := err, ErrNoMoreQuestions; !errors.Is(got, want) {
			t.Errorf("GetNext err = %v, want %v (all questions issued, both phases seen)", got, want)
		}
	})

	t.Run("walks rounds in position order issuing each round's questions then its boundary", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		// Two rounds: round 1 holds Q1, round 2 holds Q2, each with a
		// summary. The walk should be intro(r1), Q1, results(r1),
		// intro(r2), Q2, results(r2). Each question is answered before
		// advancing so the resume path does not hand the same in-flight
		// question back; each boundary phase is acked so it is not
		// re-emitted.
		testQuiz := &quiz.Quiz{
			Title:             "Two rounds",
			Slug:              "two-rounds",
			CreatedByPlayerID: seededAdminID,
			Published:         true,
			Questions: []*quiz.Question{
				{Text: "Q1", Position: 10, Options: []*quiz.Option{{Text: "a", Correct: true}, {Text: "b"}}},
				{Text: "Q2", Position: 20, Options: []*quiz.Option{{Text: "c", Correct: true}, {Text: "d"}}},
			},
		}
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}
		round1 := firstRoundID(t, testQuiz, quizStore)
		giveRoundSummary(t, quizStore, round1, "Round one wrapped up")
		round2 := &quiz.Round{QuizID: testQuiz.ID, Position: 1, Title: "Round 2", Summary: "Round two wrapped up"}
		if err := quizStore.CreateRound(ctx, round2); err != nil {
			t.Fatalf("CreateRound err = %v, want nil", err)
		}
		if err := quizStore.MoveQuestionToRound(ctx, testQuiz.ID, testQuiz.Questions[1].ID, round2.ID); err != nil {
			t.Fatalf("MoveQuestionToRound err = %v, want nil", err)
		}

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		answer := func(item *Item) {
			t.Helper()
			optID := item.Question.QuizQuestion.Options[0].ID
			if _, err := svc.SubmitAnswer(
				ctx, testGame.ID, 1, item.Question.QuizQuestion.ID, optID, time.Now(),
			); err != nil {
				t.Fatalf("SubmitAnswer err = %v, want nil", err)
			}
		}

		nextBoundary := func(label string, roundID int64, phase RoundPhase) {
			t.Helper()
			item, err := svc.GetNext(ctx, testGame.ID, 1)
			if err != nil {
				t.Fatalf("GetNext (%s) err = %v, want nil", label, err)
			}
			if got, want := item.Type, ItemTypeRoundBoundary; got != want {
				t.Fatalf("%s item.Type = %q, want %q", label, got, want)
			}
			if got, want := item.Phase, phase; got != want {
				t.Fatalf("%s item.Phase = %q, want %q", label, got, want)
			}
			if got, want := item.Round.ID, roundID; got != want {
				t.Errorf("%s boundary round = %d, want %d", label, got, want)
			}
			if err = svc.MarkRoundSeen(ctx, testGame.ID, 1, roundID, phase); err != nil {
				t.Fatalf("MarkRoundSeen (%s) err = %v, want nil", label, err)
			}
		}
		nextQuestion := func(label string, wantQuestionID int64) {
			t.Helper()
			item, err := svc.GetNext(ctx, testGame.ID, 1)
			if err != nil {
				t.Fatalf("GetNext (%s) err = %v, want nil", label, err)
			}
			if got, want := item.Type, ItemTypeQuestion; got != want {
				t.Fatalf("%s item.Type = %q, want %q", label, got, want)
			}
			if got, want := item.Question.QuizQuestion.ID, wantQuestionID; got != want {
				t.Errorf("%s question = %d, want %d", label, got, want)
			}
			answer(item)
		}

		nextBoundary("intro 1", round1, RoundPhaseIntro)
		nextQuestion("Q1", testQuiz.Questions[0].ID)
		nextBoundary("results 1", round1, RoundPhaseResults)
		nextBoundary("intro 2", round2.ID, RoundPhaseIntro)
		nextQuestion("Q2", testQuiz.Questions[1].ID)
		nextBoundary("results 2", round2.ID, RoundPhaseResults)

		// exhausted
		_, err := svc.GetNext(ctx, testGame.ID, 1)
		if got, want := err, ErrNoMoreQuestions; !errors.Is(got, want) {
			t.Errorf("final GetNext err = %v, want %v", got, want)
		}
	})

	t.Run("resume returns the in-flight question unchanged", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		first, err := svc.GetNext(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("first GetNext err = %v, want nil", err)
		}
		second, err := svc.GetNext(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("second GetNext err = %v, want nil", err)
		}
		if got, want := second.Type, ItemTypeQuestion; got != want {
			t.Fatalf("second.Type = %q, want %q", got, want)
		}
		if got, want := second.Question.QuestionID, first.Question.QuestionID; got != want {
			t.Errorf("resume question = %d, want %d (must hand back the same in-flight question)", got, want)
		}
	})
}

func TestService_MarkRoundSeen(t *testing.T) {
	t.Parallel()

	t.Run("idempotent", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}
		roundID := firstRoundID(t, testQuiz, quizStore)

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())
		if err := svc.MarkRoundSeen(ctx, testGame.ID, 1, roundID, RoundPhaseIntro); err != nil {
			t.Errorf("first MarkRoundSeen err = %v, want nil", err)
		}
		if err := svc.MarkRoundSeen(ctx, testGame.ID, 1, roundID, RoundPhaseIntro); err != nil {
			t.Errorf("second MarkRoundSeen err = %v, want nil (idempotent)", err)
		}
	})

	t.Run("unknown phase returns ErrInvalidRoundPhase", func(t *testing.T) {
		t.Parallel()

		svc := NewService(stubStore{}, stubQuizStore{}, slog.Default())
		err := svc.MarkRoundSeen(t.Context(), "game-1", 1, 1, RoundPhase("bogus"))
		if got, want := err, ErrInvalidRoundPhase; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("non-participant returns ErrGameNotFound", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}
		roundID := firstRoundID(t, testQuiz, quizStore)

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())
		err := svc.MarkRoundSeen(ctx, testGame.ID, 999, roundID, RoundPhaseIntro)
		if got, want := err, ErrGameNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("round from a different quiz returns ErrRoundNotFound", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}

		quizB := &quiz.Quiz{Title: "Other", Slug: "other", CreatedByPlayerID: seededAdminID}
		if err := quizStore.CreateQuiz(ctx, quizB); err != nil {
			t.Fatalf("CreateQuiz B err = %v, want nil", err)
		}
		groupOnB := firstRoundID(t, quizB, quizStore)

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())
		err := svc.MarkRoundSeen(ctx, testGame.ID, 1, groupOnB, RoundPhaseIntro)
		if got, want := err, quiz.ErrRoundNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

// TestService_CreateGame_Race covers #273: two concurrent
// Service.CreateGame calls for the same (player, quiz) must produce
// exactly one game. Before the fix, the service did a check-then-insert
// without a transaction or a DB-level constraint, so both calls could
// pass the existence check and both insert. The fix denormalises
// quiz_id onto game_participants and adds a UNIQUE INDEX on
// (player_id, quiz_id); the loser of the race surfaces as
// ErrGameAlreadyExists from CreateParticipant.
func TestService_CreateGame_Race(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)

	qz := &quiz.Quiz{
		Title:             "Race Quiz",
		Slug:              "race-quiz",
		Description:       "for the create-game race test",
		CreatedByPlayerID: seededAdminID,
		Published:         true,
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
			g, gerr := svc.CreateGame(context.Background(), qz.ID, player.ID, false)
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

// TestService_GetNextQuestion_Race pins the UNIQUE INDEX on
// game_questions(game_id, question_id): N concurrent /next calls on the same
// game must produce exactly one game_questions row, not N. Without the index +
// ON CONFLICT handling, a double-tap on "Next" would insert duplicate rows,
// re-serving the same question and inflating play_count.
func TestService_GetNextQuestion_Race(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)

	qz := &quiz.Quiz{
		Title:             "Next Race Quiz",
		Slug:              "next-race-quiz",
		Description:       "for the concurrent /next race test",
		CreatedByPlayerID: seededAdminID,
		Published:         true,
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

	player, err := stores.Players.CreateAnonymousPlayer(ctx, "anon-next-race")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	svc := NewService(stores.Games, stores.Quizzes, slog.Default())
	svc.SetLeaderboardPublisher(leaderboard.NewHub())

	game, err := svc.CreateGame(ctx, qz.ID, player.ID, false)
	if err != nil {
		t.Fatalf("CreateGame err = %v, want nil", err)
	}

	const parallel = 4
	var wg sync.WaitGroup
	wg.Add(parallel)
	results := make([]*Question, parallel)
	errs := make([]error, parallel)

	for i := range parallel {
		go func(idx int) {
			defer wg.Done()
			gq, gerr := svc.GetNextQuestion(context.Background(), game.ID, player.ID)
			results[idx] = gq
			errs[idx] = gerr
		}(i)
	}
	wg.Wait()

	for i, gerr := range errs {
		if gerr != nil {
			t.Errorf("goroutine %d err = %v, want nil", i, gerr)
		}
		if results[i] == nil {
			t.Errorf("goroutine %d returned nil question", i)

			continue
		}
		if got, want := results[i].QuestionID, qz.Questions[0].ID; got != want {
			t.Errorf("goroutine %d QuestionID = %d, want %d", i, got, want)
		}
	}

	// The core assertion: exactly one game_questions row for (game, question).
	// Without the UNIQUE index, the race would produce up to `parallel` rows.
	rowCount := countGameQuestionRows(ctx, t, db, game.ID, qz.Questions[0].ID)
	if got, want := rowCount, 1; got != want {
		t.Errorf("game_questions rows for (game=%s, question=%d) = %d, want %d",
			game.ID, qz.Questions[0].ID, got, want)
	}
}

// countGameQuestionRows pulls the row count for a (game_id, question_id) pair
// directly from the DB. The UNIQUE INDEX should make this either 0 or 1.
func countGameQuestionRows(ctx context.Context, t *testing.T, db *sql.DB, gameID string, questionID int64) int {
	t.Helper()
	row := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM game_questions WHERE game_id = ? AND question_id = ?`,
		gameID, questionID,
	)
	var n int
	if err := row.Scan(&n); err != nil {
		t.Fatalf("QueryRow.Scan err = %v, want nil", err)
	}

	return n
}
