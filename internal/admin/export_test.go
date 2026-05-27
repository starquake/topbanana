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

// ClientIP exposes the unexported RemoteAddr-only client-IP helper so
// the external admin_test package can pin its parsing rules.
var ClientIP = clientIP

// EmailRateLimiterEntryCount returns how many IPs the limiter is
// tracking right now. Lets the unit test pin the prune-stale-entries
// behaviour without exporting the internal map.
func EmailRateLimiterEntryCount(l *EmailRateLimiter) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.last)
}
