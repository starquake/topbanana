package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/starquake/topbanana/internal/quiz"
)

// quizForm wraps a parsed [quiz.Quiz] for admin-form validation
// (#36). The form-level rules used to live on the domain struct as
// quiz.Quiz.Valid; moving them here keeps the player-API surface
// (which never receives untrusted input shaped like a quiz) free of
// admin form concerns, and pulls the rules next to the markup that
// renders them.
//
// The form wraps a *quiz.Quiz rather than redeclaring every field so
// the admin handlers' existing fillQuizFromForm flow stays
// untouched — the form is built around the same domain pointer the
// store call already consumes, no second mapping layer required.
type quizForm struct {
	quiz *quiz.Quiz
}

// Valid checks every form-level rule on the wrapped quiz, its
// questions, and its options. Returns a problems map keyed by the
// dotted field path that the per-field renderer (#32) consumes. An
// empty map means the form is valid.
func (f *quizForm) Valid(ctx context.Context) map[string]string {
	problems := make(map[string]string)
	q := f.quiz
	if q.Title == "" {
		problems["Title"] = "Title is required"
	}
	if q.Slug == "" {
		problems["Slug"] = "Slug is required"
	}
	if q.Description == "" {
		problems["Description"] = "Description is required"
	}
	// Only flag the time-limit range when the caller actually set a
	// value; a zero TimeLimitSeconds means "unset" (the store layer
	// rewrites it to DefaultTimeLimitSeconds before INSERT), so we
	// must not reject the zero-value case that test fixtures and the
	// JSON-import path both rely on.
	if q.TimeLimitSeconds != 0 &&
		(q.TimeLimitSeconds < quiz.MinTimeLimitSeconds || q.TimeLimitSeconds > quiz.MaxTimeLimitSeconds) {
		problems["TimeLimitSeconds"] = fmt.Sprintf(
			"Time limit must be between %d and %d seconds",
			quiz.MinTimeLimitSeconds, quiz.MaxTimeLimitSeconds,
		)
	}
	// An empty visibility is treated as "public" by the store; only
	// flag genuinely unrecognised values so the admin form's selector
	// can surface them inline.
	if q.Visibility != "" && !quiz.IsValidVisibility(q.Visibility) {
		problems["Visibility"] = "Visibility must be one of: public, unlisted, private"
	}
	for qsIndex, question := range q.Questions {
		qf := &questionForm{question: question}
		for k, v := range qf.Valid(ctx) {
			problems[fmt.Sprintf("Questions[%d][%s]", qsIndex, k)] = v
		}
		for oIndex, option := range question.Options {
			of := &optionForm{option: option}
			for k, v := range of.Valid(ctx) {
				problems[fmt.Sprintf("Questions[%d].Options[%d][%s]", qsIndex, oIndex, k)] = v
			}
		}
	}

	return problems
}

// questionForm wraps a [quiz.Question] for the standalone
// add-question / edit-question admin form. The quizForm above
// composes it for the per-question rules embedded in a quiz save.
type questionForm struct {
	question *quiz.Question
}

// Valid checks the question's field-level rules. The store layer
// is responsible for cross-row invariants (e.g. unique position per
// quiz); this form is purely about input shape.
func (f *questionForm) Valid(_ context.Context) map[string]string {
	problems := make(map[string]string)
	q := f.question
	if q.Text == "" {
		problems["Text"] = "Text is required"
	}
	if len(q.Options) == 0 {
		problems["Options"] = "Options are required"
	}
	if q.TimeLimitSeconds != nil {
		v := *q.TimeLimitSeconds
		if v < quiz.MinTimeLimitSeconds || v > quiz.MaxTimeLimitSeconds {
			problems["TimeLimitSeconds"] = fmt.Sprintf(
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
		problems["Text"] = "Text is required"
	}

	return problems
}

// lowercaseFormFieldKeys translates the form types' map keys
// ("Title", "Text", "Options", "Description", "Visibility",
// "TimeLimitSeconds") into the lowercased form-field names the
// templates bind to. Pulled out so all three callers (quiz save,
// question save, JSON-import save) format the keys identically;
// keeps the Valid maps untouched so future non-renderer consumers
// can read them in their original shape.
func lowercaseFormFieldKeys(problems map[string]string) map[string]string {
	out := make(map[string]string, len(problems))
	for k, v := range problems {
		out[strings.ToLower(k)] = v
	}

	return out
}
