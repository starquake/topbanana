// host-lobby.js drives the host TV surface across every session phase
// (MP-3 / #680 lobby, MP-8 / #685 in-game).
//
// It follows the documented session contract: an SSE tick on
// GET /api/sessions/{code}/events is a pure "state moved" signal that carries
// no game data, so every tick (and the initial connect) triggers a fresh
// GET /api/sessions/{code}/state - the single authoritative read. Everything
// the TV renders (roster, phase, question, answered order) comes only from
// that read, so the UI can never drift from the server's view.
//
// The countdown is driven off the server answer window (question.expiresAt
// minus the serverNow the state read carries), never the client wall clock,
// so a skewed TV clock cannot desync the bar from the players' devices. The
// per-question countdown and the between-rounds standings bar graph are the
// shared player/host helpers (frontend/shared), bundled in by esbuild so the
// host and player surfaces stay in lockstep without a cross-tree runtime fetch.
//
// Alpine is the vendored UMD build (window.Alpine, auto-started). This module
// loads before it in the layout and only registers the component on the
// alpine:init event.

import { runAnim } from '@shared/anim.js';
import { clockOffsetFromServerNow, serverTime } from '@shared/serverClock.js';
import { startQuestionCountdown } from '@shared/countdown.js';
import { startStartCountdown, formatCountdown } from '@shared/startCountdown.js';
import { buildStandingsRows, animateStandingsBars } from '@shared/standings.js';

