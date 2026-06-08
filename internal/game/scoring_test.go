package game_test

import (
	"log/slog"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/quiz"
)

// TestScoreAnswerZeroWindow pins the #792 guard: a zero-or-negative
// answer window (StartedAt == ExpiredAt) must not divide by zero. A
// correct in-window pick scores full points (1000) instead of returning
// int(NaN), which is implementation-defined.
func TestScoreAnswerZeroWindow(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	now := time.Now()

	tests := []struct {
		name      string
		correct   bool
		startedAt time.Time
		expiredAt time.Time
		answered  time.Time
		want      int
	}{
		{
			name:      "zero window, correct in-window pick scores full points",
			correct:   true,
			startedAt: now,
			expiredAt: now,
			answered:  now,
			want:      1000,
		},
		{
			name:      "negative window, correct in-window pick scores full points",
			correct:   true,
			startedAt: now,
			expiredAt: now.Add(-time.Second),
			answered:  now.Add(-time.Second),
			want:      1000,
		},
		{
			name:      "zero window, wrong pick still scores zero",
			correct:   false,
			startedAt: now,
			expiredAt: now,
			answered:  now,
			want:      0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ScoreAnswer(t.Context(), logger, tc.correct, tc.startedAt, tc.expiredAt, tc.answered)
			if want := tc.want; got != want {
				t.Errorf("ScoreAnswer() = %d, want %d", got, want)
			}
		})
	}
}

// TestScoreAnswerNormalWindow confirms the linear scoring curve over a
// positive window is unchanged by the #792 guard: full points at the
// start, half points at the midpoint, zero at the edge, zero past it.
func TestScoreAnswerNormalWindow(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	startedAt := time.Now()
	expiredAt := startedAt.Add(10 * time.Second)

	tests := []struct {
		name     string
		correct  bool
		answered time.Time
		want     int
	}{
		{
			name:     "correct at start scores full points",
			correct:  true,
			answered: startedAt,
			want:     1000,
		},
		{
			name:     "correct at midpoint scores half points",
			correct:  true,
			answered: startedAt.Add(5 * time.Second),
			want:     500,
		},
		{
			name:     "correct at edge scores zero",
			correct:  true,
			answered: expiredAt,
			want:     0,
		},
		{
			name:     "correct past edge scores zero",
			correct:  true,
			answered: expiredAt.Add(time.Second),
			want:     0,
		},
		{
			name:     "wrong pick scores zero",
			correct:  false,
			answered: startedAt,
			want:     0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ScoreAnswer(t.Context(), logger, tc.correct, startedAt, expiredAt, tc.answered)
			if want := tc.want; got != want {
				t.Errorf("ScoreAnswer() = %d, want %d", got, want)
			}
		})
	}
}

// TestIntroBoundaryWindowPositive pins the #792 round-boundary guard: a
// quiz whose default time limit is zero must still produce a positive
// boundary window, so the card does not auto-advance the instant it is
// shown (StartedAt == ExpiredAt).
func TestIntroBoundaryWindowPositive(t *testing.T) {
	t.Parallel()

	startedAt, expiredAt := ExportIntroBoundaryWindow(&quiz.Quiz{TimeLimitSeconds: 0})

	if got, want := expiredAt.After(startedAt), true; got != want {
		t.Errorf("ExpiredAt.After(StartedAt) = %v, want %v (ExpiredAt=%v, StartedAt=%v)",
			got, want, expiredAt, startedAt)
	}
	if got, want := expiredAt.Sub(startedAt), ExportDefaultExpiration; got != want {
		t.Errorf("boundary window = %v, want %v", got, want)
	}
}
