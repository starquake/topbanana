import { sessionService } from '../services/SessionService.js';
import { playerService } from '../services/PlayerService.js';
import { runAnim } from '@shared/anim.js';
import { clockOffsetFromServerNow, serverTime } from '@shared/serverClock.js';
import { startQuestionCountdown } from '@shared/countdown.js';
import { startStartCountdown, formatCountdown } from '@shared/startCountdown.js';
import {
    buildStandingsRows,
    animateStandingsBars,
    applyStandingsFlip,
} from '@shared/standings.js';
import { optionStateClass } from '../util/answerOptions.js';
import {
    SESSION_STORAGE_KEY,
    readRememberedSession,
    forgetRememberedSession,
} from '@shared/rememberedSession.js';
import { t } from '../util/i18n.js';

// JOIN_PATH_PATTERN matches /join/<code>, capturing the room code. The bare
// /join entry (enter-code form) has no capture group, so the component falls
// back to the typed-code step there.
const JOIN_PATH_PATTERN = /^\/join\/([^/]+)\/?$/;

// STATE_FAILURE_LIMIT is how many consecutive non-404 GET /state failures the
// player lobby tolerates before surfacing the "Connection problem, retrying..."
// banner (#795). The component keeps polling underneath; the banner just tells
// the player why the roster looks frozen. A single blip stays silent; three in
// a row (each one an SSE tick or a foreground re-read apart) reads as a real
// outage. A 404 is not counted here - it is the room-gone signal that flips
// sessionClosed instead.
const STATE_FAILURE_LIMIT = 3;

// rememberSession persists the join code so a reload can resume. Best-effort: a
// storage exception (private mode, quota) is swallowed - resume is a
// convenience, not a correctness requirement. The read/forget side and the key
// string live in the shared rememberedSession module so the home page reads the
// same entry (#1005); only the write side stays here, since home never writes.
function rememberSession(code) {
    try {
        window.localStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify({ code }));
    } catch {
        // localStorage may be unavailable; resume simply won't fire next load.
    }
}

