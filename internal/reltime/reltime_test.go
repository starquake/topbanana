package reltime_test

import (
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/reltime"
)

func TestHumanizeSince(t *testing.T) {
	t.Parallel()

	// A fixed reference time keeps this deterministic: HumanizeSince takes
	// "now" rather than reading the clock, so there is no scheduling-jitter
	// window for a paused subtest to cross a bucket boundary (#666).
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{"just now (5s ago)", now.Add(-5 * time.Second), "just now"},
		{"1 minute ago", now.Add(-1 * time.Minute), "1 min ago"},
		{"5 minutes ago", now.Add(-5 * time.Minute), "5 min ago"},
		{"1 hour ago", now.Add(-1 * time.Hour), "1 hr ago"},
		{"3 hours ago", now.Add(-3 * time.Hour), "3 hr ago"},
		{"1 day ago", now.Add(-24 * time.Hour), "1 day ago"},
		{"5 days ago", now.Add(-5 * 24 * time.Hour), "5 days ago"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got, want := reltime.HumanizeSince(now, tc.t), tc.want; got != want {
				t.Errorf("HumanizeSince(now, %v) = %q, want %q", tc.t, got, want)
			}
		})
	}
}
