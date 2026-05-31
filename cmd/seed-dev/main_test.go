package main_test

import (
	"errors"
	"testing"

	. "github.com/starquake/topbanana/cmd/seed-dev"
)

func TestQuizFromFixtureFlat(t *testing.T) {
	t.Parallel()

	f := ExportQuizFixture{
		Title:       "Flat Quiz",
		Description: "A single-round quiz.",
		Questions: []ExportQuestionFixture{
			{Text: "Q1", Options: []ExportOptionFixture{{Text: "A", Correct: true}, {Text: "B"}}},
			{Text: "Q2", Options: []ExportOptionFixture{{Text: "C"}, {Text: "D", Correct: true}}},
		},
	}

	qz, err := ExportQuizFromFixture(&f)
	if err != nil {
		t.Fatalf("ExportQuizFromFixture() err = %v, want nil", err)
	}

	if got, want := len(qz.Rounds), 0; got != want {
		t.Errorf("len(Rounds) = %d, want %d", got, want)
	}
	if got, want := len(qz.Questions), 2; got != want {
		t.Fatalf("len(Questions) = %d, want %d", got, want)
	}
	for i, q := range qz.Questions {
		if got, want := q.Position, i+1; got != want {
			t.Errorf("Questions[%d].Position = %d, want %d", i, got, want)
		}
	}
	if got, want := qz.Questions[0].Text, "Q1"; got != want {
		t.Errorf("Questions[0].Text = %q, want %q", got, want)
	}
	if got, want := len(qz.Questions[0].Options), 2; got != want {
		t.Errorf("len(Questions[0].Options) = %d, want %d", got, want)
	}
}

func TestQuizFromFixtureRounds(t *testing.T) {
	t.Parallel()

	f := ExportQuizFixture{
		Title:       "Round Quiz",
		Description: "A multi-round quiz.",
		Rounds: []ExportRoundFixture{
			{
				Title:   "Warm-up",
				Summary: "An easy start.",
				Questions: []ExportQuestionFixture{
					{Text: "R1Q1", Options: []ExportOptionFixture{{Text: "A", Correct: true}, {Text: "B"}}},
					{Text: "R1Q2", Options: []ExportOptionFixture{{Text: "C", Correct: true}, {Text: "D"}}},
				},
			},
			{
				Title:   "Final stretch",
				Summary: "One harder round.",
				Questions: []ExportQuestionFixture{
					{Text: "R2Q1", Options: []ExportOptionFixture{{Text: "E", Correct: true}, {Text: "F"}}},
				},
			},
		},
	}

	qz, err := ExportQuizFromFixture(&f)
	if err != nil {
		t.Fatalf("ExportQuizFromFixture() err = %v, want nil", err)
	}

	if got, want := len(qz.Rounds), 2; got != want {
		t.Fatalf("len(Rounds) = %d, want %d", got, want)
	}

	if got, want := qz.Rounds[0].Title, "Warm-up"; got != want {
		t.Errorf("Rounds[0].Title = %q, want %q", got, want)
	}
	if got, want := qz.Rounds[0].Summary, "An easy start."; got != want {
		t.Errorf("Rounds[0].Summary = %q, want %q", got, want)
	}
	if got, want := qz.Rounds[0].Position, 0; got != want {
		t.Errorf("Rounds[0].Position = %d, want %d", got, want)
	}
	if got, want := qz.Rounds[1].Title, "Final stretch"; got != want {
		t.Errorf("Rounds[1].Title = %q, want %q", got, want)
	}
	if got, want := qz.Rounds[1].Position, 1; got != want {
		t.Errorf("Rounds[1].Position = %d, want %d", got, want)
	}

	if got, want := len(qz.Rounds[0].Questions), 2; got != want {
		t.Errorf("len(Rounds[0].Questions) = %d, want %d", got, want)
	}
	if got, want := len(qz.Rounds[1].Questions), 1; got != want {
		t.Errorf("len(Rounds[1].Questions) = %d, want %d", got, want)
	}

	// finishGame iterates qz.Questions, so the flat mirror must hold every
	// question with quiz-wide positions 1..N in document order across rounds.
	if got, want := len(qz.Questions), 3; got != want {
		t.Fatalf("len(Questions) = %d, want %d", got, want)
	}
	wantText := []string{"R1Q1", "R1Q2", "R2Q1"}
	for i, q := range qz.Questions {
		if got, want := q.Position, i+1; got != want {
			t.Errorf("Questions[%d].Position = %d, want %d", i, got, want)
		}
		if got, want := q.Text, wantText[i]; got != want {
			t.Errorf("Questions[%d].Text = %q, want %q", i, got, want)
		}
	}

	// The flat mirror and the per-round slices share the same question
	// pointers, so a position assigned once is visible from both views.
	if qz.Rounds[0].Questions[0] != qz.Questions[0] {
		t.Error("Rounds[0].Questions[0] is not the same pointer as Questions[0]")
	}
	if qz.Rounds[1].Questions[0] != qz.Questions[2] {
		t.Error("Rounds[1].Questions[0] is not the same pointer as Questions[2]")
	}
}

func TestQuizFromFixtureRoundsAndQuestionsRejected(t *testing.T) {
	t.Parallel()

	f := ExportQuizFixture{
		Title:     "Both",
		Questions: []ExportQuestionFixture{{Text: "Q1"}},
		Rounds:    []ExportRoundFixture{{Title: "R", Questions: []ExportQuestionFixture{{Text: "R1Q1"}}}},
	}

	if _, err := ExportQuizFromFixture(&f); !errors.Is(err, ErrExportFixtureQuestionsOrRounds) {
		t.Errorf("err = %v, want %v", err, ErrExportFixtureQuestionsOrRounds)
	}
}

func TestQuizFromFixtureNeitherRejected(t *testing.T) {
	t.Parallel()

	f := ExportQuizFixture{Title: "Empty"}

	if _, err := ExportQuizFromFixture(&f); !errors.Is(err, ErrExportFixtureQuestionsOrRounds) {
		t.Errorf("err = %v, want %v", err, ErrExportFixtureQuestionsOrRounds)
	}
}

func TestQuizFromFixtureRoundTitleRequired(t *testing.T) {
	t.Parallel()

	f := ExportQuizFixture{
		Title: "No Round Title",
		Rounds: []ExportRoundFixture{
			{Title: "", Questions: []ExportQuestionFixture{{Text: "R1Q1"}}},
		},
	}

	if _, err := ExportQuizFromFixture(&f); !errors.Is(err, ErrExportFixtureRoundTitleRequired) {
		t.Errorf("err = %v, want %v", err, ErrExportFixtureRoundTitleRequired)
	}
}

func TestQuizFromFixtureRoundNoQuestions(t *testing.T) {
	t.Parallel()

	f := ExportQuizFixture{
		Title: "Empty Round",
		Rounds: []ExportRoundFixture{
			{Title: "Lonely", Questions: nil},
		},
	}

	if _, err := ExportQuizFromFixture(&f); !errors.Is(err, ErrExportFixtureRoundNoQuestions) {
		t.Errorf("err = %v, want %v", err, ErrExportFixtureRoundNoQuestions)
	}
}
