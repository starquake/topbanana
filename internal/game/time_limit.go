package game

import (
	"time"

	"github.com/starquake/topbanana/internal/quiz"
)

// defaultExpiration is the answer-window fallback used when neither the
// question nor the quiz sets a time limit.
const defaultExpiration = 10 * time.Second

// resolveAnswerWindow picks the per-question answer window from #99's
// priority chain: the question's own time_limit_seconds wins; falling
// back to the quiz default; falling back to defaultExpiration when both
// are unset or zero. Returning a [time.Duration] keeps the call site
// arithmetic identical to the prior hard-coded path.
func resolveAnswerWindow(q *quiz.Question, qz *quiz.Quiz) time.Duration {
	if q != nil && q.TimeLimitSeconds != nil && *q.TimeLimitSeconds > 0 {
		return time.Duration(*q.TimeLimitSeconds) * time.Second
	}
	if qz != nil && qz.TimeLimitSeconds > 0 {
		return time.Duration(qz.TimeLimitSeconds) * time.Second
	}

	return defaultExpiration
}

// resolveRoundBoundaryWindow picks the round-boundary auto-advance window
// (shared by the intro and recap/results cards) from #554's priority
// chain: the round's own boundary_duration_seconds wins; falling back to
// the quiz default; falling back to defaultExpiration when both are unset
// or zero.
func resolveRoundBoundaryWindow(round *quiz.Round, qz *quiz.Quiz) time.Duration {
	if round != nil && round.BoundaryDurationSeconds != nil && *round.BoundaryDurationSeconds > 0 {
		return time.Duration(*round.BoundaryDurationSeconds) * time.Second
	}

	return resolveAnswerWindow(nil, qz)
}
