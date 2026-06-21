// host-bigscreen.js drives the host big screen across every session phase
// (MP-3 / #680 lobby, MP-8 / #685 in-game).
//
// It follows the documented session contract: an SSE tick on
// GET /api/sessions/{code}/events is a pure "state moved" signal that carries
// no game data, so every tick (and the initial connect) triggers a fresh
// GET /api/sessions/{code}/state - the single authoritative read. Everything
// the big screen renders (roster, phase, question, answered order) comes only from
// that read, so the UI can never drift from the server's view.
//
// The countdown is driven off the server answer window (question.expiresAt
// minus the serverNow the state read carries), never the client wall clock,
// so a skewed big-screen clock cannot desync the bar from the players' devices. The
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
import { preloadImage } from '@shared/preloadImage.js';
import { createAudioEngine, initialMuted } from '@shared/audioEngine.js';
import {
    buildStandingsRows,
    animateStandingsBars,
    applyStandingsFlip,
} from '@shared/standings.js';

// STATE_FAILURE_LIMIT is how many consecutive GET /state failures the host big
// screen tolerates before surfacing the "Connection problem, retrying..." banner
// (#795). Mirrors the player lobby's threshold so both surfaces read the same.
// The big screen keeps refreshing off every SSE tick underneath; the banner just tells
// the host why the screen looks frozen. Cleared on the next good read.
const STATE_FAILURE_LIMIT = 3;

