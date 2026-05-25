package admin

import (
	"context"

	"github.com/starquake/topbanana/internal/quiz"
)

// HumanizeTime exposes the unexported humanizeTime helper for tests.
var HumanizeTime = humanizeTime

// AccountTypeLabel exposes the unexported account-type derivation used
// by the admin players list so the per-branch table tests can pin the
// mapping without exporting the helper from the package.
var AccountTypeLabel = accountTypeLabel

// ParsePageParam exposes the unexported ?page= parser so the test can
// pin the clamping rules (blank / negative / non-numeric → 1).
var ParsePageParam = parsePageParam

// TotalPagesFor exposes the unexported ceiling-division helper used by
// the admin players list pagination math.
var TotalPagesFor = totalPagesFor

// PlayersPerPage exposes the admin players list page size so an
// integration test can build a multi-page DB without hard-coding the
// value.
const PlayersPerPage = playersPerPage

// ValidateQuizForm exposes the unexported quizForm.Valid behaviour to
// the external admin_test package so the form-level rules pinned in
// #36 can be tested without exporting the quizForm struct itself.
// The form rules move with the form code; the rest of the codebase
// has no business constructing a quizForm.
func ValidateQuizForm(ctx context.Context, q *quiz.Quiz) map[string]string {
	return (&quizForm{quiz: q}).Valid(ctx)
}
