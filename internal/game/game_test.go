package game_test

import (
	"log/slog"
	"testing"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"

	"github.com/starquake/topbanana/internal/dbtest"
	. "github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

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

		gq, err := svc.GetNextQuestion(ctx, g.ID)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		insertPlayer2 := `INSERT INTO players (id, username, email, created_at) VALUES (2, 'player2', 'player2@test.com', CURRENT_TIMESTAMP)`
		if _, err = db.ExecContext(ctx, insertPlayer2); err != nil {
			t.Fatalf("failed to insert player 2: %v", err)
		}

		correctOption := testQuiz.Questions[0].Options[0] // Paris, Correct: true
		wrongOption := testQuiz.Questions[0].Options[1]   // London, Correct: false

		if _, err = svc.SubmitAnswer(ctx, g.ID, 1, gq.QuizQuestion.ID, correctOption.ID); err != nil {
			t.Fatalf("failed to submit answer for player 1: %v", err)
		}
		if _, err = svc.SubmitAnswer(ctx, g.ID, 2, gq.QuizQuestion.ID, wrongOption.ID); err != nil {
			t.Fatalf("failed to submit answer for player 2: %v", err)
		}

		results, err := svc.GetResults(ctx, g.ID)
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

		gq, err := svc.GetNextQuestion(ctx, g.ID)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		insertPlayer2 := `INSERT INTO players (id, username, email, created_at) VALUES (2, 'player2', 'player2@test.com', CURRENT_TIMESTAMP)`
		if _, err = db.ExecContext(ctx, insertPlayer2); err != nil {
			t.Fatalf("failed to insert player 2: %v", err)
		}

		wrongOption := testQuiz.Questions[0].Options[1] // London, Correct: false

		if _, err = svc.SubmitAnswer(ctx, g.ID, 1, gq.QuizQuestion.ID, wrongOption.ID); err != nil {
			t.Fatalf("failed to submit answer for player 1: %v", err)
		}
		if _, err = svc.SubmitAnswer(ctx, g.ID, 2, gq.QuizQuestion.ID, wrongOption.ID); err != nil {
			t.Fatalf("failed to submit answer for player 2: %v", err)
		}

		results, err := svc.GetResults(ctx, g.ID)
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

		service := NewService(gameStore, quizStore, slog.Default())
		gq, err := service.GetNextQuestion(ctx, testGame.ID)
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

		err = gameStore.CreateQuestion(ctx, &Question{GameID: testGame.ID, QuestionID: testQuiz.Questions[0].ID})
		if err != nil {
			t.Fatalf("failed to create game question: %v", err)
		}

		service := NewService(gameStore, quizStore, slog.Default())
		gq, err := service.GetNextQuestion(ctx, testGame.ID)
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
}
