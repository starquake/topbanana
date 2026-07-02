package admin_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/admin"
)

// TestQuizImportExampleParses is the golden test that the exact JSON
// sample rendered on the import screen parses cleanly through the real
// importer: the same DisallowUnknownFields decode and payload-to-domain
// translation the POST handler runs, then the shared form validation.
// DisallowUnknownFields fails the test if the sample carries a field the
// parser rejects (a renamed or undocumented key), and the boundary-override
// assertion below fails it if the sample stops demonstrating the
// boundaryDurationSeconds optional the field reference documents, so the
// on-screen example and the parser cannot drift (#1138).
func TestQuizImportExampleParses(t *testing.T) {
	t.Parallel()

	var payload admin.QuizImportPayload
	dec := json.NewDecoder(strings.NewReader(admin.QuizImportExample))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		t.Fatalf("decoding sample: err = %v, want nil", err)
	}

	qz, err := admin.QuizFromImportPayload(payload)
	if err != nil {
		t.Fatalf("QuizFromImportPayload err = %v, want nil", err)
	}

	if problems := admin.ValidateQuizForm(t.Context(), qz); len(problems) > 0 {
		t.Errorf("ValidateQuizForm problems = %v, want none", problems)
	}

	boundarySet := false
	for _, r := range qz.Rounds {
		if r.BoundaryDurationSeconds != nil {
			boundarySet = true

			break
		}
	}
	if !boundarySet {
		t.Error("sample no longer sets boundaryDurationSeconds on any round (#1138)")
	}
}

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

// TestQuizFromImportPayload_MapsRoundBoundaryDuration pins the #554
// round override: a round carrying boundaryDurationSeconds round-trips
// the value onto Quiz.Rounds, and a round omitting it yields nil
// (inherit the quiz default at game time).
func TestQuizFromImportPayload_MapsRoundBoundaryDuration(t *testing.T) {
	t.Parallel()

	dur := 15
	qz, err := admin.QuizFromImportPayload(admin.QuizImportPayload{
		Title:       "Capitals",
		Description: "x",
		Rounds: []admin.QuizImportRoundPayload{
			{
				Title:                   "Timed",
				BoundaryDurationSeconds: &dur,
				Questions: []admin.QuizImportQuestionPayload{
					{Text: "Q1", Options: []admin.QuizImportOptionPayload{{Text: "a", Correct: true}}},
				},
			},
			{
				Title: "Default",
				Questions: []admin.QuizImportQuestionPayload{
					{Text: "Q2", Options: []admin.QuizImportOptionPayload{{Text: "b", Correct: true}}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("QuizFromImportPayload err = %v, want nil", err)
	}

	if got := qz.Rounds[0].BoundaryDurationSeconds; got == nil {
		t.Fatal("rounds[0].BoundaryDurationSeconds = nil, want 15")
	} else if want := 15; *got != want {
		t.Errorf("rounds[0].BoundaryDurationSeconds = %d, want %d", *got, want)
	}
	if got := qz.Rounds[1].BoundaryDurationSeconds; got != nil {
		t.Errorf("rounds[1].BoundaryDurationSeconds = %v, want nil", *got)
	}
}

// TestQuizFromImportPayload_RoundBoundaryDurationRangeValidated pins the
// #554 import gate: an imported round whose boundaryDurationSeconds falls
// outside the 1..600 bound surfaces as a quizForm validation problem (a
// clean inline 400) rather than tripping the DB CHECK at INSERT.
func TestQuizFromImportPayload_RoundBoundaryDurationRangeValidated(t *testing.T) {
	t.Parallel()

	dur := 9999
	qz, err := admin.QuizFromImportPayload(admin.QuizImportPayload{
		Title:       "Capitals",
		Description: "x",
		Rounds: []admin.QuizImportRoundPayload{
			{
				Title:                   "Timed",
				BoundaryDurationSeconds: &dur,
				Questions: []admin.QuizImportQuestionPayload{
					{Text: "Q1", Options: []admin.QuizImportOptionPayload{{Text: "a", Correct: true}}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("QuizFromImportPayload err = %v, want nil", err)
	}

	problems := admin.ValidateQuizForm(t.Context(), qz)
	if _, ok := problems["rounds[0][boundarydurationseconds]"]; !ok {
		t.Errorf("problems = %v, want a rounds[0][boundarydurationseconds] key", problems)
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

// TestStripCodeFences pins the import paste tolerance: a ```json (or bare ```)
// fenced block pasted from an LLM is unwrapped to its inner JSON, while
// unfenced input passes through untouched.
func TestStripCodeFences(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"unfenced passes through", `{"title":"x"}`, `{"title":"x"}`},
		{"json fence", "```json\n{\"title\":\"x\"}\n```", `{"title":"x"}`},
		{"bare fence", "```\n{\"title\":\"x\"}\n```", `{"title":"x"}`},
		{"leading and trailing whitespace", "  ```json\n{\"title\":\"x\"}\n```  ", `{"title":"x"}`},
		{"fence with no closing", "```json\n{\"title\":\"x\"}", `{"title":"x"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got, want := admin.StripCodeFences(tt.in), tt.want; got != want {
				t.Errorf("StripCodeFences(%q) = %q, want %q", tt.in, got, want)
			}
		})
	}
}
