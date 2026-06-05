import { sessionService } from '../services/SessionService.js';

// JOIN_PATH_PATTERN matches /join/<code>, capturing the room code. The bare
// /join entry (enter-code form) has no capture group, so the component falls
// back to the typed-code phase there.
const JOIN_PATH_PATTERN = /^\/join\/([^/]+)\/?$/;

// JoinApp is the Alpine component behind the player join + lobby surface
// (MP-4 / #681). It is deliberately separate from gameApp (the solo client)
// and from the host/TV surface: it owns only the player's own join form and
// lobby view.
//
// Three phases, gated by `phase`:
//   - 'code'  : no room code yet - the PC enter-code form.
//   - 'name'  : code known, not joined - the display-name form.
//   - 'lobby' : joined - the roster + ready toggle, driven by SSE.
//
// The lobby follows the frozen contract: the SSE channel carries only
// {version, phase} ticks, so every tick (and the initial frame on subscribe)
// triggers a fresh GET /state. The component never reads roster data off the
// stream.
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
        // subscriber when the player navigates away or closes the tab.
        window.addEventListener('beforeunload', () => this.closeStream());
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
            return;
        }
        this.state = state;
        this.syncReadyFromState();
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
}
