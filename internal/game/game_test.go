package game_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"

	"github.com/starquake/topbanana/internal/dbtest"
	. "github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

var errStub = errors.New("stub error")

// stubStore satisfies game.Store for service-level tests that do not need a
// live database. Each behaviour is overridable per test via a func field; a
// nil field returns errStub so accidental use surfaces loudly. The
// getGameByPlayerAndQuiz field defaults to "not found" rather than errStub
// so the existing CreateGame happy-path tests do not have to opt in.
type stubStore struct {
	listAnswersForQuizLeaderboard func(ctx context.Context, quizID int64) ([]*LeaderboardAnswer, error)
	getGameByPlayerAndQuiz        func(ctx context.Context, playerID, quizID int64) (*Game, error)
	deleteGamesForPlayerOnQuiz    func(ctx context.Context, playerID, quizID int64) error
	listQuizIDsForPlayer          func(ctx context.Context, playerID int64) ([]int64, error)
}

func (stubStore) Ping(_ context.Context) error { return nil }

func (stubStore) GetGame(_ context.Context, _ string) (*Game, error) {
	return nil, errStub
}

func (s stubStore) GetGameByPlayerAndQuiz(
	ctx context.Context, playerID, quizID int64,
) (*Game, error) {
	if s.getGameByPlayerAndQuiz == nil {
		return nil, ErrGameNotFound
	}

	return s.getGameByPlayerAndQuiz(ctx, playerID, quizID)
}
func (stubStore) CreateGame(_ context.Context, _ *Game) error               { return errStub }
func (stubStore) StartGame(_ context.Context, _ string) error               { return errStub }
func (stubStore) CreateParticipant(_ context.Context, _ *Participant) error { return errStub }
func (stubStore) CreateQuestion(_ context.Context, _ *Question) error       { return errStub }
func (stubStore) CreateAnswer(_ context.Context, _ *Answer) error           { return errStub }

func (s stubStore) ListAnswersForQuizLeaderboard(
	ctx context.Context, quizID int64,
) ([]*LeaderboardAnswer, error) {
	if s.listAnswersForQuizLeaderboard == nil {
		return nil, errStub
	}

	return s.listAnswersForQuizLeaderboard(ctx, quizID)
}

func (s stubStore) DeleteGamesForPlayerOnQuiz(
	ctx context.Context, playerID, quizID int64,
) error {
	if s.deleteGamesForPlayerOnQuiz == nil {
		return errStub
	}

	return s.deleteGamesForPlayerOnQuiz(ctx, playerID, quizID)
}

func (s stubStore) ListQuizIDsForPlayer(ctx context.Context, playerID int64) ([]int64, error) {
	if s.listQuizIDsForPlayer == nil {
		return nil, nil
	}

	return s.listQuizIDsForPlayer(ctx, playerID)
}

// stubQuizStore satisfies quiz.Store for service-level tests. Only GetQuiz
// and QuizExists are overridable since the leaderboard/reset paths never
// reach the other methods.
type stubQuizStore struct {
	getQuiz    func(ctx context.Context, id int64) (*quiz.Quiz, error)
	quizExists func(ctx context.Context, id int64) (bool, error)
}

func (stubQuizStore) Ping(_ context.Context) error                        { return nil }
func (stubQuizStore) ListQuizzes(_ context.Context) ([]*quiz.Quiz, error) { return nil, nil }
func (stubQuizStore) QuestionCountsByQuiz(_ context.Context) (map[int64]int, error) {
	return map[int64]int{}, nil
}

func (s stubQuizStore) GetQuiz(ctx context.Context, id int64) (*quiz.Quiz, error) {
	if s.getQuiz == nil {
		return nil, errStub
	}

	return s.getQuiz(ctx, id)
}