// JoinApp is the Alpine component behind the player join + lobby + in-game
// surface (MP-4 / #681, MP-7 / #684). It is deliberately separate from gameApp
// (the solo client) and from the host big screen: it owns only the player's
// own join form, lobby view, and synchronized question play.
//
// `step` is the local join-flow stage:
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
        this.step = 'code';
        // The room code the player is joining. Upper-cased for display; the
        // server is case-insensitive, but normalizing keeps the UI tidy and
        // the deep-link target consistent.
        this.code = '';
        // Bound to the enter-code input on the 'code' step.
        this.codeInput = '';
        // Bound to the display-name input on the 'name' step.
        this.displayName = '';
        // The name the player joined with, shown in the lobby header. Comes
        // from the join/state response (the player's current
        // players.display_name), and updates on each state read so a rename
        // reflects in the header too.
        this.myDisplayName = '';
        // The viewer's own players.id, resolved once from /api/players/me.
        // Used to spot their own roster row across reads, which a rename would
        // otherwise break if the row were matched by name.
        this.myPlayerId = null;
        // The current player as returned by GET /api/players/me, or null until
        // init() resolves (or when the read fails / the player is anonymous).
        // Backs the shared header's "Signed in as {displayName}" account
        // control, mirroring the solo client (#520). Held reactively so the
        // header renders the name as soon as init() lands.
        this.player = null;
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
        this.sessionClosed = false;
        // The SSE subscription handle, closed on teardown and before re-open.
        this.eventSource = null;
        // The bound visibility/focus handler, wired once in init. Held on the
        // instance so a single shared reference backs all three listeners. Null
        // until init attaches it.
        this.onVisible = null;

        // The account display name to auto-join with, set once init resolves a
        // logged-in player who has already chosen a custom name. Null for an
        // anonymous visitor, or a logged-in player still on an auto-petname -
        // both keep the name-entry form. When set, a known code skips the name
        // step and joins straight away under this name.
        this.accountName = null;

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
        // True during the read beat [serverNow, startedAt): the question text
        // shows but the options are hidden until the answer window opens. The
        // bar fills 0 -> 100 while revealing is true, then drains 100 -> 0 over
        // the answer window - one bar, two phases, mirroring the solo game's
        // reveal beat (#247).
        this.revealing = false;
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
        // The playerId order (best-first) of the last standings screen, kept
        // across the intervening question/reveal phases (when the graph is
        // cleared) so the next screen can stage its rows in this order and slide
        // them into the new ranking - the FLIP swap (#730). Null until the first
        // standings screen, where there is nothing to slide from.
        this.lastStandingsOrder = null;

        // --- Host-armed last-call countdown (#735) ---------------------------
        // The absolute armed start deadline (ISO string) off the latest state
        // read while in the lobby, or null when no countdown is armed.
        this.startAt = null;
        // Whole seconds left until startAt, driven off the server clock so the
        // player lobby and the host big screen tick in lockstep.
        this.startRemaining = 0;
        // Interval handle for the start countdown, cleared before each new one
        // and on teardown.
        this.startTimer = null;

        // --- Connection-health banner (#795) --------------------------------
        // Surfaces a "Connection problem, retrying..." banner once GET /state
        // has failed STATE_FAILURE_LIMIT times in a row with a non-404 error
        // (a network drop or a 5xx), so the player knows why a stale roster
        // isn't updating. Cleared on the next successful read. A 404 is the
        // distinct room-gone signal and flips sessionClosed instead.
        this.connectionTrouble = false;
        // Running count of consecutive non-404 GET /state failures, reset to 0
        // on any success.
        this.stateFailures = 0;
        // Monotonic id per GET /state read; ignore a superseded response so an
        // out-of-order read can't regress the surface (e.g. reveal->question) (#1178).
        this.stateSeq = 0;
        // Guards the connection-trouble banner's "Reconnect now" control (#1121)
        // so a double-tap does not fire two overlapping recoveries, and drives
        // the button's in-flight "Reconnecting..." label.
        this.reconnecting = false;

        // --- Exit-session confirm (#888) ------------------------------------
        // exitConfirmOpen drives the destructive-confirm modal the lobby's
        // Exit-session link opens. Modeled on the host's #853 confirm-and-
        // restart pattern: a one-shot Alpine flag, closed on cancel /
        // backdrop / explicit confirm. exiting guards the confirm button
        // while the explicit leave POST is in flight.
        this.exitConfirmOpen = false;
        this.exiting = false;

        // --- Leave beacon (#794) --------------------------------------------
        // Guards against a duplicate leave: beforeunload and pagehide can both
        // fire on the same teardown, and the beacon should go out once. Reset
        // when the player lands back in a lobby (a bfcache restore re-Joins),
        // so a later genuine exit still sends.
        this.leftSent = false;

        // --- Screen Wake Lock (QoL 2 / #760) --------------------------------
        // Held WakeLockSentinel that keeps the player's screen awake during a
        // live game, or null when none is held. Acquired off the user gesture
        // that lands them in the lobby and re-acquired on return to the
        // foreground (the OS auto-releases it when the page hides). Released on
        // the finished phase, when the lobby is gone, and on teardown.
        this.wakeLock = null;
        // True while a wake lock is held or a request is in flight, guarding
        // against a double-acquire. A plain boolean rather than a truthiness
        // check on this.wakeLock because Alpine wraps reactive object fields in
        // a Proxy, so an identity check (sentinel === this.wakeLock) inside the
        // release handler would never match - the boolean sidesteps that.
        this.wakeLockHeld = false;
        // Monotonic id bumped on each acquire so a sentinel's release handler
        // only clears the held flag when it is still the current one (a stale
        // late release for a superseded sentinel is ignored).
        this.wakeLockGen = 0;

        // The component root element, captured from $root in init(). The
        // standings FLIP measures its rows by querying down from here, since the
        // SSE-driven syncStandingsFromState path runs outside an Alpine
        // expression where $el does not resolve to the root. Null until init().
        this.rootEl = null;
    }

    // init resolves the room code (from a /join/{code} deep link or a
    // remembered session) and attempts to resume before showing any form. A
    // /join/{code} deep link otherwise lands on the name form; the bare /join
    // entry with no remembered session shows the enter-code form first.
    async init() {
        // Capture the component root so the standings FLIP can scope its row
        // queries to this island. $root resolves here because init() runs in
        // Alpine context; the later SSE-driven syncStandingsFromState path does
        // not, which is why the lookup must be cached now rather than read off
        // $el there.
        this.rootEl = this.$root;

        // Fire the leave beacon on every event that can signal the player is
        // going away. beforeunload alone is unreliable on mobile - a tab the OS
        // discards in the background, or a swipe-away on iOS Safari, often never
        // raises it (#794). pagehide fires on bfcache navigation and tab close
        // where beforeunload may not, and visibilitychange(hidden) covers the
        // app-switch / lock-screen case. sendBeacon is idempotent server-side
        // and the client guards against a duplicate send within one teardown,
        // so wiring all three is safe.
        //
        // The leave also tears down the stream and timers so a backgrounded tab
        // does not leak a server-side subscriber or a stale countdown interval.
        // beforeunload/pagehide also fire on a reload, so a reloading player may
        // be marked left before the reloaded page comes back. That race is
        // harmless: resume re-Joins, which revives the left roster row (the
        // server's resume gate accepts a prior participant regardless of
        // left_at). The remembered session below is what makes the reload land
        // back in the lobby.
        window.addEventListener('beforeunload', () => this.sendLeave());
        window.addEventListener('pagehide', () => this.sendLeave());

        // Mobile browsers commonly suspend or close the SSE connection while
        // the tab is backgrounded, and EventSource does not always reconnect on
        // return, so no further state reads fire and the roster goes stale. On
        // every return to the foreground re-read state and re-open the stream
        // if it dropped, so the roster and current phase come back (#751). One
        // shared handler covers visibilitychange, pageshow (bfcache restore),
        // and focus; it no-ops outside the lobby stage. pageshow forwards its
        // event so a bfcache restore (persisted=true) can re-Join: pagehide
        // fired the leave beacon on the way out, so the player's roster row was
        // marked left and a plain state read would 404 - the restore must
        // revive it.
        this.onVisible = (event) => this.handleVisible(event);
        document.addEventListener('visibilitychange', this.onVisible);
        window.addEventListener('pageshow', this.onVisible);
        window.addEventListener('focus', this.onVisible);

        // Resolve the current player once. A logged-in player who has already
        // chosen a custom name is auto-joined under that name, skipping the
        // name form (the solo client treats the same isAuthenticated +
        // hasCustomName pair as "named", #165). The id is kept regardless so
        // the viewer's own roster row can be matched by id (rename-safe).
        // Best-effort: a failed fetch or an anonymous / unnamed player leaves
        // accountName null, so the claim-then-join flow runs and joining is
        // never blocked on this read.
        const player = await playerService.getMe();
        if (player) {
            this.player = player;
            this.myPlayerId = player.id;
            if (player.isAuthenticated && player.hasCustomName) {
                this.accountName = player.displayName;
            }
        }

        const match = JOIN_PATH_PATTERN.exec(window.location.pathname);
        const urlCode = match ? decodeURIComponent(match[1]).toUpperCase() : '';
        const remembered = readRememberedSession();
        // Resume only from a REMEMBERED session (a room the player previously
        // joined), never from a bare deep link on its own: a fresh /join/{code}
        // visit by an unnamed player must still show the name form rather than
        // silently auto-joining under their auto-petname. A deep link that
        // matches the remembered room (or a remembered room with no deep link)
        // resumes straight into the lobby.
        const resumeCode = remembered && (!urlCode || remembered.code === urlCode)
            ? remembered.code
            : '';

        if (resumeCode) {
            const resumed = await this.tryResume(resumeCode);
            if (resumed) return;
        }

        // No resume: seed the form flow. With a URL code, a named player is
        // auto-joined under their account name (skipping the name form), while
        // anyone else lands on the name form. With no code, the enter-code
        // form shows first.
        if (urlCode) {
            this.code = urlCode;
            if (this.accountName) {
                await this.autoJoin();
                return;
            }
            this.step = 'name';
        }
    }

    // landInLobby captures the join result, switches to the lobby stage,
    // remembers the code for resume, and opens the SSE subscription. Shared by
    // every successful-join path (auto-join, resume, claim-then-join). Joining
    // and resuming both run off a user gesture (the join/ready tap, or the
    // reload that re-runs init), so this is the point to take the screen wake
    // lock that keeps the player's phone awake through a live game (#760).
    async landInLobby(result) {
        this.myDisplayName = result.displayName;
        this.isReady = result.isReady;
        this.step = 'lobby';
        this.leftSent = false;
        rememberSession(this.code);
        this.acquireWakeLock();
        await this.refreshState();
        this.subscribe();
    }

    // autoJoin joins the current code for a logged-in named player, landing
    // them straight in the lobby and skipping the name form. The join is
    // nameless (#716): the player keeps their account name. On a non-OK result
    // it falls back to a recoverable surface: a notFound bounces to the code
    // form, a closed (the game already started) routes to the terminal closed
    // view (#793), and anything else lands on the name form.
    async autoJoin() {
        this.busy = true;
        this.error = '';
        try {
            const result = await sessionService.join(this.code);
            if (!result.ok) {
                if (result.kind === 'closed') {
                    this.enterClosedState();
                    return;
                }
                this.error = result.message;
                this.step = result.kind === 'notFound' ? 'code' : 'name';
                if (result.kind === 'notFound') this.codeInput = this.code;
                return;
            }
            await this.landInLobby(result);
        } finally {
            this.busy = false;
        }
    }

    // tryResume attempts to rejoin code, landing the player straight in the
    // lobby on success. The join is nameless (#716): the player is already
    // named on their players row. A re-Join revives a prior participant's
    // roster row (even one marked left by the unload beacon), so a reload
    // resumes without re-entering the code; the server-derived countdown
    // re-anchors on the next refreshState. On a closed lobby (409 for a
    // never-joined player) or an unknown room (404) it clears the remembered
    // entry and falls back to the normal flow. Returns whether resume landed.
    async tryResume(code) {
        let result;
        try {
            result = await sessionService.join(code);
        } catch {
            return false;
        }
        if (!result.ok) {
            forgetRememberedSession();
            return false;
        }
        this.code = code;
        await this.landInLobby(result);
        return true;
    }

    // submitCode advances from the enter-code form. A named player (logged in
    // with a custom name) is auto-joined under their account name, skipping the
    // name form; anyone else moves to the name form. For the form path it does
    // not hit the network - the code is validated by the join attempt itself,
    // so a bad code surfaces as the same "no game found" message either way.
    submitCode() {
        const trimmed = (this.codeInput || '').trim().toUpperCase();
        if (trimmed === '') {
            this.error = t('join.enterCode');
            return;
        }
        this.error = '';
        this.code = trimmed;
        if (this.accountName) {
            void this.autoJoin();
            return;
        }
        this.step = 'name';
    }

    // submitName claims the chosen name on the player's players row through the
    // shared claim flow (#716), then posts the nameless join. The claim is what
    // names an anonymous / unnamed player before they join; the join itself
    // carries no name. On a claim collision it surfaces the error and keeps the
    // name form so the player can pick another. A join notFound bounces back to
    // the code form so the player can fix a typo; a join closed (the game has
    // already started, so the lobby is gone) routes to the terminal closed view
    // rather than stranding an error under the dead name form (#793).
    async submitName() {
        if (this.busy) return;
        const trimmed = (this.displayName || '').trim();
        if (trimmed === '') {
            this.error = t('claim.enterName');
            return;
        }
        this.busy = true;
        this.error = '';
        try {
            const claim = await this.claimName(trimmed);
            if (!claim.ok) return;

            const result = await sessionService.join(this.code);
            if (!result.ok) {
                if (result.kind === 'closed') {
                    this.enterClosedState();
                    return;
                }
                this.error = result.message;
                if (result.kind === 'notFound') {
                    // Send them back to fix the code rather than retyping a
                    // name against a room that doesn't exist.
                    this.step = 'code';
                    this.codeInput = this.code;
                }
                return;
            }
            await this.landInLobby(result);
        } finally {
            this.busy = false;
        }
    }

    // enterClosedState routes the player to the terminal "this game is no
    // longer available" view (#793). It is the same sessionClosed surface a
    // mid-game session-gone read lands on: the lobby stage with the closed
    // banner and nothing else. No stream is opened and the remembered session
    // is cleared so a reload does not bounce back into a dead room.
    enterClosedState() {
        this.step = 'lobby';
        this.sessionClosed = true;
        this.error = '';
        forgetRememberedSession();
    }

    // claimName sets the player's players.display_name through the shared
    // claim endpoint (PlayerService, the same call the solo client's claim
    // modal uses). On an already_claimed drift (the player turned out to be
    // named already) it re-reads /me and treats that name as claimed so the
    // join proceeds. Returns { ok } and sets this.error on a recoverable
    // failure (a taken name, an empty name). On success it caches the name as
    // the account name so a later re-entry skips the form.
    async claimName(name) {
        const result = await playerService.claimName(name);
        if (result.ok) {
            this.accountName = result.player.displayName;
            this.myPlayerId = result.player.id;
            return { ok: true };
        }
        if (result.kind === 'already_claimed') {
            const player = await playerService.getMe();
            if (player) {
                this.accountName = player.displayName;
                this.myPlayerId = player.id;
            }
            return { ok: true };
        }
        this.error = result.message;
        return { ok: false };
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
            this.error = t('join.readyError');
            return;
        } finally {
            this.busy = false;
        }
        // busy is cleared by the finally above, so this authoritative read is
        // allowed to reconcile the optimistic flip against the server roster.
        await this.refreshState();
    }

    // refreshState performs the authoritative read. A null result (404) means
    // the session is gone or the viewer is no longer a participant; the
    // component flips sessionClosed and tears down the stream so the UI stops
    // polling a dead room. A thrown read (network drop, 5xx) leaves the prior
    // roster on screen and, after STATE_FAILURE_LIMIT in a row, surfaces the
    // connection-trouble banner (#795) while the next tick keeps retrying.
    async refreshState() {
        const seq = ++this.stateSeq;
        let state;
        try {
            state = await sessionService.getState(this.code);
        } catch {
            // Count every failure, even a superseded one: seq-gating this would
            // drop a fast-superseded run and never trip the banner (#1178). A
            // transient read failure leaves the prior roster on screen; the next
            // tick (or a reconnect) retries. Don't tear the lobby down on a
            // single blip, but after several in a row tell the player why the
            // roster looks frozen.
            this.stateFailures += 1;
            if (this.stateFailures >= STATE_FAILURE_LIMIT) {
                this.connectionTrouble = true;
            }
            return;
        }
        if (seq !== this.stateSeq) return; // stale snapshot resolved late (#1178)
        if (state === null) {
            this.sessionClosed = true;
            this.releaseWakeLock();
            this.closeStream();
            this.clearQuestionTimer();
            this.clearStartTimer();
            // The room is gone or we are no longer a participant, so a future
            // load must not try to resume into it.
            forgetRememberedSession();
            return;
        }
        // A good read clears the failure budget and the trouble banner.
        this.stateFailures = 0;
        this.connectionTrouble = false;
        this.state = state;
        this.syncClockFrom(state);
        this.syncReadyFromState();
        this.syncQuestionFromState();
        this.syncStartCountdownFromState();
        this.syncStandingsFromState();
        this.syncWakeLockFromState(state);
    }

    // syncWakeLockFromState reconciles the screen wake lock with the game
    // phase. An end-of-game screen (intermission, the between-games screen #836,
    // and the terminal finished phase) has no answer window keeping the screen
    // busy, so the lock is dropped and the phone can sleep again - the standings
    // are the last thing the player reads on that screen. When the host re-arms
    // (#836) and the room walks back into active play, the lock is re-acquired
    // so the next game keeps the screen awake. The acquire runs off the same
    // wakeLockHeld guard, so a repeat tick within a phase does not re-request.
    syncWakeLockFromState(state) {
        if (state.phase === 'intermission' || state.phase === 'finished') {
            this.releaseWakeLock();
            return;
        }
        this.acquireWakeLock();
    }

    // syncStartCountdownFromState reconciles the host-armed last-call countdown
    // with each state read. The server carries startAt only while a countdown
    // is armed in the lobby; once it fires (or is cancelled) the field is gone,
    // so the live "Starting in M:SS" line gives way to the static waiting hint
    // and the timer stops.
    syncStartCountdownFromState() {
        const phase = this.state ? this.state.phase : null;
        this.startAt = phase === 'lobby' ? (this.state.startAt ?? null) : null;
        if (!this.startAt) {
            this.clearStartTimer();
            this.startRemaining = 0;
            return;
        }
        startStartCountdown(this.startAt, {
            serverNow: () => this.serverTime(),
            setRemaining: (sec) => { this.startRemaining = sec; },
            setTimer: (handle) => { this.startTimer = handle; },
            clearTimer: () => this.clearStartTimer(),
        });
    }

    // clearStartTimer cancels the start-countdown interval. Safe to call when
    // no timer is pending.
    clearStartTimer() {
        if (this.startTimer) {
            clearInterval(this.startTimer);
            this.startTimer = null;
        }
    }

    // startArmed reports whether a last-call countdown is running, so the lobby
    // swaps the static waiting hint for the live countdown.
    startArmed() {
        return !!this.startAt;
    }

    // startCountdownLabel is the "Starting in M:SS" text the player lobby shows
    // while the countdown is armed.
    startCountdownLabel() {
        return t('join.startingIn', { time: formatCountdown(this.startRemaining) });
    }

    // lobbyTitle is the lobby heading: the room's quiz title once a quiz is
    // armed, or a generic "Get ready" for an empty room (#836). The state read
    // omits quiz for a room opened with no game picked yet, so reading
    // state.quiz.title directly would throw and break the lobby render - this
    // guards that case so the player sees a sane waiting lobby.
    lobbyTitle() {
        return this.state && this.state.quiz ? this.state.quiz.title : t('join.getReady');
    }

    // lobbyHasQuiz reports whether the room has a quiz armed, so the lobby can
    // word its waiting hint for the empty-room staging state (no quiz yet)
    // distinctly from a room that has a game queued (#836).
    lobbyHasQuiz() {
        return !!(this.state && this.state.quiz);
    }

    // syncClockFrom recomputes clockOffset from the serverNow that travels with
    // every state read, so the per-question countdown runs on the server's
    // clock rather than a skewed device clock (mirrors the solo client, #180).
    // A missing or unparseable serverNow leaves the offset untouched.
    syncClockFrom(state) {
        const offset = clockOffsetFromServerNow(state && state.serverNow);
        if (offset !== null) this.clockOffset = offset;
    }

    // serverTime returns the current time in ms as the server sees it, using
    // the offset captured on the last state read. All countdown math goes
    // through this helper.
    serverTime() {
        return serverTime(this.clockOffset);
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
        // must not re-arm the countdown and un-freeze it. During the read beat
        // (serverNow < startedAt) the options stay hidden and the bar fills
        // 0 -> 100; at startedAt the options open and the bar drains over the
        // answer window.
        if (phase === 'question' && question && !this.hasAnswered()) {
            this.startCountdown(question);
            return;
        }
        // Any non-answering state (already answered, or a non-question phase:
        // reveal, round_intro, round_results, finished, lobby) freezes the bar
        // and ends any read beat.
        this.clearQuestionTimer();
        this.revealing = false;
        if (phase === 'reveal') {
            this.questionProgress = 0;
        }
    }

    // showsStandings reports whether the current server phase renders the
    // standings bar graph: the between-rounds round_results screen and the
    // end-of-game screens - intermission (the between-games screen, #836) and
    // the terminal finished phase.
    showsStandings() {
        const phase = this.state ? this.state.phase : null;
        return phase === 'round_results' || phase === 'intermission' || phase === 'finished';
    }

    // syncStandingsFromState reconciles the between-rounds / final bar graph
    // with each state read. The server carries a standings array in the
    // round_results phase and on the end-of-game screen - intermission (the
    // between-games screen, #836) and the terminal finished phase (null
    // elsewhere). On a genuine new entry it builds the rows starting at each
    // player's pre-round total and grows the bars to the new total while the
    // numeric labels count up; from the second screen on the rows also slide
    // from their previous-screen position into the new ranking (a FLIP swap,
    // #730) so an overtake reads as rows trading places. A later tick within the
    // same phase does not re-trigger the animation, so it doesn't replay on
    // every SSE beat. The end-of-game screen animates the last round's
    // contribution: its standings carry the last round's roundScore so the bars
    // grow into the final totals.
    syncStandingsFromState() {
        const phase = this.state ? this.state.phase : null;
        const standings = this.state && Array.isArray(this.state.standings) ? this.state.standings : null;
        if (!this.showsStandings() || !standings) {
            this.standingsBars = [];
            this.maxStandingsTotal = 1;
            this.lastStandingsKey = null;
            // lastStandingsOrder is kept across the question/reveal phases so the
            // next standings screen can slide its rows from the prior order.
            return;
        }

        // Re-key on the phase plus the question id of the round that just
        // finished so a new round (or the transition into the end-of-game
        // screen) fires the animation exactly once. A repeat tick with the same
        // key is a no-op.
        const questionId = this.state.question ? this.state.question.id : 'none';
        const key = `${phase}:${questionId}`;
        if (key === this.lastStandingsKey) return;
        this.lastStandingsKey = key;

        const animate = this.showsStandings();
        const { rows, maxTotal } = buildStandingsRows(standings, {
            animate,
            ownsRow: (row) => this.ownsRow(row),
        });
        this.maxStandingsTotal = maxTotal;
        const prevOrder = this.lastStandingsOrder;
        this.lastStandingsOrder = rows.map((row) => String(row.playerId));

        applyStandingsFlip({
            rows,
            prevOrder,
            animate,
            runAnim,
            setBars: (next) => { this.standingsBars = next; },
            getBars: () => this.standingsBars,
            getContainer: () => this.standingsContainer(),
            afterRender: (cb) => this.$nextTick(cb),
            animateBars: animateStandingsBars,
        });
    }

    // standingsContainer returns the rendered standings <ul>, or null before the
    // graph is shown. Scoped to this.rootEl (captured from $root in init()), not
    // document: the single standings <ul> stays mounted across the standings
    // phases via x-show, so the root-scoped query lands it without assuming the
    // page holds exactly one standings surface.
    standingsContainer() {
        return this.rootEl ? this.rootEl.querySelector('[data-testid="standings-bars"]') : null;
    }

    // startCountdown drives the per-question bar through the shared helper:
    // a read beat filling 0 -> 100 while options stay hidden, then an
    // answer-window drain 100 -> 0 over [startedAt, expiresAt]. Both phases run
    // on the server clock. Idempotent across ticks within the same question.
    startCountdown(question) {
        startQuestionCountdown(question, {
            serverNow: () => this.serverTime(),
            setProgress: (pct) => { this.questionProgress = pct; },
            setRevealing: (revealing) => { this.revealing = revealing; },
            setTimer: (handle) => { this.questionTimer = handle; },
            clearTimer: () => this.clearQuestionTimer(),
        });
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
    // the toggle tracks the server truth (e.g. after a reconnect resync), and
    // refreshes the displayed name from their roster row so a rename shows in
    // the header. The viewer's row is resolved by ownsRow (playerId when known,
    // rename-safe).
    //
    // It skips the mirror while a request is in flight (busy): in the lobby
    // that only happens during a ready-toggle, and an SSE tick landing mid-POST
    // would otherwise clobber the optimistic flip with the pre-toggle roster
    // value, flickering the button until the toggle's own refreshState lands.
    syncReadyFromState() {
        if (this.busy) return;
        if (!this.state || !Array.isArray(this.state.players)) return;
        const mine = this.state.players.find((p) => this.ownsRow(p));
        if (mine) {
            this.isReady = mine.isReady;
            this.myDisplayName = mine.displayName;
        }
    }

    // handleVisible recovers the live surface when the player returns to a
    // backgrounded tab. It only acts in the lobby stage on a live (non-closed)
    // session, and only when the page is actually visible (visibilitychange
    // also fires on the way to hidden). It re-reads state immediately so the
    // roster and phase repopulate, re-subscribes only when the stream has
    // dropped (subscribe is a no-op-ish reopen otherwise, but guarding here
    // avoids needlessly tearing down a still-live socket), and re-acquires the
    // screen wake lock the OS auto-released while the tab was hidden (#760).
    //
    // A bfcache restore (pageshow with persisted=true) is special: pagehide
    // already fired the leave beacon, so the player's roster row is marked
    // left and a plain state read would 404 into the closed view. Re-Join
    // instead, which revives the row, then let landInLobby reseed the stream
    // and wake lock.
    handleVisible(event) {
        if (document.visibilityState !== 'visible') return;
        if (this.step !== 'lobby' || !this.code) return;
        if (event && event.type === 'pageshow' && event.persisted) {
            this.resumeAfterRestore();
            return;
        }
        if (this.sessionClosed) return;
        this.refreshState();
        if (this.streamDropped()) {
            this.subscribe();
        }
        this.acquireWakeLock();
    }

    // resumeAfterRestore re-Joins after a bfcache restore. pagehide already
    // fired the leave beacon on the way out, marking the player's roster row
    // left, so a plain state read would 404 into the closed view; re-Join
    // revives the row instead. If the re-Join does not land (a genuinely ended
    // session, or a transient network blip) it falls back to a normal state
    // read, which distinguishes the two: a 404 flips the terminal closed view,
    // while a transient failure leaves the roster and retries on the next tick.
    async resumeAfterRestore() {
        this.sessionClosed = false;
        const resumed = await this.tryResume(this.code);
        if (resumed) return;
        await this.refreshState();
        if (this.streamDropped()) this.subscribe();
        this.acquireWakeLock();
    }

    // streamDropped reports whether the SSE subscription is gone or closed, so
    // a return-to-foreground only re-opens a dead socket rather than leaking a
    // duplicate over a still-connected one.
    streamDropped() {
        return !this.eventSource || this.eventSource.readyState === EventSource.CLOSED;
    }

    // reconnectNow forces an immediate recovery from the connection-trouble
    // state instead of waiting for the next automatic retry (an SSE tick or a
    // return to the foreground), wired to the banner's "Reconnect now" control
    // (#1121). It reuses the same path the foreground-return recovery uses:
    // re-open the SSE stream and re-read authoritative state right away. A good
    // read clears the trouble banner; a 404 flips the closed view. subscribe()
    // runs first so refreshState()'s 404 path can tear down the stream it just
    // opened. The reconnecting guard absorbs a double-tap and labels the button.
    async reconnectNow() {
        if (this.reconnecting) return;
        if (this.step !== 'lobby' || !this.code) return;
        this.reconnecting = true;
        try {
            this.subscribe();
            await this.refreshState();
        } finally {
            this.reconnecting = false;
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

    // sendLeave tears the live surface down and fires the best-effort leave
    // beacon so the player's row drops out of the roster, answered-order
    // badges, and standings at once (MP-10 / #687). Wired to beforeunload and
    // pagehide because beforeunload alone is unreliable on mobile - the OS can
    // discard a backgrounded tab without raising it, and pagehide fires on
    // bfcache navigation where beforeunload may not (#794). The leftSent guard
    // keeps the two from double-firing on one teardown; the server leave is
    // idempotent anyway, but the guard avoids a redundant beacon. It is
    // deliberately NOT wired to visibilitychange(hidden): that fires on every
    // app-switch the player means to return from, and the leave would strand
    // them out of the lobby on their way back.
    sendLeave() {
        this.closeStream();
        this.clearQuestionTimer();
        this.clearStartTimer();
        this.releaseWakeLock();
        if (this.step === 'lobby' && this.code && !this.leftSent) {
            this.leftSent = true;
            sessionService.leave(this.code);
        }
    }

    // exitSession is the explicit-exit handler the #888 modal triggers. It
    // mirrors sendLeave but runs from a deliberate user gesture rather than a
    // page-unload event, so it uses fetch (with keepalive so a fast follow-up
    // navigation still flushes) instead of sendBeacon, awaits the result, and
    // then routes the player to the bare /join entry-code screen. The remembered
    // session is cleared so the next visit shows the enter-code form rather than
    // auto-resuming back into the dead row. leftSent is flipped so the unload
    // beacon does not also fire while the page navigates away.
    async exitSession() {
        if (this.exiting) return;
        this.exiting = true;
        this.closeStream();
        this.clearQuestionTimer();
        this.clearStartTimer();
        this.releaseWakeLock();
        forgetRememberedSession();
        const code = this.code;
        this.leftSent = true;
        try {
            await fetch(`/api/sessions/${encodeURIComponent(code)}/leave`, {
                method: 'POST',
                keepalive: true,
            });
        } catch {
            // Server-side leave is best-effort: a transient failure still
            // exits the surface, and the row ages out of the active window
            // server-side, so there is nothing to retry here.
        }
        this.exitConfirmOpen = false;
        window.location.assign('/join');
    }

    // acquireWakeLock requests a screen wake lock so the player's phone does
    // not dim or sleep through a live game (#760). Progressive enhancement:
    // feature-detected, secure-context only (the API is unavailable on plain
    // HTTP), and any rejection (denied, unsupported, low battery) is swallowed
    // so a missing wake lock never blocks play. The wakeLockHeld guard keeps it
    // to at most one in-flight/held lock; the OS releases the lock when the
    // page hides, and the release handler clears the guard so handleVisible can
    // re-acquire on return. The generation id ensures a stale release event for
    // a superseded sentinel does not clear a newer one's guard.
    acquireWakeLock() {
        if (typeof navigator === 'undefined' || !('wakeLock' in navigator)) return;
        if (this.wakeLockHeld) return;
        this.wakeLockHeld = true;
        const gen = ++this.wakeLockGen;
        navigator.wakeLock.request('screen').then((sentinel) => {
            // A release (deliberate or game-over) may have raced in before the
            // request resolved; if the generation moved on, drop this sentinel.
            if (gen !== this.wakeLockGen) {
                sentinel.release().catch(() => {});
                return;
            }
            this.wakeLock = sentinel;
            sentinel.addEventListener('release', () => {
                if (gen === this.wakeLockGen) {
                    this.wakeLock = null;
                    this.wakeLockHeld = false;
                }
            });
        }).catch(() => {
            // Denied / unsupported / battery saver: play continues without it.
            if (gen === this.wakeLockGen) this.wakeLockHeld = false;
        });
    }

    // releaseWakeLock drops a held screen wake lock and clears the guard.
    // Safe to call when none is held. Called when the game finishes, when the
    // lobby is gone, and on teardown. Bumping the generation first invalidates
    // any in-flight request so a lock that resolves after this is released at
    // once. release() returns a promise; a rejection is swallowed since there
    // is nothing to recover.
    releaseWakeLock() {
        this.wakeLockGen += 1;
        this.wakeLockHeld = false;
        const sentinel = this.wakeLock;
        this.wakeLock = null;
        if (sentinel) {
            sentinel.release().catch(() => {
                // Already released by the OS; nothing to do.
            });
        }
    }

    // isHost reports whether a roster row is the host, so the lobby can badge
    // the host-as-player. Reads hostId off the authoritative state.
    isHost(player) {
        return !!this.state && player.playerId === this.state.hostId;
    }

    // isMe reports whether a roster row is the viewer's own, for highlighting.
    isMe(player) {
        return this.ownsRow(player);
    }

    // ownsRow reports whether a roster/standings row belongs to the viewer.
    // It matches by playerId (rename-safe) when the id is known, falling back
    // to the landed name only when myPlayerId could not be resolved (a failed
    // /api/players/me on load) so the highlight and ready mirror still work in
    // that degraded case.
    ownsRow(row) {
        if (this.myPlayerId !== null) {
            return row.playerId === this.myPlayerId;
        }

        return row.displayName === this.myDisplayName;
    }

    // isAuthenticated reports whether the player is known to the system
    // through some credential (password, OAuth identity, or the seeded admin
    // role). Backs the shared header's account control, which shows only for a
    // signed-in player (#520) - the same gate the solo client uses.
    isAuthenticated() {
        return !!(this.player && this.player.isAuthenticated);
    }

    // inActiveQuestion reports whether a live question is on screen, so the
    // shared header (brand + account control) can hide and give the question
    // the full canvas - mirroring the solo client's `gameId && !finished` gate
    // (#253). It covers the question phase and the reveal phase (the same
    // question text, with the correct answer marked); every other screen -
    // enter-code, name, lobby, round_intro, round_results, intermission, and
    // finished - keeps the header.
    inActiveQuestion() {
        const phase = this.state ? this.state.phase : null;
        return this.step === 'lobby' && (phase === 'question' || phase === 'reveal');
    }

    // currentQuestion returns the live question off the authoritative state, or
    // null outside the question / reveal phases.
    currentQuestion() {
        return this.state ? this.state.question : null;
    }

    // currentRound returns the round_intro round off the authoritative state,
    // or null outside the round_intro phase (the server carries it only there).
    currentRound() {
        return this.state ? this.state.round : null;
    }

    // roundEyebrow is the small heading above the round title on the round_intro
    // screen. It reads "Round N of M" so the first round never says "next
    // round"; it falls back to a generic "Get ready" when the server did not
    // carry the round position (a deleted round mid-game).
    roundEyebrow() {
        const round = this.currentRound();
        if (round && round.number > 0 && round.total > 0) {
            return t('join.roundNof', { number: round.number, total: round.total });
        }
        return t('join.getReady');
    }

    // roundTitle is the round_intro heading: the round's own title, or a
    // generic "Next round" when no round metadata is present. The fallback
    // differs from roundEyebrow's "Get ready" so the two lines never stack the
    // same words when round metadata is missing.
    roundTitle() {
        const round = this.currentRound();
        return round && round.title ? round.title : t('join.nextRound');
    }

    // roundSummary is the optional copy beneath the round title, empty when the
    // round has no summary so the template skips it.
    roundSummary() {
        const round = this.currentRound();
        return round && round.summary ? round.summary : '';
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
            // the absolute server deadline makes the shared helper recompute
            // the real remaining window.
            this.startCountdown(question);
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
        return optionStateClass(option, idx, {
            revealed: this.isRevealed(),
            correctIds: this.correctOptionIds(),
            pickedId: this.pickedOptionId,
            highlightPick: true,
        });
    }
}
