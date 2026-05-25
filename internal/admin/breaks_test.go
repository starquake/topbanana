package admin_test

import (
	"context"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/quiz"
)

// TestBuildSequence pins the interleave order documented on the Break
// type: a break with position 0 sits before the first question, a
// break with position N sits immediately after the question whose
// position is N (#167).
func TestBuildSequence(t *testing.T) {
	t.Parallel()

	t.Run("position 0 break sits before the first question", func(t *testing.T) {
		t.Parallel()

		questions := []*QuestionData{
			{ID: 1, Position: 1, Text: "Q1"},
			{ID: 2, Position: 2, Text: "Q2"},
		}
		breaks := []*BreakData{
			{ID: 10, Position: 0, Text: "intro"},
		}

		seq := BuildSequence(questions, breaks)
		if got, want := len(seq), 3; got != want {
			t.Fatalf("len(seq) = %d, want %d", got, want)
		}
		if got, want := seq[0].Kind, "break"; got != want {
			t.Errorf("seq[0].Kind = %q, want %q", got, want)
		}
		if got, want := seq[1].Kind, "question"; got != want {
			t.Errorf("seq[1].Kind = %q, want %q", got, want)
		}
		if got, want := seq[2].Kind, "question"; got != want {
			t.Errorf("seq[2].Kind = %q, want %q", got, want)
		}
	})

	t.Run("position N break sits after question at position N", func(t *testing.T) {
		t.Parallel()

		questions := []*QuestionData{
			{ID: 1, Position: 1, Text: "Q1"},
			{ID: 2, Position: 2, Text: "Q2"},
			{ID: 3, Position: 3, Text: "Q3"},
		}
		breaks := []*BreakData{
			{ID: 10, Position: 2, Text: "after Q2"},
		}

		seq := BuildSequence(questions, breaks)
		if got, want := len(seq), 4; got != want {
			t.Fatalf("len(seq) = %d, want %d", got, want)
		}
		// Order: Q1, Q2, break(after Q2), Q3
		wantKinds := []string{"question", "question", "break", "question"}
		for i, want := range wantKinds {
			if got := seq[i].Kind; got != want {
				t.Errorf("seq[%d].Kind = %q, want %q", i, got, want)
			}
		}
	})

	t.Run("question indices are assigned in iteration order", func(t *testing.T) {
		t.Parallel()

		questions := []*QuestionData{
			{ID: 1, Position: 1, Text: "Q1"},
			{ID: 2, Position: 2, Text: "Q2"},
		}
		breaks := []*BreakData{
			{ID: 10, Position: 0},
			{ID: 11, Position: 1},
		}

		seq := BuildSequence(questions, breaks)
		// Seq: break, Q1, break, Q2 - the question items must carry
		// indices 0 and 1 so the partial's move-up/down disable state
		// is right.
		for i, item := range seq {
			if item.Kind != "question" {
				continue
			}
			if got, want := item.QuestionIndex, item.Question.Position-1; got != want {
				t.Errorf("seq[%d].QuestionIndex = %d, want %d", i, got, want)
			}
		}
	})
}

// TestBuildSlotOptions pins the (Beginning)+per-question option list
// rendered by the break form's "Insert after" dropdown (#167).
func TestBuildSlotOptions(t *testing.T) {
	t.Parallel()

	t.Run("empty quiz exposes only the Beginning slot", func(t *testing.T) {
		t.Parallel()

		opts := BuildSlotOptions(nil)
		if got, want := len(opts), 1; got != want {
			t.Fatalf("len(opts) = %d, want %d", got, want)
		}
		if got, want := opts[0].Position, 0; got != want {
			t.Errorf("opts[0].Position = %d, want %d", got, want)
		}
		if got, want := opts[0].Label, "(Beginning)"; got != want {
			t.Errorf("opts[0].Label = %q, want %q", got, want)
		}
	})

	t.Run("each question contributes one entry keyed by position", func(t *testing.T) {
		t.Parallel()

		questions := []*quiz.Question{
			{Position: 1, Text: "capital of France?"},
			{Position: 2, Text: "capital of Spain?"},
		}

		opts := BuildSlotOptions(questions)
		if got, want := len(opts), 3; got != want {
			t.Fatalf("len(opts) = %d, want %d", got, want)
		}
		if got, want := opts[1].Position, 1; got != want {
			t.Errorf("opts[1].Position = %d, want %d", got, want)
		}
		if got, want := opts[1].Label, "Question 1: capital of France?"; got != want {
			t.Errorf("opts[1].Label = %q, want %q", got, want)
		}
	})

	t.Run("long question text is truncated", func(t *testing.T) {
		t.Parallel()

		long := strings.Repeat("x", 200)
		questions := []*quiz.Question{{Position: 1, Text: long}}

		opts := BuildSlotOptions(questions)
		if got, want := opts[1].Label, "..."; !strings.HasSuffix(got, want) {
			t.Errorf("opts[1].Label = %q, want suffix %q", got, want)
		}
	})
}

// TestDefaultCreateSlot pins the create-form default-position rule
// from the spec: empty quiz picks (Beginning); otherwise the last
// question (#167).
func TestDefaultCreateSlot(t *testing.T) {
	t.Parallel()

	if got, want := DefaultCreateSlot(nil), 0; got != want {
		t.Errorf("DefaultCreateSlot(nil) = %d, want %d", got, want)
	}

	questions := []*quiz.Question{
		{Position: 1},
		{Position: 2},
		{Position: 3},
	}
	if got, want := DefaultCreateSlot(questions), 3; got != want {
		t.Errorf("DefaultCreateSlot(...) = %d, want %d", got, want)
	}
}

// TestBreakForm_Valid pins the validation rules attached to the
// "Insert after" dropdown: position 0 is always valid, a positive
// position has to match an existing question on the quiz, and a
// negative position is a programmer-error reject (#167).
func TestBreakForm_Valid(t *testing.T) {
	t.Parallel()

	qz := &quiz.Quiz{
		ID: 1,
		Questions: []*quiz.Question{
			{Position: 1},
			{Position: 2},
		},
	}

	t.Run("position 0 is valid", func(t *testing.T) {
		t.Parallel()
		problems := ValidateBreakForm(context.Background(), qz, &quiz.Break{Position: 0})
		if got, want := len(problems), 0; got != want {
			t.Errorf("len(problems) = %d, want %d (%v)", got, want, problems)
		}
	})

	t.Run("position matching a question is valid", func(t *testing.T) {
		t.Parallel()
		problems := ValidateBreakForm(context.Background(), qz, &quiz.Break{Position: 2})
		if got, want := len(problems), 0; got != want {
			t.Errorf("len(problems) = %d, want %d (%v)", got, want, problems)
		}
	})

	t.Run("position pointing at no question is invalid", func(t *testing.T) {
		t.Parallel()
		problems := ValidateBreakForm(context.Background(), qz, &quiz.Break{Position: 99})
		if got, want := problems["position"], "no longer exists"; !strings.Contains(got, want) {
			t.Errorf("problems[position] = %q, want substring %q", got, want)
		}
	})

	t.Run("negative position is invalid", func(t *testing.T) {
		t.Parallel()
		problems := ValidateBreakForm(context.Background(), qz, &quiz.Break{Position: -1})
		if got, want := problems["position"], "Pick a slot"; !strings.Contains(got, want) {
			t.Errorf("problems[position] = %q, want substring %q", got, want)
		}
	})
}
