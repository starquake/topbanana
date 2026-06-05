import { sessionService } from '../services/SessionService.js';
import { runAnim } from '../util/anim.js';

// JOIN_PATH_PATTERN matches /join/<code>, capturing the room code. The bare
// /join entry (enter-code form) has no capture group, so the component falls
// back to the typed-code phase there.
const JOIN_PATH_PATTERN = /^\/join\/([^/]+)\/?$/;

// QUESTION_OPTION_TONES cycles the four answer-button tones over a question's
// options, matching the solo client's per-index Kahoot-style colours.
const QUESTION_OPTION_TONES = ['btn-answer-tone-a', 'btn-answer-tone-b', 'btn-answer-tone-c', 'btn-answer-tone-d'];

// STANDINGS_BAR_DURATION is how long the between-rounds bar graph spends
// growing each bar from its pre-round total to its new total (ms).
const STANDINGS_BAR_DURATION = 900;

// JoinApp is the Alpine component behind the player join + lobby + in-game
// surface (MP-4 / #681, MP-7 / #684). It is deliberately separate from gameApp
// (the solo client) and from the host/TV surface: it owns only the player's
// own join form, lobby view, and synchronized question play.
//
// `phase` is the local join-flow stage:
//   - 'code'  : no room code yet - the PC enter-code form.
//   - 'name'  : code known, not joined - the display-name form.
//   - 'lobby' : joined - everything from the lobby roster through play and the
//               final standings is driven by the server phase on state.phase.
//
// Once in the 'lobby' stage the view rendered is decided by state.phase (the
// server-authoritative session phase): lobby roster, round_intro, question,
// reveal, round_results, or finished. The player never advances the phase -
// the runner does, server-side - they only submit one answer per question.
//
// Everything follows the frozen contract: the SSE channel carries only
// {version, phase} ticks, so every tick (and the initial frame on subscribe)
// triggers a fresh GET /state. The component never reads game data off the
// stream; GET /state is the only authoritative read. The per-question
// countdown runs off QuestionExpiresAt minus the server clock (derived from
// the state response's serverNow), never the device wall clock - the same
// technique the solo client uses.
export class JoinApp {
    constructor() {
        // 'code' | 'name' | 'lobby'.
        this.phase = 'code';
        // The room code the player is joining. Upper-cased for display; the
        // server is case-insensitive, but normalizing keeps the UI tidy and
        // the deep-link target consistent.
        this.code = '';
        // Bound to the enter-code input on the 'code' phase.
        this.codeInput = '';
        // Bound to the display-name input on the 'name' phase.
        this.displayName = '';
        // The name the player actually landed with (post collision-fallback),
        // shown in the lobby header so they can spot their own row.
        this.myDisplayName = '';
        // True while a join / ready request is in flight, to guard buttons.
        this.busy = false;
        // Human-readable error for the current form, cleared on retry.
        this.error = '';
        // Authoritative lobby state from GET /state. Null until the first
        // read lands. Shape: { joinCode, phase, hostId, players[], quiz }.
        this.state = null;
        // The viewer's own ready flag, mirrored from the roster so the toggle
        // reflects the server truth after every state read.
        this.isReady = false;
        // Surfaces when the lobby state is gone (session ended / not a
        // participant) so the player isn't stranded on a frozen roster.
        this.lobbyClosed = false;
        // The SSE subscription handle, closed on teardown and before re-open.
        this.eventSource = null;

        // --- In-game state (MP-7 / #684) -------------------------------------
        // Offset between the server clock and Date.now() in ms, refreshed from
        // serverNow in every state read. serverTime() applies it so the
        // per-question countdown runs against the server's view of "now"
        // rather than a skewed device clock (mirrors the solo client, #180).
        this.clockOffset = 0;
        // Drives the per-question countdown bar: 100 at the start of the answer
        // window, draining to 0 at the deadline.
        this.questionProgress = 100;
        // Interval handle for the countdown, cleared before each new countdown
        // and on teardown.
        this.questionTimer = null;
        // The question id the countdown / answered flag are currently tracking,
        // so a state refresh only resets them when the runner moves to a new
        // question (not on every tick within the same one).
        this.currentQuestionId = null;
        // The option the player picked for the current question, or null before
        // they answer. Drives the "answered, waiting" state and (at reveal) the
        // highlight of their own pick.
        this.pickedOptionId = null;
        // True while the answer POST is in flight, to guard the option buttons.
        this.submitting = false;
        // Surfaces a retry banner when an answer POST throws (5xx / network).
        // Cleared on the next pick. A 409 (window closed) is not an error: the
        // player still lands in the answered/waiting state.
        this.answerError = false;

        // --- Between-rounds bar graph (MP-9 / #686) -------------------------
        // Rendered rows for the round_results / finished standings bar graph.
        // Each row is { playerId, displayName, rank, total, preTotal, isMe,
        // displayTotal }, where displayTotal is the animated value the bar
        // width + numeric label bind to. The array is held in rank order (the
        // server already returns standings best-first) so the rows end sorted
        // with the leaders on top once the animation settles.
        this.standingsBars = [];
        // The largest total across the current standings, so each bar's width
        // maps to its share of the leader's bar (leader = full width). Guarded
        // to at least 1 so a zero-score round never divides by zero.
        this.maxStandingsTotal = 1;
        // Identifies the round the current bar graph is animating so the
        // grow-and-resort animation fires once per round_results entry rather
        // than on every SSE tick within the same phase - mirrors how
        // syncQuestionFromState only resets on a genuine question change. The
        // key combines the phase and the current question id (the last
        // question of the round just finished) so a new round retriggers it.
        this.lastStandingsKey = null;
    }

