// Shared host-armed last-call countdown for the host big screen and the player lobby
// (#735). Both render the same "Starting in M:SS" off the absolute start_at
// deadline minus the server's view of "now" (a serverNow() the caller supplies
// from its own clock-offset bookkeeping), never the device wall clock, so a
// skewed clock cannot desync the countdown across surfaces. esbuild inlines
// this into each tree's bundle, so there is no cross-tree runtime fetch.
//
// The component keeps its own reactive remaining-seconds field and its own
// interval handle - Alpine only tracks state declared on the component, so the
// helper drives those through caller-supplied setters and timer hooks rather
// than owning reactive state itself.

// TICK_MS is how often the countdown updates. 250ms keeps the M:SS label
// landing on its second boundary promptly without thrashing Alpine.
const TICK_MS = 250;

// remainingSeconds returns whole seconds left until deadlineMs from the
// server's now, rounded up so a deadline 0.4s away still reads "1" rather than
// "0", and clamped at 0.
export function remainingSeconds(nowMs, deadlineMs) {
    return Math.max(0, Math.ceil((deadlineMs - nowMs) / 1000));
}

// formatCountdown renders whole seconds as "M:SS" (e.g. 59 -> "0:59", 65 ->
// "1:05"). Negative input clamps to "0:00".
export function formatCountdown(totalSeconds) {
    const safe = Math.max(0, Math.floor(totalSeconds));
    const minutes = Math.floor(safe / 60);
    const seconds = safe % 60;

    return `${minutes}:${String(seconds).padStart(2, '0')}`;
}

// startStartCountdown drives the "Starting in M:SS" countdown for an armed
// deadline. It clears any prior interval first and re-derives from the absolute
// startAt timestamp, so a resync re-anchors the label. Idempotent across ticks
// within the same armed deadline.
//
// hooks:
//   - serverNow():       current time in ms as the server sees it.
//   - setRemaining(sec): set the reactive remaining-seconds field.
//   - setTimer(handle):  store the interval handle (null clears it).
//   - clearTimer():      cancel any pending interval.
//
// startAt is an ISO 8601 string (the state payload's startAt). A missing or
// unparseable value clears the countdown (remaining 0, no interval), matching
// "no countdown armed".
export function startStartCountdown(startAt, hooks) {
    hooks.clearTimer();
    const deadline = startAt ? new Date(startAt).getTime() : NaN;
    if (!Number.isFinite(deadline)) {
        hooks.setRemaining(0);

        return;
    }
    const tick = () => {
        const left = remainingSeconds(hooks.serverNow(), deadline);
        hooks.setRemaining(left);
        if (left <= 0) {
            hooks.clearTimer();
        }
    };
    tick();
    if (remainingSeconds(hooks.serverNow(), deadline) <= 0) return;
    hooks.setTimer(setInterval(tick, TICK_MS));
}