func (s stubQuizStore) QuizExists(ctx context.Context, id int64) (bool, error) {
	if s.quizExists == nil {
		return false, errStub
	}

	return s.quizExists(ctx, id)
}
func (stubQuizStore) CreateQuiz(_ context.Context, _ *quiz.Quiz) error         { return nil }
func (stubQuizStore) UpdateQuiz(_ context.Context, _ *quiz.Quiz) error         { return nil }
func (stubQuizStore) DeleteQuiz(_ context.Context, _ int64) error              { return nil }
func (stubQuizStore) CreateQuestion(_ context.Context, _ *quiz.Question) error { return nil }
func (stubQuizStore) UpdateQuestion(_ context.Context, _ *quiz.Question) error { return nil }
func (stubQuizStore) NextQuestionPosition(_ context.Context, _ int64) (int, error) {
	return 0, errStub
}

func (stubQuizStore) SwapQuestionPositions(_ context.Context, _, _ int64, _ string) error {
	return errStub
}
func (stubQuizStore) DeleteQuestion(_ context.Context, _ int64) error { return nil }
func (stubQuizStore) ListQuestions(_ context.Context, _ int64) ([]*quiz.Question, error) {
	return nil, nil
}

func (stubQuizStore) GetQuestion(_ context.Context, _ int64) (*quiz.Question, error) {
	return nil, errStub
}

func (stubQuizStore) GetOption(_ context.Context, _ int64) (*quiz.Option, error) {
	return nil, errStub
}

func (stubQuizStore) GetOptionsByIDs(_ context.Context, _ []int64) ([]*quiz.Option, error) {
	return nil, nil
}

