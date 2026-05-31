package admin_test

import (
	"testing"

	"github.com/starquake/topbanana/internal/admin"
)

// TestQuizFromImportPayload_MapsQuestions pins the payload-to-domain
// translation: the title drives the slug, and questions keep their
// payload order with 1..N positions. Rounds are not part of the import
// wire shape (#444) - the store attaches imported questions to each
// quiz's default round.
func TestQuizFromImportPayload_MapsQuestions(t *testing.T) {
	t.Parallel()

	qz := admin.QuizFromImportPayload(admin.QuizImportPayload{
		Title:       "Capitals",
		Description: "x",
		Questions: []admin.QuizImportQuestionPayload{
			{Text: "Q1", Options: []admin.QuizImportOptionPayload{{Text: "a", Correct: true}}},
			{Text: "Q2", Options: []admin.QuizImportOptionPayload{{Text: "b", Correct: true}}},
		},
	})

	if qz == nil {
		t.Fatal("quiz = nil, want non-nil")
	}
	if got, want := qz.Slug, "capitals"; got != want {
		t.Errorf("slug = %q, want %q", got, want)
	}
	if got, want := len(qz.Questions), 2; got != want {
		t.Fatalf("len(questions) = %d, want %d", got, want)
	}
	if got, want := qz.Questions[0].Position, 1; got != want {
		t.Errorf("questions[0].Position = %d, want %d", got, want)
	}
	if got, want := qz.Questions[1].Position, 2; got != want {
		t.Errorf("questions[1].Position = %d, want %d", got, want)
	}
}

// TestQuizFromImportPayload_NoQuestions pins the empty-questions path:
// a payload with no questions still produces a non-nil quiz with an
// empty (non-nil) question slice.
func TestQuizFromImportPayload_NoQuestions(t *testing.T) {
	t.Parallel()

	qz := admin.QuizFromImportPayload(admin.QuizImportPayload{
		Title:       "Capitals",
		Description: "x",
	})

	if qz == nil {
		t.Fatal("quiz = nil, want non-nil")
	}
	if got, want := len(qz.Questions), 0; got != want {
		t.Errorf("len(questions) = %d, want %d", got, want)
	}
}
