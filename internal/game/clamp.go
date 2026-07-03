package game

import "time"

// maxLatencyRefund bounds the #237 tappedAt refund: enough for real
// latency, too small for a client to claim the window start (#1163).
const maxLatencyRefund = 2 * time.Second

// clampTappedAt records tappedAt only when it lands in
// [serverNow - maxRefund, serverNow], else serverNow. The bounded lower
// edge refunds latency without letting a client claim an arbitrarily early
// instant (#237, #1163).
func clampTappedAt(tappedAt, serverNow time.Time, maxRefund time.Duration) time.Time {
	floor := serverNow.Add(-maxRefund)
	if tappedAt.IsZero() || tappedAt.Before(floor) || tappedAt.After(serverNow) {
		return serverNow
	}

	return tappedAt
}
