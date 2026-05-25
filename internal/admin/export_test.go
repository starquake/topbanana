package admin

import (
	"context"

	"github.com/starquake/topbanana/internal/quiz"
)

// HumanizeTime exposes the unexported humanizeTime helper for tests.
var HumanizeTime = humanizeTime

// ValidateQuizForm exposes the unexported quizForm.Valid behaviour to
// the external admin_test package so the form-level rules pinned in
// #36 can be tested without exporting the quizForm struct itself.
// The form rules move with the form code; the rest of the codebase
// has no business constructing a quizForm.
func ValidateQuizForm(ctx context.Context, q *quiz.Quiz) map[string]string {
	return (&quizForm{quiz: q}).Valid(ctx)
}
