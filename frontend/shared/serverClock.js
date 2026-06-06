// Shared server-clock helpers for the player-client surfaces (the solo game
// and the join / in-game surface). Both reconcile a clock offset from the
// `serverNow` carried on each question / state payload so the per-question
// countdown runs against the server's view of "now" instead of the device's,
// which can be minutes off on phones with stale time (#180). These are pure
// functions; each component keeps its own reactive `clockOffset` field so
// Alpine can track it.

// clockOffsetFromServerNow parses an ISO 8601 serverNow string and returns the
// millisecond offset to add to Date.now() to reach the server's clock. Returns
// null when the value is missing or unparseable, so callers leave their prior
// offset untouched rather than snapping it to zero (the existing
// skew-vulnerable-but-stable behaviour).
export function clockOffsetFromServerNow(serverNowString) {
    if (!serverNowString) return null;
    const serverMs = new Date(serverNowString).getTime();
    if (!Number.isFinite(serverMs)) return null;
    return serverMs - Date.now();
}

// serverTime returns the current time in ms as the server sees it, applying
// the offset captured from the last payload. All per-question countdown math
// goes through this so a skewed device clock can't push the timer past the
// server window in either direction (#180).
export function serverTime(offset) {
    return Date.now() + offset;
}
