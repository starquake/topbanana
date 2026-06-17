package game

import "time"

// clampTappedAt applies the #237 trust window: the recorded answer time
// is the client-supplied tappedAt when it falls inside [startedAt,
// serverNow], otherwise it's serverNow. The fallback is intentionally
// the upper bound - an out-of-range claim should never give the player
// a faster score than they earned in real time.
func clampTappedAt(tappedAt, startedAt, serverNow time.Time) time.Time {
	if tappedAt.IsZero() || tappedAt.Before(startedAt) || tappedAt.After(serverNow) {
		return serverNow
	}

	return tappedAt
}
