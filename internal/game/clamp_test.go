package game_test

import (
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/game"
)

// TestClampTappedAt pins the bounded #237 refund: tappedAt is kept only
// inside [serverNow - maxRefund, serverNow], else serverNow (#1163).
func TestClampTappedAt(t *testing.T) {
	t.Parallel()

	serverNow := time.Date(2026, 5, 22, 12, 0, 8, 0, time.UTC)
	const maxRefund = 2 * time.Second
	floor := serverNow.Add(-maxRefund)
	tests := []struct {
		name     string
		tappedAt time.Time
		want     time.Time
	}{
		{
			name:     "tap inside the refund window is recorded as-is",
			tappedAt: serverNow.Add(-1 * time.Second),
			want:     serverNow.Add(-1 * time.Second),
		},
		{
			name:     "tap equal to the refund floor is recorded as-is",
			tappedAt: floor,
			want:     floor,
		},
		{
			name:     "tap equal to serverNow is recorded as-is",
			tappedAt: serverNow,
			want:     serverNow,
		},
		{
			name:     "tap before the refund floor clamps to serverNow",
			tappedAt: serverNow.Add(-5 * time.Second),
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
			if got, want := ExportClampTappedAt(tc.tappedAt, serverNow, maxRefund), tc.want; !got.Equal(want) {
				t.Errorf("clampTappedAt(%v, %v, %v) = %v, want %v", tc.tappedAt, serverNow, maxRefund, got, want)
			}
		})
	}
}
