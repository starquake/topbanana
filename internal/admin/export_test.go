package admin

import (
	"context"

	"github.com/starquake/topbanana/internal/quiz"
)

// HumanizeTime exposes the unexported humanizeTime helper for tests.
var HumanizeTime = humanizeTime

// CanEditQuiz exposes the unexported creator-or-admin edit
// predicate (#281/#319) so the external admin_test package can pin the
// creator / admin / unrelated-host matrix without driving the
// full HTTP handler stack.
var CanEditQuiz = canEditQuiz

// NavSection exposes the unexported path-to-nav-section helper so the
// external admin_test package can pin the prefix mapping without
// exporting it from the package (#517).
var NavSection = navSection

// AccountTypeLabel exposes the unexported account-type derivation used
// by the admin players list so the per-branch table tests can pin the
// mapping without exporting the helper from the package.
var AccountTypeLabel = accountTypeLabel

// ParsePageParam exposes the unexported ?page= parser so the test can
// pin the clamping rules (blank / negative / non-numeric -> 1).
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

// ValidateBreakForm exposes the unexported breakForm.Valid behaviour
// for the same reason as ValidateQuizForm above (#167).
func ValidateBreakForm(ctx context.Context, q *quiz.Quiz, b *quiz.Break) map[string]string {
	return (&breakForm{quiz: q, brk: b}).Valid(ctx)
}

// BuildSequence exposes the unexported sequence-merging helper so the
// external admin_test package can pin the interleave order without
// running the full HTTP handler stack (#167).
var BuildSequence = buildSequence

// BuildSlotOptions exposes the unexported "Insert after" dropdown
// builder so the external admin_test package can pin its labelling
// and truncation rules (#167).
var BuildSlotOptions = buildSlotOptions

// DefaultCreateSlot exposes the unexported create-form default-slot
// helper so the external admin_test package can pin the
// last-question-vs-empty-quiz fork (#167).
var DefaultCreateSlot = defaultCreateSlot

// NewEmailRateLimiterWithClock exposes the internal clock-injected
// rate-limiter constructor so the external admin_test package can pin
// the per-IP cool-down without sleeping (#321).
var NewEmailRateLimiterWithClock = newEmailRateLimiterWithClock

// EmailRateLimiterEntryCount returns how many IPs the limiter is
// tracking right now. Lets the unit test pin the prune-stale-entries
// behaviour without exporting the internal map.
func EmailRateLimiterEntryCount(l *EmailRateLimiter) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.last)
}

// DispatchAdminResendVerification exposes the unexported resend-email
// dispatcher so the test package can pin its "did I actually dispatch"
// boolean contract: false (and no audit row) when email is not
// configured, true when it spawns the send.
var DispatchAdminResendVerification = dispatchAdminResendVerification

// QuizImportPayload exposes the unexported import wire-shape to the
// external admin_test package so the per-branch translation tests can
// build payloads without exporting the struct.
type QuizImportPayload = quizImportPayload

// QuizImportQuestionPayload is the question half of [QuizImportPayload].
type QuizImportQuestionPayload = quizImportQuestionPayload

// QuizImportOptionPayload is the option half of [QuizImportPayload].
type QuizImportOptionPayload = quizImportOptionPayload

// QuizImportBreakPayload is the break half of [QuizImportPayload].
type QuizImportBreakPayload = quizImportBreakPayload

// QuizFromImportPayload exposes the unexported import-translation
// helper so the test package can pin the payload-to-domain mapping
// without spinning the full HTTP handler.
var QuizFromImportPayload = quizFromImportPayload

// ValidateImportBreaks exposes the import-side break validator so the
// test package can pin the duplicate-position and slot-mismatch rules.
var ValidateImportBreaks = validateImportBreaks

// ValidateQuestionForm exposes the unexported questionForm.Valid
// behaviour so the option-count and at-least-one-correct rules can be
// tested directly without constructing a full quiz.
func ValidateQuestionForm(ctx context.Context, q *quiz.Question) map[string]string {
	return (&questionForm{question: q}).Valid(ctx)
}

// MaxOptions exposes the per-question option cap so tests can build a
// payload one over the limit without hard-coding the value.
const MaxOptions = maxOptions
