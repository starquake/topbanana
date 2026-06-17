package game

import (
	"context"
	"log/slog"
	"time"
)

// maxPoints is the score awarded for a correct answer landing exactly at
// the start of the answer window; the curve falls linearly to zero at
// the window's end.
const maxPoints = 1000

// CalculateScore calculates the score for a given answer.
func (s *Service) CalculateScore(ctx context.Context, a *Answer) int {
	// TODO: Should this be the points for answering immediately? Or within one second?
	return scoreAnswerCurve(ctx, s.logger, a.Option.Correct, a.Question.StartedAt, a.Question.ExpiredAt, a.AnsweredAt)
}

// ScoreAnswer scores a pick from its timing primitives, letting the
// live-session runner (MP-5 / #682) reuse the exact CalculateScore curve via
// the service it already holds, without building a game.Answer.
func (s *Service) ScoreAnswer(ctx context.Context, correct bool, startedAt, expiredAt, answeredAt time.Time) int {
	return scoreAnswerCurve(ctx, s.logger, correct, startedAt, expiredAt, answeredAt)
}

// scoreAnswerCurve is the pure scoring formula, decoupled from the [Answer]
// struct so [Service.CalculateScore] and [Service.ScoreAnswer] (the seam the
// live-session runner reuses, MP-5 / #682) share one curve without building a
// game.Answer. A wrong pick scores zero, a pick after the window scores zero,
// and a correct pick scores linearly from maxPoints at startedAt down to zero
// at expiredAt.
//
//nolint:revive // correct is the option's correctness (a scoring input), not a behavioural control flag.
func scoreAnswerCurve(
	ctx context.Context, logger *slog.Logger, correct bool, startedAt, expiredAt, answeredAt time.Time,
) int {
	if !correct {
		return 0
	}

	if answeredAt.After(expiredAt) {
		logger.InfoContext(ctx, "score=0, answeredAt > expiredAt, answered too late!")

		return 0
	}

	answerWindow := expiredAt.Sub(startedAt)
	if answerWindow <= 0 {
		// A zero-or-negative window would divide by zero below (+Inf/NaN,
		// and int(NaN) is implementation-defined). Unreachable on the
		// in-tree callers, but this curve is reused via the Scorer
		// interface, so award a correct in-window pick full points.
		return maxPoints
	}

	duration := max(
		// Defensive clamp: a hand-crafted client could POST an answer
		// before startedAt (which sits in the future due to the reveal
		// delay - #247). Without clamping, a negative duration would
		// score above maxPoints. Treat early arrivals as if they landed
		// at startedAt.
		answeredAt.Sub(startedAt), 0)

	return int(float64(maxPoints) - (duration.Seconds() / answerWindow.Seconds() * float64(maxPoints)))
}
