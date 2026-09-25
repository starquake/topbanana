// createReconnectBackoff re-runs a reconnect on a capped exponential delay.
// EventSource gives up for good on a fatal non-200 (e.g. a proxy 502 during a
// deploy), so the live surfaces drive their own re-subscribe (#1179, #1342).
export function createReconnectBackoff({ baseMs = 1000, maxMs = 30000 } = {}) {
    let delay = baseMs;
    let timer = null;

    return {
        // schedule is a no-op while a retry is already pending.
        schedule(fn) {
            if (timer) return;
            const wait = delay;
            delay = Math.min(delay * 2, maxMs);
            timer = setTimeout(() => {
                timer = null;
                fn();
            }, wait);
        },
        reset() {
            delay = baseMs;
        },
        cancel() {
            if (timer) {
                clearTimeout(timer);
                timer = null;
            }
        },
    };
}
