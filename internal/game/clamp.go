package game

import "time"

// maxLatencyRefund bounds how far the #237 client tappedAt refund can
// pull the recorded answer time earlier than the server received it.
// Large enough to cover realistic network latency, small enough that a
// crafted client cannot claim the window start (tappedAt == StartedAt) to
// dodge the scoring curve and take full points after unlimited real time
// (#1163).
const maxLatencyRefund = 2 * time.Second

// clampTappedAt applies the #237 latency refund with a bounded window
// (#1163): the recorded answer time is the client-supplied tappedAt only
// when it falls inside [serverNow - maxRefund, serverNow]; otherwise it
// is serverNow. Bounding the backward reach to maxRefund lets an honest
// player on a slow link claw back their network latency without letting a
// crafted client claim an arbitrarily early instant. The out-of-range
// fallback is the upper bound so a claim can never score faster than the
// player earned in real time.
func clampTappedAt(tappedAt, serverNow time.Time, maxRefund time.Duration) time.Time {
	floor := serverNow.Add(-maxRefund)
	if tappedAt.IsZero() || tappedAt.Before(floor) || tappedAt.After(serverNow) {
		return serverNow
	}

	return tappedAt
}
