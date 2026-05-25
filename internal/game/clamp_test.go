package game_test

import (
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/game"
)

// TestClampTappedAt pins the #237 trust window. The recorded AnsweredAt
// must equal the client's tappedAt only when it lands inside
// [startedAt, serverNow] — anything else falls back to serverNow, never
// to tappedAt. The asymmetry matters: a clamp that returned the closer
// edge of the window would let a forward-skewed client get a faster
// score than they actually earned.
func TestClampTappedAt(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	serverNow := startedAt.Add(8 * time.Second)
	tests := []struct {
		name     string
		tappedAt time.Time
		want     time.Time
	}{
		{
			name:     "valid tap inside the window is recorded as-is",
			tappedAt: startedAt.Add(3 * time.Second),
			want:     startedAt.Add(3 * time.Second),
		},
		{
			name:     "tap equal to startedAt is recorded as-is",
			tappedAt: startedAt,
			want:     startedAt,
		},
		{
			name:     "tap equal to serverNow is recorded as-is",
			tappedAt: serverNow,
			want:     serverNow,
		},
		{
			name:     "tap before startedAt clamps to serverNow",
			tappedAt: startedAt.Add(-5 * time.Second),
			want:     serverNow,
		},
		{
			name:     "tap after serverNow clamps to serverNow",
			tappedAt: serverNow.Add(1 * time.Second),
			want:     serverNow,
		},
		{
			name:     "zero tappedAt clamps to serverNow",
			tappedAt: time.Time{},
			want:     serverNow,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, want := ExportClampTappedAt(tc.tappedAt, startedAt, serverNow), tc.want; !got.Equal(want) {
				t.Errorf("clampTappedAt(%v, %v, %v) = %v, want %v", tc.tappedAt, startedAt, serverNow, got, want)
			}
		})
	}
}
