// Shared per-question countdown for the player join surface and the host big screen.
// Both run the same two-phase bar off the server's view of the answer window:
// a read beat that fills 0 -> 100 over [serverNow, startedAt] (options hidden),
// then a drain 100 -> 0 over [startedAt, expiresAt]. Both phases run on the
// server clock (a serverNow() the caller supplies from its own clock-offset
// bookkeeping), never the device wall clock, so a skewed clock cannot desync
// the bar from the players' devices (#180, #247). esbuild inlines this into
// each tree's bundle, so there is no cross-tree runtime fetch.
//
// The component keeps its own reactive progress / revealing fields and its own
// interval handle - Alpine only tracks state declared on the component, so the
// helper drives those through caller-supplied setters and timer hooks rather
// than owning reactive state itself.

// TICK_MS is how often the bar updates while a countdown runs. 100ms is smooth
// enough for a draining bar without thrashing Alpine's reactive updates.
const TICK_MS = 100;

// readBeatProgress returns the read-beat fill (0 -> 100) for the current
// serverNow over [beatStart, startAt], clamped to [0, 100].
export function readBeatProgress(now, beatStart, startAt) {
    const total = startAt - beatStart;
    if (!(total > 0)) return 100;

    return clampPercent(((now - beatStart) / total) * 100);
}

// answerProgress returns the answer-window drain (100 -> 0) for the current
// serverNow over [startAt, endAt], clamped to [0, 100].
export function answerProgress(now, startAt, endAt) {
    const total = endAt - startAt;
    if (!(total > 0)) return 0;

    return clampPercent(((endAt - now) / total) * 100);
}

function clampPercent(value) {
    return Math.max(0, Math.min(100, value));
}

// startQuestionCountdown drives the two-phase bar for a question. It clears any
// prior interval first and re-derives from the absolute server timestamps, so a
// resync mid-question re-anchors the bar. Idempotent across ticks within the
// same question.
//
// hooks:
//   - serverNow():        current time in ms as the server sees it.
//   - setProgress(pct):   set the reactive progress field (0..100).
//   - setRevealing(bool): set the reactive read-beat flag (options hidden while
//                         true).
//   - setTimer(handle):   store the interval handle (null clears it).
//   - clearTimer():       cancel any pending interval.
//   - readBeat:           optional caller-owned {} memo that keeps the read
//                         beat's start across re-calls for the same question.
//
// startedAt / expiresAt are ISO 8601 strings off the question payload. A
// missing or unparseable pair leaves the bar full and revealing off, matching
// the prior per-component behaviour.
export function startQuestionCountdown(question, hooks) {
    hooks.clearTimer();
    const start = question && question.startedAt ? new Date(question.startedAt).getTime() : NaN;
    const end = question && question.expiresAt ? new Date(question.expiresAt).getTime() : NaN;
    if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) {
        hooks.setRevealing(false);
        hooks.setProgress(100);

        return;
    }
    if (hooks.serverNow() < start) {
        startReadBeat(start, end, beatStartFor(question, hooks), hooks);

        return;
    }
    startAnswerCountdown(start, end, hooks);
}

// beatStartFor returns when the read beat for this question began, so a state
// re-read mid-beat does not snap the bar back to 0. startedAt is part of the
// key because a replayed quiz reuses question ids.
function beatStartFor(question, hooks) {
    const memo = hooks.readBeat;
    if (!memo) return hooks.serverNow();
    const key = `${question.id}|${question.startedAt}`;
    if (memo.key !== key) {
        memo.key = key;
        memo.start = hooks.serverNow();
    }

    return memo.start;
}

// startReadBeat fills the bar 0 -> 100 over [beatStart, startAt] while options
// stay hidden, then hands off to startAnswerCountdown the moment the window
// opens.
function startReadBeat(startAt, endAt, beatStart, hooks) {
    hooks.setRevealing(true);
    hooks.setProgress(0);
    const tick = () => {
        const now = hooks.serverNow();
        if (now >= startAt) {
            hooks.setProgress(100);
            hooks.clearTimer();
            hooks.setRevealing(false);
            startAnswerCountdown(startAt, endAt, hooks);

            return;
        }
        hooks.setProgress(readBeatProgress(now, beatStart, startAt));
    };
    tick();
    hooks.setTimer(setInterval(tick, TICK_MS));
}

// startAnswerCountdown drains the bar 100 -> 0 over [startAt, endAt] on the
// server clock. If the window is already past it pins the bar at 0 without
// spinning an interval.
function startAnswerCountdown(startAt, endAt, hooks) {
    hooks.clearTimer();
    hooks.setRevealing(false);
    const tick = () => {
        const pct = answerProgress(hooks.serverNow(), startAt, endAt);
        hooks.setProgress(pct);
        if (pct <= 0) {
            hooks.clearTimer();
        }
    };
    tick();
    if (answerProgress(hooks.serverNow(), startAt, endAt) <= 0) return;
    hooks.setTimer(setInterval(tick, TICK_MS));
}
