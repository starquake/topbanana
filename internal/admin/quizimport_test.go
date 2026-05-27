package admin_test

import (
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/quiz"
)

// TestQuizFromImportPayload_NoBreaksKey pins the back-compat path: a
// payload omitting the `breaks` field altogether produces a quiz with
// an empty break slice and no error.
func TestQuizFromImportPayload_NoBreaksKey(t *testing.T) {
	t.Parallel()

	qz, breaks := admin.QuizFromImportPayload(admin.QuizImportPayload{
		Title:       "Capitals",
		Description: "x",
		Questions: []admin.QuizImportQuestionPayload{
			{Text: "Q1", Options: []admin.QuizImportOptionPayload{{Text: "a", Correct: true}}},
		},
	})

	if qz == nil {
		t.Fatal("quiz = nil, want non-nil")
	}
	if got, want := len(breaks), 0; got != want {
		t.Errorf("len(breaks) = %d, want %d", got, want)
	}
}

// TestQuizFromImportPayload_EmptyBreaksArray covers the case where the
// payload sends an empty `breaks: []`. Should behave identically to
// the omitted-key case.
func TestQuizFromImportPayload_EmptyBreaksArray(t *testing.T) {
	t.Parallel()

	qz, breaks := admin.QuizFromImportPayload(admin.QuizImportPayload{
		Title:  "Capitals",
		Breaks: []admin.QuizImportBreakPayload{},
		Questions: []admin.QuizImportQuestionPayload{
			{Text: "Q1", Options: []admin.QuizImportOptionPayload{{Text: "a", Correct: true}}},
		},
	})

	if qz == nil {
		t.Fatal("quiz = nil, want non-nil")
	}
	if got, want := len(breaks), 0; got != want {
		t.Errorf("len(breaks) = %d, want %d", got, want)
	}
}

// TestQuizFromImportPayload_PositionZeroBreak pins the pre-roll break:
// position 0 is the "before question 1" slot and must round-trip
// without translation.
func TestQuizFromImportPayload_PositionZeroBreak(t *testing.T) {
	t.Parallel()

	_, breaks := admin.QuizFromImportPayload(admin.QuizImportPayload{
		Title: "Capitals",
		Breaks: []admin.QuizImportBreakPayload{
			{Position: 0, Text: "Welcome!"},
		},
		Questions: []admin.QuizImportQuestionPayload{
			{Text: "Q1", Options: []admin.QuizImportOptionPayload{{Text: "a", Correct: true}}},
		},
	})

	if got, want := len(breaks), 1; got != want {
		t.Fatalf("len(breaks) = %d, want %d", got, want)
	}
	if got, want := breaks[0].Position, 0; got != want {
		t.Errorf("breaks[0].Position = %d, want %d", got, want)
	}
	if got, want := breaks[0].Text, "Welcome!"; got != want {
		t.Errorf("breaks[0].Text = %q, want %q", got, want)
	}
	if got, want := breaks[0].QuizID, int64(0); got != want {
		t.Errorf("breaks[0].QuizID = %d, want %d (caller sets it post-insert)", got, want)
	}
}

// TestQuizFromImportPayload_MidQuizBreak pins the mid-quiz slot: a
// break at position N (N > 0) means "after the Nth question".
func TestQuizFromImportPayload_MidQuizBreak(t *testing.T) {
	t.Parallel()

	_, breaks := admin.QuizFromImportPayload(admin.QuizImportPayload{
		Title: "Capitals",
		Breaks: []admin.QuizImportBreakPayload{
			{Position: 2, Text: "Halfway there."},
		},
		Questions: []admin.QuizImportQuestionPayload{
			{Text: "Q1", Options: []admin.QuizImportOptionPayload{{Text: "a", Correct: true}}},
			{Text: "Q2", Options: []admin.QuizImportOptionPayload{{Text: "b", Correct: true}}},
			{Text: "Q3", Options: []admin.QuizImportOptionPayload{{Text: "c", Correct: true}}},
		},
	})

	if got, want := len(breaks), 1; got != want {
		t.Fatalf("len(breaks) = %d, want %d", got, want)
	}
	if got, want := breaks[0].Position, 2; got != want {
		t.Errorf("breaks[0].Position = %d, want %d", got, want)
	}
}

