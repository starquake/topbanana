package admin

import (
	"context"
	"fmt"

	"github.com/starquake/topbanana/internal/quiz"
)

// quizForm wraps a parsed [quiz.Quiz] for admin-form validation.
// Problem-map keys match the lowercase form-field names the templates
// bind to so the handlers do not need a translation step.
type quizForm struct {
	quiz *quiz.Quiz
}

// Valid checks every form-level rule on the wrapped quiz, its
// questions, and its options. An empty map means the form is valid.
func (f *quizForm) Valid(ctx context.Context) map[string]string {
	problems := make(map[string]string)
	q := f.quiz
	if q.Title == "" {
		problems["title"] = "Title is required"
	}
	if q.Slug == "" {
		problems["slug"] = "Slug is required"
	}
	if q.Description == "" {
		problems["description"] = "Description is required"
	}
	// Only flag the time-limit range when the caller actually set a
	// value; a zero TimeLimitSeconds means "unset" (the store layer
	// rewrites it to DefaultTimeLimitSeconds before INSERT), so we
	// must not reject the zero-value case that test fixtures and the
	// JSON-import path both rely on.
	if q.TimeLimitSeconds != 0 &&
		(q.TimeLimitSeconds < quiz.MinTimeLimitSeconds || q.TimeLimitSeconds > quiz.MaxTimeLimitSeconds) {
		problems["timelimitseconds"] = fmt.Sprintf(
			"Time limit must be between %d and %d seconds",
			quiz.MinTimeLimitSeconds, quiz.MaxTimeLimitSeconds,
		)
	}
	// An empty visibility is treated as "public" by the store; only
	// flag genuinely unrecognised values so the admin form's selector
	// can surface them inline.
	if q.Visibility != "" && !quiz.IsValidVisibility(q.Visibility) {
		problems["visibility"] = "Visibility must be one of: public, unlisted, private"
	}
	// An empty mode is treated as "solo" by the store; only flag
	// genuinely unrecognised values so the admin form's selector can
	// surface them inline (MP-0 / #677).
	if q.Mode != "" && !quiz.IsValidMode(q.Mode) {
		problems["mode"] = "Mode must be one of: solo, live"
	}
	// An empty language is treated as "en" by the store; only flag
	// genuinely unrecognised values so the admin form's selector can
	// surface them inline (#1115).
	if q.Language != "" && !quiz.IsValidLanguage(q.Language) {
		problems["language"] = "Language must be one of: en, nl"
	}
	addQuestionProblems(ctx, problems, q.Questions)
	addRoundProblems(ctx, problems, q.Rounds)

	return problems
}

// addQuestionProblems folds each question's (and its options')
// field-level problems into problems under the question-indexed keys the
// admin template binds to.
func addQuestionProblems(ctx context.Context, problems map[string]string, questions []*quiz.Question) {
	for qsIndex, question := range questions {
		qf := &questionForm{question: question}
		for k, v := range qf.Valid(ctx) {
			problems[fmt.Sprintf("questions[%d][%s]", qsIndex, k)] = v
		}
		for oIndex, option := range question.Options {
			of := &optionForm{option: option}
			for k, v := range of.Valid(ctx) {
				problems[fmt.Sprintf("questions[%d].options[%d][%s]", qsIndex, oIndex, k)] = v
			}
		}
	}
}

// addRoundProblems folds each round's field-level problems into problems
// under the round-indexed keys. The JSON-import path populates q.Rounds,
// so this is the only gate that range-checks an imported round's
// boundary_duration_seconds before it reaches the DB CHECK (#554).
func addRoundProblems(ctx context.Context, problems map[string]string, rounds []*quiz.Round) {
	for rIndex, round := range rounds {
		rf := &roundForm{round: round}
		for k, v := range rf.Valid(ctx) {
			problems[fmt.Sprintf("rounds[%d][%s]", rIndex, k)] = v
		}
	}
}

// questionForm wraps a [quiz.Question] for the standalone
// add-question / edit-question admin form. The quizForm above
// composes it for the per-question rules embedded in a quiz save.
type questionForm struct {
	question *quiz.Question
}

// Valid checks the question's field-level rules. The store layer is
// responsible for cross-row invariants (e.g. unique position per
// quiz); this form is purely about input shape.
func (f *questionForm) Valid(_ context.Context) map[string]string {
	problems := make(map[string]string)
	q := f.question
	if q.Text == "" {
		problems["text"] = "Text is required"
	}
	switch {
	case len(q.Options) == 0:
		problems["options"] = "Options are required"
	case len(q.Options) > maxOptions:
		problems["options"] = fmt.Sprintf("A question may have at most %d options", maxOptions)
	default:
		// Option count is in range. Deliberately no correct-option
		// check: a question where the player is meant to pick none is a
		// supported shape.
	}
	if q.TimeLimitSeconds != nil {
		v := *q.TimeLimitSeconds
		if v < quiz.MinTimeLimitSeconds || v > quiz.MaxTimeLimitSeconds {
			problems["timelimitseconds"] = fmt.Sprintf(
				"Time limit must be between %d and %d seconds, or blank to inherit the quiz default",
				quiz.MinTimeLimitSeconds, quiz.MaxTimeLimitSeconds,
			)
		}
	}

	return problems
}

// optionForm wraps a [quiz.Option]; embedded in the per-question
// rules a quiz save evaluates so the renderer can surface text
// errors next to the option row.
type optionForm struct {
	option *quiz.Option
}

// Valid checks the option's field-level rules.
func (f *optionForm) Valid(_ context.Context) map[string]string {
	problems := make(map[string]string)
	if f.option.Text == "" {
		problems["text"] = "Text is required"
	}

	return problems
}
