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
// so a skewed TV clock cannot desync the bar from the players' devices.
//
// The between-rounds standings bar graph (MP-9 / #685, #686) animates with
// anime.js (vendored UMD, window.anime). runAnim wraps it with a
// reduced-motion / missing-global fallback that snaps to the final state via
// the complete callback, so the bars never stick at a half-grown frame -
// mirrors the solo client's helper (GameApp.js).
//
// Alpine is the vendored UMD build (window.Alpine, auto-started). This module
// loads before it in the layout and only registers the component on the
// alpine:init event, so there is no import - Alpine boots itself.

// STANDINGS_BAR_DURATION is how long each bar spends growing from its
// pre-round total to its new total (ms).
const STANDINGS_BAR_DURATION = 900;

function reducedMotion() {
    return typeof window !== 'undefined'
        && typeof window.matchMedia === 'function'
        && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

function runAnim(targets, params) {
    if (reducedMotion()) {
        if (typeof params.complete === 'function') params.complete();
        return;
    }
    const a = typeof window !== 'undefined' ? window.anime : null;
    if (!a) {
        if (typeof params.complete === 'function') params.complete();
        return;
    }
    if (typeof a.animate === 'function') {
        a.animate(targets, params);
    } else if (typeof a === 'function') {
        a({ targets, ...params });
    } else if (typeof params.complete === 'function') {
        params.complete();
    }
}

function hostLobby(joinCode) {
    return {
        joinCode,
        phase: 'lobby',
        players: [],
        question: null,
        // serverClockSkew is (client wall clock - server clock) at the last
        // state read, in milliseconds. The countdown subtracts it so it ticks
        // off the server's view of the deadline regardless of TV clock skew.
        serverClockSkew: 0,
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

            const serverNow = state.serverNow ? Date.parse(state.serverNow) : NaN;
            this.serverClockSkew = Number.isNaN(serverNow) ? 0 : Date.now() - serverNow;

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

            this.syncStandings(state);
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

            this.maxStandingsTotal = Math.max(1, ...standings.map((s) => s.totalScore));
            const animate = this.phase === 'round_results';
            this.standingsBars = standings.map((s) => ({
                playerId: s.playerId,
                displayName: s.displayName,
                rank: s.rank,
                total: s.totalScore,
                preTotal: s.totalScore - s.roundScore,
                displayTotal: animate ? s.totalScore - s.roundScore : s.totalScore,
            }));

            if (animate) {
                this.animateStandings();
            }
        },

        // animateStandings grows each row's displayTotal from its pre-round
        // total to its new total; the template binds it to both the bar width
        // and the numeric label so the count-up and fill move together. Under
        // reduced motion or a missing anime global runAnim snaps to the final
        // state via complete, so no bar sticks half-grown. Rows are already in
        // rank order, so the leaders rest on top once the growth settles.
        animateStandings() {
            const bars = this.standingsBars;
            const a = typeof window !== 'undefined' ? window.anime : null;
            const hasRound = bars.some((bar) => bar.total !== bar.preTotal);
            if (!hasRound || !a) {
                bars.forEach((bar) => { bar.displayTotal = bar.total; });

                return;
            }
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
        },

        // serverTime estimates the server's "now" from the TV wall clock minus
        // the measured skew, so every countdown runs on the server's view of
        // the deadline regardless of TV clock skew.
        serverTime() {
            return Date.now() - this.serverClockSkew;
        },

        // startCountdown drives the per-question bar. Before the window opens
        // (serverNow < startedAt) it runs the read beat, filling 0 -> 100 while
        // the options stay hidden; at startedAt it flips to the answer-window
        // countdown, draining 100 -> 0. Mirrors the solo game's reveal beat
        // (#247) and the player surface.
        startCountdown() {
            this.stopCountdown();
            const start = this.question.startedAt ? Date.parse(this.question.startedAt) : NaN;
            const end = this.question.expiresAt ? Date.parse(this.question.expiresAt) : NaN;
            if (Number.isNaN(start) || Number.isNaN(end) || end <= start) {
                this.revealing = false;
                this.progress = 100;

                return;
            }
            if (this.serverTime() < start) {
                this.startReadBeat(start, end);

                return;
            }
            this.startAnswerCountdown(start, end);
        },

        startReadBeat(start, end) {
            const beatStart = this.serverTime();
            const beatTotal = start - beatStart;
            this.revealing = true;
            this.progress = 0;
            const update = () => {
                const serverNow = this.serverTime();
                if (serverNow >= start) {
                    this.progress = 100;
                    this.stopCountdown();
                    this.revealing = false;
                    this.startAnswerCountdown(start, end);

                    return;
                }
                this.progress = Math.max(0, Math.min(100, ((serverNow - beatStart) / beatTotal) * 100));
            };
            update();
            this.timer = setInterval(update, 100);
        },

        startAnswerCountdown(start, end) {
            this.stopCountdown();
            this.revealing = false;
            const total = end - start;
            const update = () => {
                const serverNow = this.serverTime();
                const remaining = end - serverNow;
                this.progress = Math.max(0, Math.min(100, (remaining / total) * 100));
                if (this.progress <= 0) {
                    this.stopCountdown();
                }
            };
            update();
            this.timer = setInterval(update, 100);
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

        csrfToken() {
            const input = this.$el.querySelector('input[name="csrf_token"]');

            return input ? input.value : '';
        },
    };
}

document.addEventListener('alpine:init', () => {
    window.Alpine.data('hostLobby', hostLobby);
});
