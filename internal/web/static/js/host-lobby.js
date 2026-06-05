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
// Alpine is the vendored UMD build (window.Alpine, auto-started). This module
// loads before it in the layout and only registers the component on the
// alpine:init event, so there is no import - Alpine boots itself.

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
        connected: false,
        starting: false,
        startMessage: '',
        source: null,
        timer: null,

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
                this.progress = this.phase === 'reveal' ? 0 : 100;
            }
        },

        startCountdown() {
            this.stopCountdown();
            const start = this.question.startedAt ? Date.parse(this.question.startedAt) : NaN;
            const end = this.question.expiresAt ? Date.parse(this.question.expiresAt) : NaN;
            if (Number.isNaN(start) || Number.isNaN(end) || end <= start) {
                this.progress = 100;

                return;
            }
            const total = end - start;
            const update = () => {
                // serverNow estimate = client clock minus the measured skew.
                const serverNow = Date.now() - this.serverClockSkew;
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
