// Package reltime formats timestamps as coarse, human-readable relative
// strings (e.g. "3 hr ago"). It is shared by the admin and host surfaces,
// which both render the same quiz-card partial.
package reltime

import (
	"fmt"
	"time"
)

// hoursPerDay is the bucket size for switching from hours to days.
const hoursPerDay = 24

// Humanize returns a coarse relative-time string for t (e.g. "3 hr ago"),
// measured against the current time.
func Humanize(t time.Time) string {
	return HumanizeSince(time.Now(), t)
}

// HumanizeSince is the pure relative-time formatter, with the reference "now"
// passed in rather than read from the clock. Splitting it out keeps the
// formatting deterministic and testable: a test passes a fixed now instead of
// racing [time.Now] against scheduling jitter (#666).
func HumanizeSince(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 min ago"
		}

		return fmt.Sprintf("%d min ago", m)
	case d < hoursPerDay*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hr ago"
		}

		return fmt.Sprintf("%d hr ago", h)
	default:
		days := int(d.Hours() / hoursPerDay)
		if days == 1 {
			return "1 day ago"
		}

		return fmt.Sprintf("%d days ago", days)
	}
}