    // init resolves the room code from the URL. A /join/{code} deep link lands
    // straight on the name form; the bare /join entry shows the enter-code
    // form first.
    init() {
        const match = JOIN_PATH_PATTERN.exec(window.location.pathname);
        if (match) {
            this.code = decodeURIComponent(match[1]).toUpperCase();
            this.phase = 'name';
        }
        // Closing the stream on unload avoids leaking a server-side
        // subscriber when the player navigates away or closes the tab; clearing
        // the question timer stops a stale countdown interval from firing.
        window.addEventListener('beforeunload', () => {
            this.closeStream();
            this.clearQuestionTimer();
        });
    }

    // submitCode advances from the enter-code form to the name form. It does
    // not hit the network - the code is validated by the join attempt itself,
    // so a bad code surfaces as the same "no game found" message either way.
    submitCode() {
        const trimmed = (this.codeInput || '').trim().toUpperCase();
        if (trimmed === '') {
            this.error = 'Please enter a code.';
            return;
        }
        this.error = '';
        this.code = trimmed;
        this.phase = 'name';
    }

    // submitName posts the join. On success it captures the landed display
    // name, seeds the ready flag from the response, switches to the lobby,
    // and opens the SSE subscription. A notFound result bounces back to the
    // code form so the player can fix a typo.
    async submitName() {
        if (this.busy) return;
        const trimmed = (this.displayName || '').trim();
        if (trimmed === '') {
            this.error = 'Please enter a name.';
            return;
        }
        this.busy = true;
        this.error = '';
        try {
            const result = await sessionService.join(this.code, trimmed);
            if (!result.ok) {
                this.error = result.message;
                if (result.kind === 'notFound') {
                    // Send them back to fix the code rather than retyping a
                    // name against a room that doesn't exist.
                    this.phase = 'code';
                    this.codeInput = this.code;
                }
                return;
            }
            this.myDisplayName = result.displayName;
            this.isReady = result.isReady;
            this.phase = 'lobby';
            await this.refreshState();
            this.subscribe();
        } finally {
            this.busy = false;
        }
    }

    // toggleReady flips the viewer's ready flag optimistically, posts it, and
    // relies on the SSE tick -> refreshState to confirm. On failure it rolls
    // the optimistic flip back and shows a retry banner.
    async toggleReady() {
        if (this.busy) return;
        const next = !this.isReady;
        this.busy = true;
        this.error = '';
        this.isReady = next;
        try {
            await sessionService.setReady(this.code, next);
        } catch {
            this.isReady = !next;
            this.error = "Couldn't update your ready state - try again.";
            return;
        } finally {
            this.busy = false;
        }
        // busy is cleared by the finally above, so this authoritative read is
        // allowed to reconcile the optimistic flip against the server roster.
        await this.refreshState();
    }

