// host-lobby.js drives the TV lobby (MP-3 / #680).
//
// It follows the documented session contract: an SSE tick on
// GET /api/sessions/{code}/events is a pure "state moved" signal that carries
// no game data, so every tick (and the initial connect) triggers a fresh
// GET /api/sessions/{code}/state - the single authoritative read. The roster
// and ready states the template renders come only from that read, so the UI
// can never drift from the server's view.
//
// Alpine is the vendored UMD build (window.Alpine, auto-started). This module
// loads before it in the layout and only registers the component on the
// alpine:init event, so there is no import - Alpine boots itself.

function hostLobby(joinCode) {
    return {
        joinCode,
        players: [],
        connected: false,
        starting: false,
        startMessage: '',
        source: null,

        init() {
            // Pull the authoritative state once up front so the roster is
            // correct even before the first tick arrives, then subscribe.
            this.refresh();
            this.connect();
            // EventSource keeps retrying on its own, but close it cleanly so
            // a navigation away does not leak the connection.
            window.addEventListener('beforeunload', () => this.disconnect());
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
                this.players = Array.isArray(state.players) ? state.players : [];
            } catch (err) {
                // A transient fetch failure is non-fatal: the next tick (or
                // EventSource reconnect) drives another refresh.
            }
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
