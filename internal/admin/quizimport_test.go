package admin_test

import (
	"testing"

	"github.com/starquake/topbanana/internal/admin"
)

// TestQuizFromImportPayload_MapsQuestions pins the payload-to-domain
// translation: the title drives the slug, and questions keep their
// payload order with 1..N positions. A top-level questions[] with no
// rounds[] imports flat - the store attaches each question to the quiz's
// default round (#546).
func TestQuizFromImportPayload_MapsQuestions(t *testing.T) {
	t.Parallel()

	qz, err := admin.QuizFromImportPayload(admin.QuizImportPayload{
		Title:       "Capitals",
		Description: "x",
		Questions: []admin.QuizImportQuestionPayload{
			{Text: "Q1", Options: []admin.QuizImportOptionPayload{{Text: "a", Correct: true}}},
			{Text: "Q2", Options: []admin.QuizImportOptionPayload{{Text: "b", Correct: true}}},
		},
	})
	if err != nil {
		t.Fatalf("QuizFromImportPayload err = %v, want nil", err)
	}

	if qz == nil {
		t.Fatal("quiz = nil, want non-nil")
	}
	if got, want := qz.Slug, "capitals"; got != want {
		t.Errorf("slug = %q, want %q", got, want)
	}
	if got, want := len(qz.Rounds), 0; got != want {
		t.Errorf("len(rounds) = %d, want %d", got, want)
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

// TestQuizFromImportPayload_MapsRounds pins the rounds[] path (#546):
// each round maps onto Quiz.Rounds with its title, summary, and
// position, the questions land on both the round and the flat
// Quiz.Questions, and quiz-wide positions run 1..N across all rounds in
// payload order.
func TestQuizFromImportPayload_MapsRounds(t *testing.T) {
	t.Parallel()

	qz, err := admin.QuizFromImportPayload(admin.QuizImportPayload{
		Title:       "Capitals",
		Description: "x",
		Rounds: []admin.QuizImportRoundPayload{
			{
				Title:   "Warm-up",
				Summary: "Easy start.",
				Questions: []admin.QuizImportQuestionPayload{
					{Text: "Q1", Options: []admin.QuizImportOptionPayload{{Text: "a", Correct: true}}},
					{Text: "Q2", Options: []admin.QuizImportOptionPayload{{Text: "b", Correct: true}}},
				},
			},
			{
				Title: "Final",
				Questions: []admin.QuizImportQuestionPayload{
					{Text: "Q3", Options: []admin.QuizImportOptionPayload{{Text: "c", Correct: true}}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("QuizFromImportPayload err = %v, want nil", err)
	}

	if got, want := len(qz.Rounds), 2; got != want {
		t.Fatalf("len(rounds) = %d, want %d", got, want)
	}
	if got, want := qz.Rounds[0].Title, "Warm-up"; got != want {
		t.Errorf("rounds[0].Title = %q, want %q", got, want)
	}
	if got, want := qz.Rounds[0].Summary, "Easy start."; got != want {
		t.Errorf("rounds[0].Summary = %q, want %q", got, want)
	}
	if got, want := qz.Rounds[0].Position, 0; got != want {
		t.Errorf("rounds[0].Position = %d, want %d", got, want)
	}
	if got, want := qz.Rounds[1].Position, 1; got != want {
		t.Errorf("rounds[1].Position = %d, want %d", got, want)
	}
	if got, want := len(qz.Rounds[0].Questions), 2; got != want {
		t.Errorf("len(rounds[0].Questions) = %d, want %d", got, want)
	}
	if got, want := len(qz.Rounds[1].Questions), 1; got != want {
		t.Errorf("len(rounds[1].Questions) = %d, want %d", got, want)
	}

	// Flattened questions carry quiz-wide 1..N positions across rounds.
	if got, want := len(qz.Questions), 3; got != want {
		t.Fatalf("len(questions) = %d, want %d", got, want)
	}
	for i, want := range []int{1, 2, 3} {
		if got := qz.Questions[i].Position; got != want {
			t.Errorf("questions[%d].Position = %d, want %d", i, got, want)
		}
	}
}

// TestQuizFromImportPayload_QuestionsAndRoundsMutuallyExclusive pins the
// validation that rejects a payload carrying both a top-level
// questions[] and rounds[], or neither (#546).
func TestQuizFromImportPayload_QuestionsAndRoundsMutuallyExclusive(t *testing.T) {
	t.Parallel()

	question := admin.QuizImportQuestionPayload{
		Text: "Q1", Options: []admin.QuizImportOptionPayload{{Text: "a", Correct: true}},
	}
	round := admin.QuizImportRoundPayload{
		Title: "R1", Questions: []admin.QuizImportQuestionPayload{question},
	}

	tests := map[string]admin.QuizImportPayload{
		"both": {
			Title: "x", Description: "y",
			Questions: []admin.QuizImportQuestionPayload{question},
			Rounds:    []admin.QuizImportRoundPayload{round},
		},
		"neither": {Title: "x", Description: "y"},
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := admin.QuizFromImportPayload(payload); err == nil {
				t.Fatal("QuizFromImportPayload err = nil, want non-nil")
			}
		})
	}
}

// TestQuizFromImportPayload_RoundShape pins the per-round validation: a
// round must carry a title and at least one question (#546).
func TestQuizFromImportPayload_RoundShape(t *testing.T) {
	t.Parallel()

	question := admin.QuizImportQuestionPayload{
		Text: "Q1", Options: []admin.QuizImportOptionPayload{{Text: "a", Correct: true}},
	}

	tests := map[string]admin.QuizImportRoundPayload{
		"missing title":   {Questions: []admin.QuizImportQuestionPayload{question}},
		"empty questions": {Title: "R1"},
	}
	for name, round := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := admin.QuizFromImportPayload(admin.QuizImportPayload{
				Title: "x", Description: "y",
				Rounds: []admin.QuizImportRoundPayload{round},
			})
			if err == nil {
				t.Fatal("QuizFromImportPayload err = nil, want non-nil")
			}
		})
	}
}