    // refreshState performs the authoritative read. A null result means the
    // session is gone or the viewer is no longer a participant; the component
    // flips lobbyClosed and tears down the stream so the UI stops polling a
    // dead room.
    async refreshState() {
        let state;
        try {
            state = await sessionService.getState(this.code);
        } catch {
            // A transient read failure leaves the prior roster on screen; the
            // next tick (or a reconnect) retries. Don't tear the lobby down
            // on a single blip.
            return;
        }
        if (state === null) {
            this.lobbyClosed = true;
            this.closeStream();
            this.clearQuestionTimer();
            return;
        }
        this.state = state;
        this.syncClockFrom(state);
        this.syncReadyFromState();
        this.syncQuestionFromState();
        this.syncStandingsFromState();
    }

    // syncClockFrom recomputes clockOffset from the serverNow that travels with
    // every state read, so the per-question countdown runs on the server's
    // clock rather than a skewed device clock (mirrors the solo client, #180).
    // A missing or unparseable serverNow leaves the offset untouched.
    syncClockFrom(state) {
        if (!state || !state.serverNow) return;
        const serverMs = new Date(state.serverNow).getTime();
        if (!Number.isFinite(serverMs)) return;
        this.clockOffset = serverMs - Date.now();
    }

    // serverTime returns the current time in ms as the server sees it, using
    // the offset captured on the last state read. All countdown math goes
    // through this helper.
    serverTime() {
        return Date.now() + this.clockOffset;
    }

    // syncQuestionFromState reconciles the in-game question view with the
    // server phase on each state read. When the runner moves to a new question
    // (a different question id, or the question phase re-entered) it resets the
    // per-question pick + countdown; entering the question phase (re)starts the
    // countdown off the server deadline; leaving it clears the timer. The pick
    // is reset only on a genuine question change, so an SSE tick within the
    // same question does not wipe the player's answered/waiting state.
    syncQuestionFromState() {
        const question = this.state ? this.state.question : null;
        const phase = this.state ? this.state.phase : null;

        const questionId = question ? question.id : null;
        if (questionId !== this.currentQuestionId) {
            this.currentQuestionId = questionId;
            this.pickedOptionId = null;
            this.answerError = false;
        }

        // Drive the countdown only while the question is open AND the player
        // has not yet answered. Once they pick, the bar freezes on the
        // answered/waiting state - a later tick (e.g. another player answering)
        // must not re-arm the countdown and un-freeze it.
        if (phase === 'question' && question && !this.hasAnswered()) {
            this.startQuestionCountdown(question);
            return;
        }
        // Any non-answering state (already answered, or a non-question phase:
        // reveal, round_intro, round_results, finished, lobby) freezes the bar.
        this.clearQuestionTimer();
        if (phase === 'reveal') {
            this.questionProgress = 0;
        }
    }

    // syncStandingsFromState reconciles the between-rounds / final bar graph
    // with each state read. The server carries a standings array in the
    // round_results and finished phases (null elsewhere). On a genuine new
    // round_results entry it builds the rows starting at each player's
    // pre-round total and animates the bars growing to the new total while
    // the numeric labels count up, then leaves the rows in rank order. A
    // later tick within the same phase does not re-trigger the animation, so
    // the bars don't replay on every SSE beat. The finished phase reuses the
    // same rows but skips the grow animation (roundScore is 0 there - there
    // is no single round in focus), landing straight on the final totals.
    syncStandingsFromState() {
        const phase = this.state ? this.state.phase : null;
        const standings = this.state && Array.isArray(this.state.standings) ? this.state.standings : null;
        if ((phase !== 'round_results' && phase !== 'finished') || !standings) {
            this.standingsBars = [];
            this.maxStandingsTotal = 1;
            this.lastStandingsKey = null;
            return;
        }

        // Re-key on the phase plus the question id of the round that just
        // finished so a new round (or the transition into finished) fires the
        // animation exactly once. A repeat tick with the same key is a no-op.
        const questionId = this.state.question ? this.state.question.id : 'none';
        const key = `${phase}:${questionId}`;
        if (key === this.lastStandingsKey) return;
        this.lastStandingsKey = key;

        this.maxStandingsTotal = Math.max(1, ...standings.map((s) => s.totalScore));
        const animate = phase === 'round_results';
        this.standingsBars = standings.map((s) => ({
            playerId: s.playerId,
            displayName: s.displayName,
            rank: s.rank,
            total: s.totalScore,
            preTotal: s.totalScore - s.roundScore,
            isMe: s.displayName === this.myDisplayName,
            // Start at the pre-round total when animating, otherwise land on
            // the final total straight away (finished, reduced motion).
            displayTotal: animate ? s.totalScore - s.roundScore : s.totalScore,
        }));

        if (!animate) return;
        this.animateStandingsBars();
    }

