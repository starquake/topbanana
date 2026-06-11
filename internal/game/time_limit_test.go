package game_test

import (
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/quiz"
)

// TestResolveAnswerWindow pins the #99 priority chain: question
// override > quiz default > defaultExpiration. The branches are
// independent - each one needs its own subtest to prove the others
// don't accidentally short-circuit it.
func TestResolveAnswerWindow(t *testing.T) {
	t.Parallel()

	intPtr := func(v int) *int { return &v }

	tests := []struct {
		name string
		q    *quiz.Question
		qz   *quiz.Quiz
		want time.Duration
	}{
		{
			name: "question override beats quiz default",
			q:    &quiz.Question{TimeLimitSeconds: intPtr(20)},
			qz:   &quiz.Quiz{TimeLimitSeconds: 30},
			want: 20 * time.Second,
		},
		{
			name: "nil question override falls through to quiz default",
			q:    &quiz.Question{TimeLimitSeconds: nil},
			qz:   &quiz.Quiz{TimeLimitSeconds: 30},
			want: 30 * time.Second,
		},
		{
			name: "both unset falls through to defaultExpiration",
			q:    &quiz.Question{TimeLimitSeconds: nil},
			qz:   &quiz.Quiz{TimeLimitSeconds: 0},
			want: ExportDefaultExpiration,
		},
		{
			name: "zero question override is treated as unset",
			q:    &quiz.Question{TimeLimitSeconds: intPtr(0)},
			qz:   &quiz.Quiz{TimeLimitSeconds: 30},
			want: 30 * time.Second,
		},
		{
			name: "nil question + nil quiz still resolves",
			q:    nil,
			qz:   nil,
			want: ExportDefaultExpiration,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, want := ExportResolveAnswerWindow(tc.q, tc.qz), tc.want; got != want {
				t.Errorf("resolveAnswerWindow(%+v, %+v) = %v, want %v", tc.q, tc.qz, got, want)
			}
		})
	}
}

// TestResolveRoundBoundaryWindow pins the #554 priority chain for the
// round-boundary auto-advance window: round override > quiz default >
// defaultExpiration. The single column drives both the intro and
// recap/results phases.
func TestResolveRoundBoundaryWindow(t *testing.T) {
	t.Parallel()

	intPtr := func(v int) *int { return &v }

	tests := []struct {
		name  string
		round *quiz.Round
		qz    *quiz.Quiz
		want  time.Duration
	}{
		{
			name:  "round override beats quiz default",
			round: &quiz.Round{BoundaryDurationSeconds: intPtr(15)},
			qz:    &quiz.Quiz{TimeLimitSeconds: 30},
			want:  15 * time.Second,
		},
		{
			name:  "nil round override falls through to quiz default",
			round: &quiz.Round{BoundaryDurationSeconds: nil},
			qz:    &quiz.Quiz{TimeLimitSeconds: 30},
			want:  30 * time.Second,
		},
		{
			name:  "both unset falls through to defaultExpiration",
			round: &quiz.Round{BoundaryDurationSeconds: nil},
			qz:    &quiz.Quiz{TimeLimitSeconds: 0},
			want:  ExportDefaultExpiration,
		},
		{
			name:  "zero round override is treated as unset",
			round: &quiz.Round{BoundaryDurationSeconds: intPtr(0)},
			qz:    &quiz.Quiz{TimeLimitSeconds: 30},
			want:  30 * time.Second,
		},
		{
			name:  "nil round + nil quiz still resolves",
			round: nil,
			qz:    nil,
			want:  ExportDefaultExpiration,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, want := ExportResolveRoundBoundaryWindow(tc.round, tc.qz), tc.want; got != want {
				t.Errorf("resolveRoundBoundaryWindow(%+v, %+v) = %v, want %v", tc.round, tc.qz, got, want)
			}
		})
	}
}
