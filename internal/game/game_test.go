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
		qs, err := service.GetNextQuestion(ctx, testGame.ID)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		if cmp.Diff(qs, testQuiz.Questions[0]) != "" {
			t.Errorf("got qs: %+v, want %+v", qs, testQuiz.Questions[0])
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
		qs, err := service.GetNextQuestion(ctx, testGame.ID)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		if cmp.Diff(qs, testQuiz.Questions[1]) != "" {
			t.Errorf("got qs: %+v, want %+v", qs, testQuiz.Questions[1])
		}
	})
}