    // animateStandingsBars grows each row's displayTotal from its pre-round
    // total to its new total, which the template binds to both the bar width
    // and the numeric label so the count-up and the bar fill move together.
    // Under reduced motion or a missing anime global runAnim snaps straight to
    // the final state via the complete callback, so the bars never stick at a
    // half-grown frame. The rows are already in rank order (best-first), so
    // the leaders rest on top once the growth settles.
    animateStandingsBars() {
        const bars = this.standingsBars;
        const snap = () => {
            bars.forEach((bar) => { bar.displayTotal = bar.total; });
        };
        const a = typeof window !== 'undefined' ? window.anime : null;
        const hasRound = bars.some((bar) => bar.total !== bar.preTotal);
        if (!hasRound || !a) {
            snap();
            return;
        }
        // Animate one proxy object per bar so anime updates the reactive
        // displayTotal Alpine renders. Math.round keeps the label on whole
        // points throughout the count-up.
        bars.forEach((bar) => {
            const proxy = { v: bar.preTotal };
            runAnim(proxy, {
                v: bar.total,
                duration: STANDINGS_BAR_DURATION,
                easing: 'easeOutCubic',
                update: () => { bar.displayTotal = Math.round(proxy.v); },
                complete: () => { bar.displayTotal = bar.total; },
            });
        });
    }

    // startQuestionCountdown drives the answer-window bar 100 -> 0 over
    // [startedAt, expiresAt] on the server clock. Idempotent across ticks
    // within the same question: it clears any prior interval first, then
    // re-derives the remaining window from the absolute server deadline, so a
    // resync mid-question simply re-anchors the bar where it should be. If the
    // window is already past it pins the bar at 0 without spinning an interval.
    startQuestionCountdown(question) {
        this.clearQuestionTimer();
        if (!question || !question.startedAt || !question.expiresAt) {
            this.questionProgress = 100;
            return;
        }
        const start = new Date(question.startedAt).getTime();
        const end = new Date(question.expiresAt).getTime();
        const total = end - start;
        if (!Number.isFinite(total) || total <= 0) {
            this.questionProgress = 0;
            return;
        }
        const tick = () => {
            const remaining = end - this.serverTime();
            this.questionProgress = Math.max(0, Math.min(100, (remaining / total) * 100));
            if (this.questionProgress <= 0) {
                this.clearQuestionTimer();
            }
        };
        tick();
        if (this.questionProgress <= 0) return;
        this.questionTimer = setInterval(tick, 100);
    }

    // clearQuestionTimer cancels the countdown interval. Safe to call when no
    // timer is pending.
    clearQuestionTimer() {
        if (this.questionTimer) {
            clearInterval(this.questionTimer);
            this.questionTimer = null;
        }
    }

    // syncReadyFromState mirrors the viewer's own ready flag off the roster so
    // the toggle tracks the server truth (e.g. after a reconnect resync). The
    // viewer's row is the one whose displayName matches the landed name; the
    // wire shape exposes playerId but not "this is you", so the name is the
    // stable correlator on the player surface.
    //
    // It skips the mirror while a request is in flight (busy): in the lobby
    // that only happens during a ready-toggle, and an SSE tick landing mid-POST
    // would otherwise clobber the optimistic flip with the pre-toggle roster
    // value, flickering the button until the toggle's own refreshState lands.
    syncReadyFromState() {
        if (this.busy) return;
        if (!this.state || !Array.isArray(this.state.players)) return;
        const mine = this.state.players.find((p) => p.displayName === this.myDisplayName);
        if (mine) {
            this.isReady = mine.isReady;
        }
    }

    // subscribe opens the SSE event channel and re-reads state on every tick.
    // The stream carries no roster data - it is a pure "state moved" signal,
    // so onmessage just triggers refreshState. Idempotent: closes any prior
    // subscription first. No-op when EventSource is unavailable, in which case
    // the lobby is seeded by the initial refreshState and simply won't live
    // update (acceptable degraded mode).
    subscribe() {
        this.closeStream();
        if (typeof EventSource === 'undefined') return;
        const url = `/api/sessions/${encodeURIComponent(this.code)}/events`;
        const source = new EventSource(url);
        source.onmessage = () => {
            this.refreshState();
        };
        source.onerror = () => {
            // EventSource auto-reconnects, and a reconnect resends the current
            // version (the resync path), so a transient drop self-heals. Only
            // tear down on a hard close so we don't leak a dead socket.
            if (source.readyState === EventSource.CLOSED) {
                this.eventSource = null;
            }
        };
        this.eventSource = source;
    }