// TestQuizFromImportPayload_MultipleBreaks pins payload order is
// preserved on the returned slice, so a later position-based
// duplicate check sees the same order the LLM produced.
func TestQuizFromImportPayload_MultipleBreaks(t *testing.T) {
	t.Parallel()

	_, breaks := admin.QuizFromImportPayload(admin.QuizImportPayload{
		Title: "Capitals",
		Breaks: []admin.QuizImportBreakPayload{
			{Position: 0, Text: "first"},
			{Position: 2, Text: "second"},
		},
		Questions: []admin.QuizImportQuestionPayload{
			{Text: "Q1", Options: []admin.QuizImportOptionPayload{{Text: "a", Correct: true}}},
			{Text: "Q2", Options: []admin.QuizImportOptionPayload{{Text: "b", Correct: true}}},
		},
	})

	if got, want := len(breaks), 2; got != want {
		t.Fatalf("len(breaks) = %d, want %d", got, want)
	}
	if got, want := breaks[0].Text, "first"; got != want {
		t.Errorf("breaks[0].Text = %q, want %q (order must be preserved)", got, want)
	}
	if got, want := breaks[1].Text, "second"; got != want {
		t.Errorf("breaks[1].Text = %q, want %q (order must be preserved)", got, want)
	}
}

// TestValidateImportBreaks_DuplicatePosition pins the payload-side
// duplicate-slot check so a two-breaks-at-position-2 payload fails
// fast at the form layer rather than via the SQLite unique index.
func TestValidateImportBreaks_DuplicatePosition(t *testing.T) {
	t.Parallel()

	qz := &quiz.Quiz{
		Questions: []*quiz.Question{
			{Position: 1}, {Position: 2}, {Position: 3},
		},
	}
	breaks := []*quiz.Break{
		{Position: 2, Text: "first"},
		{Position: 2, Text: "second"},
	}

	msg := admin.ValidateImportBreaks(t.Context(), qz, breaks)
	if got, want := msg, "position 2 appears twice"; !strings.Contains(got, want) {
		t.Errorf("ValidateImportBreaks msg = %q, should contain %q", got, want)
	}
}

// TestValidateImportBreaks_UnknownPosition pins that a break pointing
// at a position no question carries gets the same rejection the form
// path's breakForm.Valid produces, and that the message names the
// offending slot so the admin can fix the right break.
func TestValidateImportBreaks_UnknownPosition(t *testing.T) {
	t.Parallel()

	qz := &quiz.Quiz{
		Questions: []*quiz.Question{{Position: 1}, {Position: 2}},
	}
	breaks := []*quiz.Break{{Position: 5, Text: "huh?"}}

	msg := admin.ValidateImportBreaks(t.Context(), qz, breaks)
	if got, want := msg, "break at position 5"; !strings.Contains(got, want) {
		t.Errorf("ValidateImportBreaks msg = %q, should contain %q", got, want)
	}
}

// TestValidateImportBreaks_CollectsMultipleProblems pins the
// collect-all-then-return behaviour: an LLM-generated payload with two
// distinct mistakes gets both surfaced on a single submit, not one per
// round-trip.
func TestValidateImportBreaks_CollectsMultipleProblems(t *testing.T) {
	t.Parallel()

	qz := &quiz.Quiz{
		Questions: []*quiz.Question{{Position: 1}, {Position: 2}},
	}
	breaks := []*quiz.Break{
		{Position: 5, Text: "unknown slot"},
		{Position: 9, Text: "another unknown slot"},
	}

	msg := admin.ValidateImportBreaks(t.Context(), qz, breaks)
	for _, want := range []string{"position 5", "position 9"} {
		if !strings.Contains(msg, want) {
			t.Errorf("ValidateImportBreaks msg = %q, should contain %q", msg, want)
		}
	}
}

// TestValidateImportBreaks_ValidPayload pins the happy path so the
// negative-case tests above can't pass by accidentally rejecting
// everything.
func TestValidateImportBreaks_ValidPayload(t *testing.T) {
	t.Parallel()

	qz := &quiz.Quiz{
		Questions: []*quiz.Question{{Position: 1}, {Position: 2}, {Position: 3}},
	}
	breaks := []*quiz.Break{
		{Position: 0, Text: "intro"},
		{Position: 2, Text: "halfway"},
	}

	if got, want := admin.ValidateImportBreaks(t.Context(), qz, breaks), ""; got != want {
		t.Errorf("ValidateImportBreaks = %q, want %q", got, want)
	}
}