function hostBigScreen(joinCode, hasQuiz) {
    return {
        joinCode,
        phase: 'lobby',
        players: [],
        // True once a quiz is armed in the room (state.quiz present). False for
        // an empty room (#836) - the "no game running yet" staging state, opened
        // before any quiz is picked. While this is false the lobby shows the
        // pick-a-live-quiz link (#851) so the host can pick the first game; a
        // room opened with a preselected quiz ("Host live") has it true and
        // keeps the existing Start controls instead. Seeded from the
        // server-rendered HasQuiz so a preselected lobby renders its Start
        // controls without flashing the link before the first state read
        // (no-flash hydration); applyState then keeps it in sync with each read.
        hasQuiz: !!hasQuiz,
        question: null,
        // Hides the question image when its fetch fails. Reset only on a genuine
        // question change in applyState so a stale hide can't carry into the next
        // question; it persists across the same question's question -> reveal so
        // the picture stays hidden once flagged.
        imageError: false,
        // The id of the question currently on screen, so applyState can tell a
        // genuine question change (reset imageError) from a same-question tick.
        lastQuestionId: null,
        // Mute state for the question audio + game SFX (#1088), seeded from the
        // persisted preference. The engine applies it to every live Howl.
        // Default unmuted.
        audioMuted: initialMuted(),
        // True when the question's clip could not be played (failed to load, or
        // no preloaded Howl), so the template surfaces an explicit play control.
        // Reset per question; replayClip clears it on a gesture.
        audioBlocked: false,
        // Howler-backed audio engine (#1088), created in init(). Preloads +
        // decodes the SFX on init, unlocks iOS on the host Start tap, and plays
        // every question clip from already-decoded Howls. The engine reads/writes
        // flags on `this` so its reactive writes go through Alpine's proxy.
        audio: null,
        // True once clips have been preloaded for this game, so a re-armed start
        // (or a reconnect) does not re-fetch the manifest.
        clipsPreloaded: false,
        // Guards a duplicate round-start SFX: the Start gesture plays it, and the
        // first round_intro phase would play it again. Set true by the Start
        // play so the first round_intro skips it; later rounds always play it.
        roundStartPlayed: false,
        // The phase + question id the SFX cues last fired for, so a repeated SSE
        // tick within the same phase/question does not re-fire the stings (the
        // big screen re-reads /state on every tick). null until the first cue.
        lastAudioPhase: null,
        lastAudioQuestionId: null,
        // The question id the answers-show sting has fired for, so it plays once
        // when the read beat ends (options appear), not on every countdown tick.
        answersShownQuestionId: null,
        // The round_intro round off the latest state read, or null outside the
        // round_intro phase (the server carries it only there). Drives the
        // between-rounds screen's title/summary and "Round N of M" heading
        // (#748).
        round: null,
        // Offset between the server clock and Date.now() in ms, refreshed from
        // serverNow on every state read. serverTime() applies it so the
        // countdown ticks off the server's view of the deadline regardless of
        // big-screen clock skew (#180).
        clockOffset: 0,
        progress: 100,
        // True during the read beat [serverNow, startedAt): the question text
        // shows but the options stay hidden until the answer window opens. The
        // bar fills 0 -> 100 while revealing is true, then drains 100 -> 0 over
        // the answer window - mirrors the player surface and the solo game's
        // reveal beat (#247).
        revealing: false,
        connected: false,
        // Surfaces a "Connection problem, retrying..." banner once GET /state
        // has failed STATE_FAILURE_LIMIT times in a row (#795). Distinct from
        // connected, which tracks the SSE channel: a failing state read with a
        // live stream still freezes the big screen, so this covers that gap. Cleared on
        // the next good read.
        connectionTrouble: false,
        // Running count of consecutive GET /state failures, reset to 0 on any
        // success.
        stateFailures: 0,
        // True once GET /state 404s: the session is gone (terminal), distinct
        // from connectionTrouble's retryable fault, so the footer settles.
        sessionGone: false,
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
        // host big screen and every player lobby tick in lockstep.
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
        // The playerId order (best-first) of the last standings screen, kept
        // across the intervening question/reveal phases so the next screen can
        // stage its rows in this order and slide them into the new ranking (the
        // FLIP swap, #730). Null until the first standings screen.
        lastStandingsOrder: null,

        // The component root element, captured from $root in init(). The
        // standings FLIP measures its rows by querying down from here, since the
        // SSE-driven syncStandings path runs outside an Alpine expression where
        // $el does not resolve to the root. Null until init().
        rootEl: null,

        init() {
            // Capture the component root so the standings FLIP can scope its row
            // queries to this island. $root resolves here because init() runs in
            // Alpine context; the later SSE-driven syncStandings path does not,
            // which is why the lookup must be cached now rather than read off $el
            // there.
            this.rootEl = this.$root;
            // Create the Howler audio engine and preload + decode the SFX before
            // any gesture (#1088): decode runs while the AudioContext is
            // suspended, so the buffers are ready by the host Start tap.
            this.audio = createAudioEngine(this);
            this.audio.preloadEffects();
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
            // Stop the iOS keep-alive and unload every Howl so no audio or timer
            // leaks on navigation away (#1088).
            if (this.audio) this.audio.teardown();
        },

        async refresh() {
            try {
                const response = await fetch(
                    `/api/sessions/${encodeURIComponent(this.joinCode)}/state`,
                    { headers: { Accept: 'application/json' } },
                );
                if (!response.ok) {
                    // A 404 means the session is gone (terminal), not a
                    // connection fault, so it does not feed the trouble banner.
                    if (response.status === 404) {
                        this.markSessionGone();
                    } else {
                        this.noteStateFailure();
                    }
                    return;
                }
                const state = await response.json();
                // A good read clears the failure budget and the banner.
                this.stateFailures = 0;
                this.connectionTrouble = false;
                this.applyState(state);
            } catch (err) {
                // A transient fetch failure (network drop) is non-fatal: the
                // next tick (or EventSource reconnect) drives another refresh.
                // After several in a row the banner tells the host why the
                // screen looks frozen.
                this.noteStateFailure();
            }
        },

        // noteStateFailure records one consecutive GET /state failure and
        // flips the trouble banner on once the run reaches STATE_FAILURE_LIMIT.
        noteStateFailure() {
            this.stateFailures += 1;
            if (this.stateFailures >= STATE_FAILURE_LIMIT) {
                this.connectionTrouble = true;
            }
        },

        // Tears down the stream and countdowns so the screen settles on the
        // closed signal instead of ticking against a session that is gone.
        markSessionGone() {
            if (this.sessionGone) return;
            this.sessionGone = true;
            this.connectionTrouble = false;
            this.disconnect();
            this.stopCountdown();
            this.stopStartCountdown();
        },

        applyState(state) {
            this.phase = typeof state.phase === 'string' ? state.phase : 'lobby';
            this.players = Array.isArray(state.players) ? state.players : [];
            // The state read omits quiz for an empty room (#836); its presence
            // tells "quiz armed" from the empty staging lobby so the template
            // shows the Start controls rather than the pick-a-live-quiz link.
            this.hasQuiz = state.quiz != null;
            this.question = state.question ?? null;
            const questionId = this.question ? this.question.id : null;
            if (questionId !== this.lastQuestionId) {
                this.lastQuestionId = questionId;
                this.imageError = false;
                // Fetch the image during the read beat so the picture is ready
                // the moment the element mounts.
                if (this.question && this.question.imageUrl) {
                    void preloadImage(this.question.imageUrl);
                }
                // A new question means a new clip: stop a still-playing one (and
                // its pending repeats) so it does not bleed across the question
                // change, and clear any stale "Play audio" fallback from a prior
                // question's failed/blocked clip (#1088).
                if (this.audio) this.audio.stopClip();
                this.audioBlocked = false;
            }
            this.round = state.round ?? null;

            const offset = clockOffsetFromServerNow(state.serverNow);
            if (offset !== null) this.clockOffset = offset;

            // Fire the SFX + question-clip cues for this state, guarded so a
            // repeated SSE tick within the same phase/question does not re-fire
            // them (the big screen re-reads /state on every tick) (#1088).
            this.applyAudioCues();

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

        // applyAudioCues fires the SFX + question-clip cues for the current
        // phase/question, once per transition (#1088). The big screen re-reads
        // /state on every SSE tick, so each cue is guarded against re-firing
        // within the same phase + question id:
        //   - round_intro: round-start sting (deduped against the Start gesture's
        //     round-start, which already played for the first round).
        //   - question (new question): question-show sting + the question's quiz
        //     clip (preloaded at start, so it plays immediately).
        //   - reveal: answer-correct sting. There is no per-player pick on the
        //     big screen, so it never plays answer-wrong.
        // The answers-show sting fires from the read-beat -> answer-window
        // transition in startCountdown's setRevealing hook, not here.
        applyAudioCues() {
            if (!this.audio) return;
            const qid = this.question ? this.question.id : null;
            if (this.phase === this.lastAudioPhase && qid === this.lastAudioQuestionId) {
                return;
            }
            const prevPhase = this.lastAudioPhase;
            this.lastAudioPhase = this.phase;
            this.lastAudioQuestionId = qid;

            if (this.phase === 'round_intro') {
                if (this.roundStartPlayed) {
                    this.roundStartPlayed = false;
                } else {
                    this.audio.playEffect('round-start');
                }

                return;
            }

            if (this.phase === 'question' && this.question) {
                this.audio.playEffect('question-show');
                if (this.question.audioUrl) this.audio.playClip(this.question.id);

                return;
            }

            if (this.phase === 'reveal' && prevPhase !== 'reveal') {
                this.audio.playEffect('answer-correct');
            }
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

        // startCountdownLabel is the "Starting in M:SS" text the host big screen
        // shows while the countdown is armed.
        startCountdownLabel() {
            return `Starting in ${formatCountdown(this.startRemaining)}`;
        },

        // showsPickQuizLink reports whether the room should offer the link to
        // the filtered quiz list where the host picks a live quiz (#851): an
        // empty lobby (no quiz armed) for the first game, and the between-games
        // intermission for the next game. The host follows the link, picks a
        // live quiz, and "Host live" arms it back in this same room (one room
        // per host). A lobby with a preselected quiz keeps the Start controls
        // instead, so the link is hidden there.
        showsPickQuizLink() {
            return (this.phase === 'lobby' && !this.hasQuiz)
                || this.phase === 'intermission';
        },

        // showsEndSession reports whether the host's "End session" control is
        // available: across the live phases (lobby, intermission, in-game) so
        // the host can cleanly close the room, but not once it is already
        // finished (the terminal phase).
        showsEndSession() {
            return this.phase !== 'finished';
        },

        // showsJoinHint reports whether the compact join code + URL strip shows
        // on the big screen (#852): every live phase except the lobby (which
        // already shows the full QR + code card) and the terminal finished phase
        // (the room is closed, no joins). A latecomer can join mid-game (the
        // server accepts joins in every phase but finished, #836); the strip
        // keeps the code visible so they can.
        showsJoinHint() {
            return this.phase !== 'lobby' && this.phase !== 'finished';
        },

        // showsStandings reports whether the current phase renders the
        // standings bar graph: the between-rounds round_results screen and the
        // end-of-game screens - intermission (the between-games screen, #836)
        // and the terminal finished phase.
        showsStandings() {
            return this.phase === 'round_results'
                || this.phase === 'intermission'
                || this.phase === 'finished';
        },

        // syncStandings reconciles the between-rounds / final bar graph with
        // each state read. The server carries a standings array in the
        // round_results phase and on the end-of-game screen - intermission (the
        // between-games screen, #836) and the terminal finished phase (null
        // elsewhere). On a genuine new entry it builds the rows starting at each
        // player's pre-round total and grows the bars to the new total while the
        // numeric labels count up; from the second screen on the rows also slide
        // from their previous-screen position into the new ranking (a FLIP swap,
        // #730) so an overtake reads as rows trading places. A later tick within
        // the same phase is a no-op, so it doesn't replay on every SSE beat. The
        // end-of-game screen animates the last round's contribution: its
        // standings carry the last round's roundScore so the bars grow into the
        // final totals.
        syncStandings(state) {
            const standings = Array.isArray(state.standings) ? state.standings : null;
            if (!this.showsStandings() || !standings) {
                this.standingsBars = [];
                this.maxStandingsTotal = 1;
                this.lastStandingsKey = null;
                // lastStandingsOrder is kept across question/reveal so the next
                // standings screen can slide its rows from the prior order.

                return;
            }

            const questionId = this.question ? this.question.id : 'none';
            const key = `${this.phase}:${questionId}`;
            if (key === this.lastStandingsKey) {
                return;
            }
            this.lastStandingsKey = key;

            const animate = this.showsStandings();
            const { rows, maxTotal } = buildStandingsRows(standings, { animate });
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
        },

        // standingsContainer returns the rendered standings <ul>, or null before
        // the graph is shown. Scoped to this.rootEl (captured from $root in
        // init()), not document: the host's phase blocks use x-show, so the <ul>
        // stays mounted and the root-scoped query lands it without assuming the
        // page holds exactly one standings surface.
        standingsContainer() {
            return this.rootEl ? this.rootEl.querySelector('[data-standings-bars]') : null;
        },

        // serverTime returns the current time in ms as the server sees it,
        // using the offset captured on the last state read, so every countdown
        // runs on the server's view of the deadline regardless of big-screen
        // clock skew.
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
                setRevealing: (revealing) => {
                    this.revealing = revealing;
                    // Answers-shown sting (#1088): the read beat just ended and
                    // the options appear, in the question phase. Fires once per
                    // question id so a re-anchored countdown does not replay it.
                    if (!revealing && this.phase === 'question' && this.question
                        && this.answersShownQuestionId !== this.question.id) {
                        this.answersShownQuestionId = this.question.id;
                        if (this.audio) this.audio.playEffect('answers-show');
                    }
                },
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
        // reveal so the big screen cannot leak correctness early), so the answered
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
        // omits correctness before reveal), so the big screen cannot leak the
        // answer early.
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

        // roundEyebrow is the small heading above the round title on the
        // round_intro screen. It reads "Round N of M" so the first round never
        // says "next round"; it falls back to a generic "Get ready" when the
        // server did not carry the round position (a deleted round mid-game).
        roundEyebrow() {
            if (this.round && this.round.number > 0 && this.round.total > 0) {
                return `Round ${this.round.number} of ${this.round.total}`;
            }

            return 'Get ready';
        },

        // roundTitle is the round_intro heading: the round's own title, or a
        // generic "Next round" when no round metadata is present. The fallback
        // differs from roundEyebrow's "Get ready" so the two lines never stack
        // the same words when round metadata is missing.
        roundTitle() {
            return this.round && this.round.title ? this.round.title : 'Next round';
        },

        // roundSummary is the optional copy beneath the round title, empty when
        // the round has no summary so the template skips it.
        roundSummary() {
            return this.round && this.round.summary ? this.round.summary : '';
        },

        async start() {
            // FIRST, synchronously, inside the host Start gesture and BEFORE any
            // await (#1088): resume the AudioContext + start the iOS keep-alive,
            // then play the round-start sting. That gesture-bound play unlocks
            // iOS output so every later clip autoplays. roundStartPlayed dedupes
            // the first round_intro phase, which would otherwise re-play it.
            if (this.audio) {
                this.audio.unlock();
                this.audio.playEffect('round-start');
                this.roundStartPlayed = true;
            }
            // Preload every question clip up front (#1088), in parallel with the
            // start POST, so each question plays an already-decoded Howl.
            void this.preloadGameAudio();
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

        // preloadGameAudio fetches the session audio manifest and preloads every
        // clip through the engine (#1088). Best-effort and idempotent: an
        // audio-free quiz, a fetch failure, or a second call after the clips are
        // already loaded just proceeds with the clips it has.
        async preloadGameAudio() {
            if (!this.audio || this.clipsPreloaded) return;
            this.clipsPreloaded = true;
            try {
                const response = await fetch(
                    `/api/sessions/${encodeURIComponent(this.joinCode)}/audio`,
                    { headers: { Accept: 'application/json' } },
                );
                if (!response.ok) return;
                const manifest = await response.json();
                const clips = manifest && Array.isArray(manifest.clips) ? manifest.clips : [];
                await this.audio.preloadClips(clips);
            } catch (err) {
                // Best-effort: a failed manifest fetch just means no preloaded
                // clips; playClip then surfaces the manual fallback per question.
                console.warn('preloadGameAudio failed', err);
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

        // replayAudio restarts the current question's clip from the play/replay
        // control. The engine clears the blocked fallback (the click is a user
        // gesture) and bypasses the once-per-question guard.
        replayAudio() {
            if (this.audio && this.question) this.audio.replayClip(this.question.id);
        },

        // toggleMute flips and persists the mute preference through the engine,
        // which applies it to every live Howl (SFX + clips) so a mid-clip toggle
        // takes effect at once.
        toggleMute() {
            if (this.audio) this.audio.toggleMute();
        },

        csrfToken() {
            const input = this.$el.querySelector('input[name="csrf_token"]');

            return input ? input.value : '';
        },
    };
}

document.addEventListener('alpine:init', () => {
    window.Alpine.data('hostBigScreen', hostBigScreen);
});