func newTestQuiz(t *testing.T) *quiz.Quiz {
	t.Helper()

	return &quiz.Quiz{
		Title: "Flurpsydurpsy",
		Slug:  "flurpsydurpsy",
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

func newTestGame(t *testing.T, qz *quiz.Quiz) *Game {
	t.Helper()

	return &Game{
		QuizID: qz.ID,
	}
}

func TestGame_IsCompleted(t *testing.T) {
	t.Parallel()

	t.Run("true when issued questions match quiz length", func(t *testing.T) {
		t.Parallel()

		g := &Game{
			Quiz: &quiz.Quiz{
				Questions: []*quiz.Question{{ID: 1}, {ID: 2}},
			},
			Questions: []*Question{
				{QuestionID: 1},
				{QuestionID: 2},
			},
		}
		if got, want := g.IsCompleted(), true; got != want {
			t.Errorf("IsCompleted() = %v, want %v", got, want)
		}
	})

	t.Run("false when fewer questions issued than quiz length", func(t *testing.T) {
		t.Parallel()

		g := &Game{
			Quiz: &quiz.Quiz{
				Questions: []*quiz.Question{{ID: 1}, {ID: 2}, {ID: 3}},
			},
			Questions: []*Question{
				{QuestionID: 1},
			},
		}
		if got, want := g.IsCompleted(), false; got != want {
			t.Errorf("IsCompleted() = %v, want %v", got, want)
		}
	})

	t.Run("false when zero questions issued and quiz has some", func(t *testing.T) {
		t.Parallel()

		g := &Game{
			Quiz: &quiz.Quiz{
				Questions: []*quiz.Question{{ID: 1}},
			},
		}
		if got, want := g.IsCompleted(), false; got != want {
			t.Errorf("IsCompleted() = %v, want %v", got, want)
		}
	})

	t.Run("false when quiz is not populated", func(t *testing.T) {
		t.Parallel()

		g := &Game{
			Questions: []*Question{{QuestionID: 1}},
		}
		if got, want := g.IsCompleted(), false; got != want {
			t.Errorf("IsCompleted() = %v, want %v (without Quiz the game cannot be known to be complete)", got, want)
		}
	})
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
		created, err := svc.CreateGame(ctx, testQuiz.ID, playerID)
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
	if _, err := svc.CreateGame(ctx, testQuiz.ID, playerID); err != nil {
		t.Fatalf("failed to create initial game: %v", err)
	}

	// Second attempt for the same (player, quiz) must be rejected.
	_, err := svc.CreateGame(ctx, testQuiz.ID, playerID)
	if got, want := err, ErrGameAlreadyExists; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
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
		if _, err := svc.CreateGame(ctx, testQuiz.ID, playerID); err != nil {
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

		if _, err = svc.CreateGame(ctx, testQuiz.ID, playerID); err != nil {
			t.Errorf("CreateGame after reset err = %v, want nil", err)
		}
	})

	t.Run("idempotent — calling reset twice is fine", func(t *testing.T) {
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
		if _, err := svc.CreateGame(ctx, testQuiz.ID, playerID); err != nil {
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

		g, err := svc.CreateGame(ctx, testQuiz.ID, 1)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		gq, err := svc.GetNextQuestion(ctx, g.ID, 1)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		// Use a correct option from the second question (different from the active question).
		wrongQuestionOption := testQuiz.Questions[1].Options[0] // Berlin, Correct: true, but for question 2

		_, err = svc.SubmitAnswer(ctx, g.ID, 1, gq.QuizQuestion.ID, wrongQuestionOption.ID)
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

		g, err := svc.CreateGame(ctx, testQuiz.ID, 1)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		gq, err := svc.GetNextQuestion(ctx, g.ID, 1)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		correctOption := testQuiz.Questions[0].Options[0] // Paris, Correct: true

		_, err = svc.SubmitAnswer(ctx, g.ID, 1, gq.QuizQuestion.ID, correctOption.ID)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
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

		g, err := svc.CreateGame(ctx, testQuiz.ID, 1)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		gq, err := svc.GetNextQuestion(ctx, g.ID, 1)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		insertPlayer2 := `INSERT INTO players (id, username, email, created_at) VALUES (2, 'player2', 'player2@test.com', CURRENT_TIMESTAMP)`
		if _, err = db.ExecContext(ctx, insertPlayer2); err != nil {
			t.Fatalf("failed to insert player 2: %v", err)
		}
		// Participant gate (#272): player 2 needs an explicit
		// participant row, otherwise SubmitAnswer rejects them as a
		// non-participant. The bug-fix for #272 made the gate strict;
		// pre-fix this test inadvertently relied on the missing check.
		if err = gameStore.CreateParticipant(ctx, &Participant{GameID: g.ID, PlayerID: 2}); err != nil {
			t.Fatalf("failed to create participant for player 2: %v", err)
		}

		correctOption := testQuiz.Questions[0].Options[0] // Paris, Correct: true
		wrongOption := testQuiz.Questions[0].Options[1]   // London, Correct: false

		if _, err = svc.SubmitAnswer(ctx, g.ID, 1, gq.QuizQuestion.ID, correctOption.ID); err != nil {
			t.Fatalf("failed to submit answer for player 1: %v", err)
		}
		if _, err = svc.SubmitAnswer(ctx, g.ID, 2, gq.QuizQuestion.ID, wrongOption.ID); err != nil {
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

		g, err := svc.CreateGame(ctx, testQuiz.ID, 1)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		gq, err := svc.GetNextQuestion(ctx, g.ID, 1)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		insertPlayer2 := `INSERT INTO players (id, username, email, created_at) VALUES (2, 'player2', 'player2@test.com', CURRENT_TIMESTAMP)`
		if _, err = db.ExecContext(ctx, insertPlayer2); err != nil {
			t.Fatalf("failed to insert player 2: %v", err)
		}
		// Participant gate (#272): player 2 needs an explicit
		// participant row, otherwise SubmitAnswer rejects them as a
		// non-participant. The bug-fix for #272 made the gate strict;
		// pre-fix this test inadvertently relied on the missing check.
		if err = gameStore.CreateParticipant(ctx, &Participant{GameID: g.ID, PlayerID: 2}); err != nil {
			t.Fatalf("failed to create participant for player 2: %v", err)
		}

		wrongOption := testQuiz.Questions[0].Options[1] // London, Correct: false

		if _, err = svc.SubmitAnswer(ctx, g.ID, 1, gq.QuizQuestion.ID, wrongOption.ID); err != nil {
			t.Fatalf("failed to submit answer for player 1: %v", err)
		}
		if _, err = svc.SubmitAnswer(ctx, g.ID, 2, gq.QuizQuestion.ID, wrongOption.ID); err != nil {
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
		if err = gameStore.CreateParticipant(ctx, &Participant{GameID: testGame.ID, PlayerID: 1}); err != nil {
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
		if err = gameStore.CreateParticipant(ctx, &Participant{GameID: testGame.ID, PlayerID: 1}); err != nil {
			t.Fatalf("failed to create participant: %v", err)
		}

		err = gameStore.CreateQuestion(ctx, &Question{GameID: testGame.ID, QuestionID: testQuiz.Questions[0].ID})
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
		if err := gameStore.CreateParticipant(ctx, &Participant{GameID: testGame.ID, PlayerID: 1}); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		service := NewService(gameStore, quizStore, slog.Default())
		issuedAt := time.Now()
		gq, err := service.GetNextQuestion(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("GetNextQuestion err = %v, want nil", err)
		}

		// StartedAt must be at least 2 seconds in the future relative
		// to issuedAt — the 3s reveal delay (#247) gives the player
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
		if err := gameStore.CreateParticipant(ctx, &Participant{GameID: testGame.ID, PlayerID: 1}); err != nil {
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

func TestService_CalculateScore_EarlyAnswerClamps(t *testing.T) {
	t.Parallel()

	// Hand-crafted client could POST an answer before StartedAt (which
	// sits in the future during the reveal delay — #247). The clamp
	// in CalculateScore must treat the answer as arriving AT
	// StartedAt rather than producing a score above maxPoints from a
	// negative duration.
	svc := NewService(stubStore{}, stubQuizStore{}, slog.New(slog.DiscardHandler))

	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	a := &Answer{
		AnsweredAt: start.Add(-1 * time.Second), // 1s before the reveal lands
		Question: &Question{
			StartedAt: start,
			ExpiredAt: start.Add(10 * time.Second),
		},
		Option: &quiz.Option{Correct: true},
	}

	if got, want := svc.CalculateScore(t.Context(), a), 1000; got != want {
		t.Errorf("CalculateScore for AnsweredAt - StartedAt = -1s, got %d, want %d (clamped to maxPoints)", got, want)
	}
}

// makeAnswer produces a flat LeaderboardAnswer answered at the start of the
// 10s answer window (matching defaultExpiration) so CalculateScore yields a
// predictable maxPoints (1000) for a correct answer or 0 for a wrong one.
func makeAnswer(playerID int64, username string, correct bool) *LeaderboardAnswer {
	const window = 10 * time.Second
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	return &LeaderboardAnswer{
		PlayerID:          playerID,
		Username:          username,
		QuestionStartedAt: start,
		QuestionExpiredAt: start.Add(window),
		AnsweredAt:        start,
		Correct:           correct,
	}
}

func TestService_GetQuizLeaderboard(t *testing.T) {
	t.Parallel()

	t.Run("returns 404 when quiz not found", func(t *testing.T) {
		t.Parallel()

		svc := NewService(stubStore{}, stubQuizStore{
			quizExists: func(_ context.Context, _ int64) (bool, error) {
				return false, nil
			},
		}, slog.New(slog.DiscardHandler))

		_, err := svc.GetQuizLeaderboard(t.Context(), 1, 0, 10)
		if got, want := err, quiz.ErrQuizNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("returns empty slice when no games", func(t *testing.T) {
		t.Parallel()

		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					return nil, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		got, err := svc.GetQuizLeaderboard(t.Context(), 1, 0, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Entries) != 0 {
			t.Errorf("len(entries) = %d, want 0", len(got.Entries))
		}
		if got.CurrentPlayer != nil {
			t.Errorf("CurrentPlayer = %+v, want nil", got.CurrentPlayer)
		}
	})

	t.Run("sums a single player's answers", func(t *testing.T) {
		t.Parallel()

		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					// Two correct answers + one wrong -> 1000 + 1000 + 0 = 2000.
					return []*LeaderboardAnswer{
						makeAnswer(1, "alice", true),
						makeAnswer(1, "alice", true),
						makeAnswer(1, "alice", false),
					}, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		result, err := svc.GetQuizLeaderboard(t.Context(), 1, 0, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := len(result.Entries), 1; got != want {
			t.Fatalf("len(entries) = %d, want %d", got, want)
		}
		if got, want := result.Entries[0].PlayerID, int64(1); got != want {
			t.Errorf("entries[0].PlayerID = %d, want %d", got, want)
		}
		if got, want := result.Entries[0].Username, "alice"; got != want {
			t.Errorf("entries[0].Username = %q, want %q", got, want)
		}
		if got, want := result.Entries[0].Score, 2000; got != want {
			t.Errorf("entries[0].Score = %d, want %d", got, want)
		}
		if got, want := result.Entries[0].Rank, 1; got != want {
			t.Errorf("entries[0].Rank = %d, want %d", got, want)
		}
	})

	t.Run("sorts two players by score descending", func(t *testing.T) {
		t.Parallel()

		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					return []*LeaderboardAnswer{
						// alice: 1000.
						makeAnswer(1, "alice", true),
						// bob: 2000.
						makeAnswer(2, "bob", true),
						makeAnswer(2, "bob", true),
					}, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		result, err := svc.GetQuizLeaderboard(t.Context(), 1, 0, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := len(result.Entries), 2; got != want {
			t.Fatalf("len(entries) = %d, want %d", got, want)
		}
		if got, want := result.Entries[0].Username, "bob"; got != want {
			t.Errorf("entries[0].Username = %q, want %q", got, want)
		}
		if got, want := result.Entries[1].Username, "alice"; got != want {
			t.Errorf("entries[1].Username = %q, want %q", got, want)
		}
		if got, want := result.Entries[0].Rank, 1; got != want {
			t.Errorf("entries[0].Rank = %d, want %d", got, want)
		}
		if got, want := result.Entries[1].Rank, 2; got != want {
			t.Errorf("entries[1].Rank = %d, want %d", got, want)
		}
	})

	t.Run("breaks ties by ascending username", func(t *testing.T) {
		t.Parallel()

		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					// Three players with identical 1000-point runs but
					// usernames intentionally out of order.
					return []*LeaderboardAnswer{
						makeAnswer(1, "charlie", true),
						makeAnswer(2, "alice", true),
						makeAnswer(3, "bob", true),
					}, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		result, err := svc.GetQuizLeaderboard(t.Context(), 1, 0, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gotNames := []string{result.Entries[0].Username, result.Entries[1].Username, result.Entries[2].Username}
		wantNames := []string{"alice", "bob", "charlie"}
		if diff := cmp.Diff(wantNames, gotNames); diff != "" {
			t.Errorf("username order mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("flags the entry of the current player", func(t *testing.T) {
		t.Parallel()

		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					return []*LeaderboardAnswer{
						makeAnswer(1, "alice", true),
						makeAnswer(2, "bob", true),
						makeAnswer(2, "bob", true),
					}, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		result, err := svc.GetQuizLeaderboard(t.Context(), 1, 1, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var aliceEntry, bobEntry LeaderboardEntry
		for _, e := range result.Entries {
			switch e.PlayerID {
			case 1:
				aliceEntry = e
			case 2:
				bobEntry = e
			default:
				t.Fatalf("unexpected player ID %d", e.PlayerID)
			}
		}

		if got, want := aliceEntry.IsCurrentPlayer, true; got != want {
			t.Errorf("aliceEntry.IsCurrentPlayer = %v, want %v", got, want)
		}
		if got, want := bobEntry.IsCurrentPlayer, false; got != want {
			t.Errorf("bobEntry.IsCurrentPlayer = %v, want %v", got, want)
		}
		// CurrentPlayer is also surfaced separately so off-leaderboard
		// players can see their own row (#181). Alice is in the top-N
		// here so it duplicates her entry, but the field is populated
		// either way.
		if result.CurrentPlayer == nil {
			t.Fatal("CurrentPlayer = nil, want alice's standing")
		}
		if got, want := result.CurrentPlayer.PlayerID, int64(1); got != want {
			t.Errorf("CurrentPlayer.PlayerID = %d, want %d", got, want)
		}
		if got, want := result.CurrentPlayer.Rank, 2; got != want {
			t.Errorf("CurrentPlayer.Rank = %d, want %d (alice trails bob)", got, want)
		}
	})

	t.Run("truncates results to the supplied limit", func(t *testing.T) {
		t.Parallel()

		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					answers := make([]*LeaderboardAnswer, 0, 5)
					for i := int64(1); i <= 5; i++ {
						// Player i answers (5-i+1) correct questions so
						// scores are strictly decreasing: 5000, 4000, 3000, 2000, 1000.
						for range 6 - i {
							answers = append(answers, makeAnswer(i, "p", true))
						}
					}

					return answers, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		result, err := svc.GetQuizLeaderboard(t.Context(), 1, 0, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := len(result.Entries), 3; got != want {
			t.Errorf("len(entries) = %d, want %d", got, want)
		}
		if got, want := result.Entries[0].PlayerID, int64(1); got != want {
			t.Errorf("entries[0].PlayerID = %d, want %d", got, want)
		}
	})

	t.Run("defaults limit to 10 when limit <= 0", func(t *testing.T) {
		t.Parallel()

		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					answers := make([]*LeaderboardAnswer, 0, 15)
					for i := int64(1); i <= 15; i++ {
						answers = append(answers, makeAnswer(i, "p", true))
					}

					return answers, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		result, err := svc.GetQuizLeaderboard(t.Context(), 1, 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := len(result.Entries), 10; got != want {
			t.Errorf("len(entries) = %d, want %d (default limit)", got, want)
		}
	})

	t.Run("surfaces CurrentPlayer when current player is outside the top-N", func(t *testing.T) {
		t.Parallel()

		// Five players, strictly decreasing scores (5000, 4000, 3000,
		// 2000, 1000). Limit to top-3. Player 5 (lowest score) is the
		// requesting player — they should NOT appear in Entries but
		// SHOULD appear in CurrentPlayer with Rank=5. This is the
		// scenario from #181.
		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					answers := make([]*LeaderboardAnswer, 0, 5)
					for i := int64(1); i <= 5; i++ {
						for range 6 - i {
							answers = append(answers, makeAnswer(i, fmt.Sprintf("p%d", i), true))
						}
					}

					return answers, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		result, err := svc.GetQuizLeaderboard(t.Context(), 1, 5, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := len(result.Entries), 3; got != want {
			t.Fatalf("len(entries) = %d, want %d", got, want)
		}
		for _, e := range result.Entries {
			if e.PlayerID == 5 {
				t.Errorf("player 5 should be outside the top-3, found in entries: %+v", e)
			}
		}
		if result.CurrentPlayer == nil {
			t.Fatal("CurrentPlayer = nil, want player 5's standing")
		}
		if got, want := result.CurrentPlayer.PlayerID, int64(5); got != want {
			t.Errorf("CurrentPlayer.PlayerID = %d, want %d", got, want)
		}
		if got, want := result.CurrentPlayer.Rank, 5; got != want {
			t.Errorf("CurrentPlayer.Rank = %d, want %d", got, want)
		}
		if got, want := result.CurrentPlayer.Score, 1000; got != want {
			t.Errorf("CurrentPlayer.Score = %d, want %d", got, want)
		}
		if got, want := result.CurrentPlayer.IsCurrentPlayer, true; got != want {
			t.Errorf("CurrentPlayer.IsCurrentPlayer = %v, want %v", got, want)
		}
	})

	t.Run("CurrentPlayer is nil when current player has no row", func(t *testing.T) {
		t.Parallel()

		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					return []*LeaderboardAnswer{
						makeAnswer(1, "alice", true),
					}, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		// Request as player 99, who never played.
		result, err := svc.GetQuizLeaderboard(t.Context(), 1, 99, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.CurrentPlayer != nil {
			t.Errorf("CurrentPlayer = %+v, want nil (player has no row)", result.CurrentPlayer)
		}
	})
}

// recordingPublisher captures every Publish call so a test can assert
// the exact set of quiz IDs that were notified.
type recordingPublisher struct {
	published []int64
}

// Publish records the quiz ID in the order the call was made so the
// test can assert which leaderboards the fan-out ticked.
func (p *recordingPublisher) Publish(quizID int64) {
	p.published = append(p.published, quizID)
}

func TestService_PublishLeaderboardForPlayer(t *testing.T) {
	t.Parallel()

	t.Run("publishes once per quiz the player has answered on", func(t *testing.T) {
		t.Parallel()

		pub := &recordingPublisher{}
		svc := NewService(
			stubStore{
				listQuizIDsForPlayer: func(_ context.Context, playerID int64) ([]int64, error) {
					if got, want := playerID, int64(42); got != want {
						t.Errorf("ListQuizIDsForPlayer playerID = %d, want %d", got, want)
					}

					return []int64{7, 11, 13}, nil
				},
			},
			stubQuizStore{},
			slog.New(slog.DiscardHandler),
		)
		svc.SetLeaderboardPublisher(pub)

		if err := svc.PublishLeaderboardForPlayer(t.Context(), 42); err != nil {
			t.Fatalf("PublishLeaderboardForPlayer err = %v, want nil", err)
		}

		want := []int64{7, 11, 13}
		if got := pub.published; !slices.Equal(got, want) {
			t.Errorf("PublishLeaderboardForPlayer recorded %v, want %v", got, want)
		}
	})

	t.Run("no publisher wired is a no-op", func(t *testing.T) {
		t.Parallel()

		svc := NewService(
			stubStore{
				listQuizIDsForPlayer: func(_ context.Context, _ int64) ([]int64, error) {
					t.Error("ListQuizIDsForPlayer must not be called when no publisher is wired")

					return nil, nil
				},
			},
			stubQuizStore{},
			slog.New(slog.DiscardHandler),
		)

		if err := svc.PublishLeaderboardForPlayer(t.Context(), 1); err != nil {
			t.Errorf("PublishLeaderboardForPlayer err = %v, want nil", err)
		}
	})

	t.Run("store error is wrapped and surfaced", func(t *testing.T) {
		t.Parallel()

		pub := &recordingPublisher{}
		boom := errors.New("boom")
		svc := NewService(
			stubStore{
				listQuizIDsForPlayer: func(_ context.Context, _ int64) ([]int64, error) {
					return nil, boom
				},
			},
			stubQuizStore{},
			slog.New(slog.DiscardHandler),
		)
		svc.SetLeaderboardPublisher(pub)

		err := svc.PublishLeaderboardForPlayer(t.Context(), 1)
		if got, want := err, boom; !errors.Is(got, want) {
			t.Errorf("PublishLeaderboardForPlayer err = %v, want wrap of %v", got, want)
		}
		if got, want := len(pub.published), 0; got != want {
			t.Errorf(
				"PublishLeaderboardForPlayer recorded %d calls, want %d (listing failed before any publish)",
				got,
				want,
			)
		}
	})
}