function hostLobby(joinCode) {
    return {
        joinCode,
        phase: 'lobby',
        players: [],
        question: null,
        // Offset between the server clock and Date.now() in ms, refreshed from
        // serverNow on every state read. serverTime() applies it so the
        // countdown ticks off the server's view of the deadline regardless of
        // TV clock skew (#180).
        clockOffset: 0,
        progress: 100,
        // True during the read beat [serverNow, startedAt): the question text
        // shows but the options stay hidden until the answer window opens. The
        // bar fills 0 -> 100 while revealing is true, then drains 100 -> 0 over
        // the answer window - mirrors the player surface and the solo game's
        // reveal beat (#247).
        revealing: false,
        connected: false,
        starting: false,
        startMessage: '',
        source: null,
        timer: null,

        // --- Host-armed last-call countdown (#735) --------------------------
        // The absolute armed deadline (ISO string) off the latest state read,
        // or null when no countdown is armed. armed() reads it to decide
        // whether to show the "Starting in M:SS" line and the Cancel control.
        startAt: null,
        // Whole seconds left until startAt, driven off the server clock so the
        // host TV and every player lobby tick in lockstep.
        startRemaining: 0,
        // Interval handle for the start countdown, cleared before each new one
        // and on teardown.
        startTimer: null,
        // True while an arm / cancel request is in flight, to guard the
        // controls.
        arming: false,

        // --- Between-rounds standings bar graph (MP-9 / #686) ---------------
        // Rendered rows for the round_results / finished standings, in rank
        // order (the server returns standings best-first). Each row is
        // { playerId, displayName, rank, total, preTotal, displayTotal },
        // where displayTotal is the animated value the bar width + numeric
        // label bind to.
        standingsBars: [],
        // The largest total across the current standings; each bar's width is
        // its share of the leader's bar (leader = full). At least 1 so a
        // zero-score round never divides by zero.
        maxStandingsTotal: 1,
        // Identifies the standings the bar graph is animating so the grow +
        // resort fires once per round_results entry, not on every SSE tick
        // within the phase (mirrors the player surface).
        lastStandingsKey: null,

        init() {
            // Pull the authoritative state once up front so the surface is
            // correct even before the first tick arrives, then subscribe.
            this.refresh();
            this.connect();
            // EventSource keeps retrying on its own, but close it cleanly so
            // a navigation away does not leak the connection or the timer.
            window.addEventListener('beforeunload', () => this.teardown());
        },

        connect() {
            const source = new EventSource(
                `/api/sessions/${encodeURIComponent(this.joinCode)}/events`,
            );
            this.source = source;
            source.onopen = () => {
                this.connected = true;
            };
            // Every tick means "re-read state". The payload (version, phase)
            // is intentionally ignored here; the state read is the source of
            // truth.
            source.onmessage = () => {
                this.connected = true;
                this.refresh();
            };
            source.onerror = () => {
                // The browser reconnects automatically; reflect the gap in
                // the UI until onopen/onmessage fires again.
                this.connected = false;
            };
        },

        disconnect() {
            if (this.source) {
                this.source.close();
                this.source = null;
            }
        },

        teardown() {
            this.disconnect();
            this.stopCountdown();
            this.stopStartCountdown();
        },

        async refresh() {
            try {
                const response = await fetch(
                    `/api/sessions/${encodeURIComponent(this.joinCode)}/state`,
                    { headers: { Accept: 'application/json' } },
                );
                if (!response.ok) {
                    return;
                }
                const state = await response.json();
                this.applyState(state);
            } catch (err) {
                // A transient fetch failure is non-fatal: the next tick (or
                // EventSource reconnect) drives another refresh.
            }
        },

        applyState(state) {
            this.phase = typeof state.phase === 'string' ? state.phase : 'lobby';
            this.players = Array.isArray(state.players) ? state.players : [];
            this.question = state.question ?? null;

            const offset = clockOffsetFromServerNow(state.serverNow);
            if (offset !== null) this.clockOffset = offset;

            // The countdown only runs in the question phase; every other phase
            // (including reveal, where the answer window has closed) leaves it
            // idle so the bar does not keep draining.
            if (this.phase === 'question' && this.question) {
                this.startCountdown();
            } else {
                this.stopCountdown();
                this.revealing = false;
                this.progress = this.phase === 'reveal' ? 0 : 100;
            }

            this.syncStartCountdown(state);
            this.syncStandings(state);
        },

        // syncStartCountdown reconciles the host-armed last-call countdown with
        // each state read. The server carries startAt only while a countdown is
        // armed in the lobby; once it fires (or is cancelled) the field is gone,
        // so the line and Cancel control disappear and the timer stops.
        syncStartCountdown(state) {
            this.startAt = this.phase === 'lobby' ? (state.startAt ?? null) : null;
            if (!this.startAt) {
                this.stopStartCountdown();
                this.startRemaining = 0;

                return;
            }
            startStartCountdown(this.startAt, {
                serverNow: () => this.serverTime(),
                setRemaining: (sec) => { this.startRemaining = sec; },
                setTimer: (handle) => { this.startTimer = handle; },
                clearTimer: () => this.stopStartCountdown(),
            });
        },

        stopStartCountdown() {
            if (this.startTimer) {
                clearInterval(this.startTimer);
                this.startTimer = null;
            }
        },

        // armed reports whether a last-call countdown is currently running, so
        // the template swaps the "Start in 60s" control for the live countdown
        // plus a Cancel control.
        armed() {
            return !!this.startAt;
        },

        // startCountdownLabel is the "Starting in M:SS" text the host TV shows
        // while the countdown is armed.
        startCountdownLabel() {
            return `Starting in ${formatCountdown(this.startRemaining)}`;
        },

        // syncStandings reconciles the between-rounds / final bar graph with
        // each state read. The server carries a standings array in the
        // round_results and finished phases (null elsewhere). On a genuine new
        // round_results entry it builds the rows starting at each player's
        // pre-round total and animates the bars growing to the new total while
        // the numeric labels count up, then rests them in rank order. A later
        // tick within the same phase is a no-op, so the bars don't replay on
        // every SSE beat. The finished phase reuses the same rows but skips the
        // grow animation (roundScore is 0 there - no single round in focus).
        syncStandings(state) {
            const standings = Array.isArray(state.standings) ? state.standings : null;
            if ((this.phase !== 'round_results' && this.phase !== 'finished') || !standings) {
                this.standingsBars = [];
                this.maxStandingsTotal = 1;
                this.lastStandingsKey = null;

                return;
            }

            const questionId = this.question ? this.question.id : 'none';
            const key = `${this.phase}:${questionId}`;
            if (key === this.lastStandingsKey) {
                return;
            }
            this.lastStandingsKey = key;

            const animate = this.phase === 'round_results';
            const { rows, maxTotal } = buildStandingsRows(standings, { animate });
            this.standingsBars = rows;
            this.maxStandingsTotal = maxTotal;

            if (animate) {
                animateStandingsBars(this.standingsBars, runAnim);
            }
        },

        // serverTime returns the current time in ms as the server sees it,
        // using the offset captured on the last state read, so every countdown
        // runs on the server's view of the deadline regardless of TV clock
        // skew.
        serverTime() {
            return serverTime(this.clockOffset);
        },

        // startCountdown drives the per-question bar through the shared helper:
        // a read beat filling 0 -> 100 while options stay hidden, then an
        // answer-window drain 100 -> 0. Both phases run on the server clock.
        startCountdown() {
            startQuestionCountdown(this.question, {
                serverNow: () => this.serverTime(),
                setProgress: (pct) => { this.progress = pct; },
                setRevealing: (revealing) => { this.revealing = revealing; },
                setTimer: (handle) => { this.timer = handle; },
                clearTimer: () => this.stopCountdown(),
            });
        },

        stopCountdown() {
            if (this.timer) {
                clearInterval(this.timer);
                this.timer = null;
            }
        },

        // answeredCount is how many players have locked in a pick for the live
        // question - the all-answered indicator and the badge count read it.
        answeredCount() {
            if (!this.question || !Array.isArray(this.question.answeredPlayerIds)) {
                return 0;
            }

            return this.question.answeredPlayerIds.length;
        },

        allAnswered() {
            return this.players.length > 0 && this.answeredCount() >= this.players.length;
        },

        // displayNameFor maps a roster playerId to its display name so an
        // answered badge can show who locked in (the badges render in answer
        // order from answeredPlayerIds, never in correctness order).
        displayNameFor(playerId) {
            const match = this.players.find((p) => p.playerId === playerId);

            return match ? match.displayName : 'Player';
        },

        // answerCorrectness reports whether a player's just-revealed pick was
        // correct: true / false at reveal, null otherwise. The server stamps
        // answers[].correct only in the reveal phase (it omits the flag before
        // reveal so the TV cannot leak correctness early), so the answered
        // badges turn green/red only once the answer is out.
        answerCorrectness(playerId) {
            if (this.phase !== 'reveal' || !this.question || !Array.isArray(this.question.answers)) {
                return null;
            }
            const match = this.question.answers.find((a) => a.playerId === playerId);
            if (!match || typeof match.correct !== 'boolean') {
                return null;
            }

            return match.correct;
        },

        // isCorrectOption reports whether the server marked the option correct.
        // The correctOptionIds list is empty until the reveal phase (the server
        // omits correctness before reveal), so the TV cannot leak the answer
        // early.
        isCorrectOption(optionId) {
            if (!this.question || !Array.isArray(this.question.correctOptionIds)) {
                return false;
            }

            return this.question.correctOptionIds.includes(optionId);
        },

        playerCountLabel() {
            const ready = this.players.filter((p) => p.isReady).length;

            return `${ready} / ${this.players.length} ready`;
        },

        async start() {
            this.starting = true;
            this.startMessage = '';
            try {
                const response = await fetch(
                    `/host/${encodeURIComponent(this.joinCode)}/start`,
                    {
                        method: 'POST',
                        headers: {
                            'Content-Type': 'application/x-www-form-urlencoded',
                        },
                        body: new URLSearchParams({
                            csrf_token: this.csrfToken(),
                        }),
                    },
                );
                // The POST 303-redirects back to the lobby on success, which
                // fetch follows transparently to a 200. The runner advances
                // the page into play off the SSE tick, so there is nothing to
                // do here on success beyond clearing the disabled state.
                if (!response.ok) {
                    this.startMessage = 'Could not start the game. Try again.';
                }
            } catch (err) {
                this.startMessage = 'Could not start the game. Try again.';
            } finally {
                this.starting = false;
            }
        },

        // armStart arms the last-call countdown via the host-gated JSON API.
        // The server stamps the absolute deadline; the SSE tick -> refresh
        // surfaces it (startAt) and starts the local countdown, so there is
        // nothing to set here beyond clearing the disabled state.
        async armStart() {
            this.arming = true;
            this.startMessage = '';
            try {
                const response = await fetch(
                    `/api/sessions/${encodeURIComponent(this.joinCode)}/arm-start`,
                    { method: 'POST', headers: { Accept: 'application/json' } },
                );
                if (!response.ok) {
                    this.startMessage = 'Could not arm the countdown. Try again.';
                }
            } catch (err) {
                this.startMessage = 'Could not arm the countdown. Try again.';
            } finally {
                this.arming = false;
            }
        },

        // cancelStart cancels an armed countdown via the host-gated JSON API.
        // The SSE tick -> refresh clears startAt and stops the local countdown.
        async cancelStart() {
            this.arming = true;
            this.startMessage = '';
            try {
                const response = await fetch(
                    `/api/sessions/${encodeURIComponent(this.joinCode)}/cancel-start`,
                    { method: 'POST', headers: { Accept: 'application/json' } },
                );
                if (!response.ok) {
                    this.startMessage = 'Could not cancel the countdown. Try again.';
                }
            } catch (err) {
                this.startMessage = 'Could not cancel the countdown. Try again.';
            } finally {
                this.arming = false;
            }
        },

        csrfToken() {
            const input = this.$el.querySelector('input[name="csrf_token"]');

            return input ? input.value : '';
        },
    };
}

document.addEventListener('alpine:init', () => {
    window.Alpine.data('hostLobby', hostLobby);
});