    // closeStream is safe to call regardless of subscription state.
    closeStream() {
        if (this.eventSource) {
            this.eventSource.close();
            this.eventSource = null;
        }
    }

    // isHost reports whether a roster row is the host, so the lobby can badge
    // the host-as-player. Reads hostId off the authoritative state.
    isHost(player) {
        return !!this.state && player.playerId === this.state.hostId;
    }

    // isMe reports whether a roster row is the viewer's own, for highlighting.
    isMe(player) {
        return player.displayName === this.myDisplayName;
    }

    // currentQuestion returns the live question off the authoritative state, or
    // null outside the question / reveal phases.
    currentQuestion() {
        return this.state ? this.state.question : null;
    }

    // hasAnswered reports whether the player has locked in a pick for the
    // current question, gating the "answered, waiting" state.
    hasAnswered() {
        return this.pickedOptionId !== null;
    }

    // submitAnswer records the player's single pick for the current question.
    // One answer per question: once a pick lands (or a benign 409 says the
    // window closed) the buttons lock into the answered/waiting state. The
    // server timestamps the pick on its own clock, so no client time is sent.
    // A 5xx / network failure leaves the pick unset and raises a retry banner
    // so the player can try again while the window is still open.
    async submitAnswer(optionId) {
        if (this.submitting || this.hasAnswered()) return;
        const question = this.currentQuestion();
        if (!this.state || this.state.phase !== 'question' || !question) return;
        this.submitting = true;
        this.answerError = false;
        // Lock the pick optimistically so the buttons disable the instant the
        // player taps, before the POST resolves; a hard failure rolls it back.
        this.pickedOptionId = optionId;
        // Stop the local countdown at the tap so the bar freezes on the
        // answered state rather than continuing to drain visually.
        this.clearQuestionTimer();
        try {
            const result = await sessionService.answer(this.code, optionId);
            if (!result.ok && result.kind === 'closed') {
                // The window closed between the tap and the POST. The player
                // gets no more attempts this question, so hold the answered
                // state rather than surfacing an error.
                return;
            }
        } catch {
            this.pickedOptionId = null;
            this.answerError = true;
            // Re-arm the countdown so the player keeps the time they had left;
            // the absolute server deadline makes startQuestionCountdown
            // recompute the real remaining window.
            this.startQuestionCountdown(question);
        } finally {
            this.submitting = false;
        }
    }

    // correctOptionIds returns the revealed correct-option ids, empty before
    // reveal (the no-spoiler guarantee: the server omits them until then).
    correctOptionIds() {
        const question = this.currentQuestion();
        return question && Array.isArray(question.correctOptionIds) ? question.correctOptionIds : [];
    }

    // isRevealed reports whether the session is in the reveal phase, where the
    // correct answer is shown.
    isRevealed() {
        return !!this.state && this.state.phase === 'reveal';
    }

    // pickWasCorrect reports whether the player's revealed pick was a correct
    // option. Meaningful only at reveal; false before then or if they did not
    // answer.
    pickWasCorrect() {
        if (this.pickedOptionId === null) return false;
        return this.correctOptionIds().includes(this.pickedOptionId);
    }

    // optionStateClass returns the answer-button class string. During the
    // answer window it is the per-index tone, with the player's own pick
    // marked once they have answered. At reveal the correctness skin wins: the
    // correct option(s) light up, a wrong pick is flagged, and everything else
    // dims.
    optionStateClass(option, idx) {
        if (this.isRevealed()) {
            if (this.correctOptionIds().includes(option.id)) return 'btn-answer-correct';
            if (this.pickedOptionId === option.id) return 'btn-answer-wrong';
            return 'btn-answer-dim';
        }
        const tone = QUESTION_OPTION_TONES[idx % QUESTION_OPTION_TONES.length];
        // The player's locked-in pick keeps its tone but gains a filled
        // background + accent ring so the answered/waiting state is legible
        // without leaking correctness. Uses existing theme-token utilities so
        // no new CSS class is needed.
        if (this.pickedOptionId === option.id) return `btn-answer ${tone} bg-surface-2 ring-2 ring-accent`;
        return `btn-answer ${tone}`;
    }
}
