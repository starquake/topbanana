import { quizService } from '../services/QuizService.js';
import { gameService } from '../services/GameService.js';
import { playerService } from '../services/PlayerService.js';
import { openShareDialog } from '/assets/js/share.js';

// PLAY_PATH_PATTERN matches /play/<anything>-<integer>; the integer suffix
// is the quiz ID.
const PLAY_PATH_PATTERN = /^\/play\/.+-(\d+)\/?$/;

// reducedMotion returns true when the OS-level preference is set; all
// JS-driven animation calls below short-circuit in that case so the page
// behaves identically to a no-animation build for affected users.
function reducedMotion() {
    return typeof window !== 'undefined'
        && typeof window.matchMedia === 'function'
        && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

// runAnim wraps anime.js with safe fallbacks so missing globals or
// unsupported reduced-motion preferences don't break the page. The
// targets argument can be a CSS selector string or a DOM element.
function runAnim(targets, params) {
    if (reducedMotion()) return;
    const a = typeof window !== 'undefined' ? window.anime : null;
    if (!a) return;
    if (typeof a.animate === 'function') {
        a.animate(targets, params);
    } else if (typeof a === 'function') {
        a({ targets, ...params });
    }
}

// staggerDelay returns a value usable as anime.js's `delay`. Prefers the
// real anime.stagger when available, falls back to an index-based
// computation so the staggered effect still happens on older builds.
function staggerDelay(step) {
    const a = typeof window !== 'undefined' ? window.anime : null;
    if (a && typeof a.stagger === 'function') {
        return a.stagger(step);
    }
    return (_el, i) => i * step;
}

export class GameApp {
    constructor() {
        this.quizzes = [];
        this.selectedQuizId = null;
        this.gameId = null;
        this.question = null;
        this.finished = false;
        this.leaderboard = null;
        this.quizSlugId = null;
        this.feedback = null;
        // Surfaces the "couldn't submit your answer" retry banner when
        // a submitAnswer POST throws (server 5xx, network drop). Cleared
        // on the next click or when a fresh question loads — see #179.
        this.submitError = false;
        this.progress = 100;
        this.timer = null;
        // Reset per-question; the <img> element is reused across questions
        // (Alpine doesn't recreate it when x-if stays truthy), so a stale
        // display:none from a prior broken image would otherwise hide the
        // next image too.
        this.imageError = false;
        this.startError = null;
        // Set when the page is loaded via /play/<slug>-<id>; the dropdown
        // is hidden in that case so the player just sees a description and
        // a Start button. Stays null on /client/, where the dropdown shows.
        this.deepLinkedQuiz = null;
        // Current player as returned by GET /api/players/me. Stays null
        // until init() resolves; templates guard with `player &&`. When
        // the player renames, the PATCH response replaces this object
        // so player.username and player.hasCustomName flow through every
        // bound template at once.
        this.player = null;
        // Visibility of the shared claim-name modal. A single piece of
        // state drives the modal across all three entry points
        // (pre-leaderboard, inline leaderboard row, start screen).
        // The modal sits on top of whatever view is currently rendered,
        // so the leaderboard is never gated by this flag — the modal
        // simply overlays it.
        this.claimModalOpen = false;
        // True while a submitAnswer POST is in flight. Without this,
        // the countdown could fire between the click and the response
        // and overwrite the real feedback with a timeout banner — see
        // the race notes on handleTimeout for #175.
        this.submittingAnswer = false;
        // Running total of points the player has accumulated this
        // game. Each successful submitAnswer adds its score; the
        // value drives the "Score: N" chip in the gameplay header
        // (#253). Reset when a new game starts via startGame.
        this.score = 0;
        // Drives the full-screen verdict splash (#253). `splash` is the
        // variant ('correct' / 'wrong' / 'timeout') and drives both the
        // skin class and the verdict text; `splashOn` is the visibility
        // flag x-show watches. We flip splashOn off (not splash) to
        // start the leave transition, so the variant stays set during
        // the fade-out and the text doesn't flicker through to the
        // fall-through ternary branch.
        this.splash = null;
        this.splashOn = false;
        // True while the per-question reveal beat is still running —
        // the answer buttons stay hidden during this phase (#247).
        // The progress bar handles both phases visually: it fills
        // 0 → 100 in cyan while `revealing` is true, then once it
        // reaches 100 the buttons appear and the same bar drains
        // 100 → 0 in accent over the answer window. Single visual
        // element across the whole question lifetime.
        this.revealing = false;
        this.revealTimer = null;
        // Offset between the server clock and Date.now() in ms,
        // refreshed from `serverNow` in every question payload (#180).
        // serverTime() applies it so the per-question countdown runs
        // against the server's view of "now" instead of the device's,
        // which can be minutes off on phones with stale time.
        this.clockOffset = 0;
        // Server-Sent Events handle for the leaderboard live stream
        // (#239). Opened when the leaderboard becomes visible; closed on
        // navigation away. Null when no subscription is active.
        this.leaderboardEventSource = null;
        // Register the unload cleanup exactly once. Doing it here
        // (rather than per-subscribe) means repeat subscriptions don't
        // stack up redundant listeners. closeLeaderboardStream is a
        // safe no-op when there's no active subscription.
        if (typeof window !== 'undefined') {
            window.addEventListener('beforeunload', () => this.closeLeaderboardStream());
        }
    }

    async init() {
        // Kick off both in parallel; neither depends on the other.
        // playerService.getMe is best-effort: a null result just means
        // the claim affordances stay hidden, the rest of the page is
        // unaffected. quizService.getQuizzes throws on non-2xx (#287);
        // a startup-time list failure is similarly best-effort — the
        // page renders an empty state and the player can refresh
        // later instead of seeing an uncaught rejection.
        const [quizzesResult, player] = await Promise.all([
            quizService.getQuizzes().catch(err => {
                console.error('init: getQuizzes failed', err);
                return [];
            }),
            playerService.getMe(),
        ]);
        this.quizzes = quizzesResult;
        this.player = player;
        const deepLinked = this.findDeepLinkedQuiz();
        if (deepLinked) {
            this.deepLinkedQuiz = deepLinked;
            this.selectedQuizId = deepLinked.id;
        }
        // No auto-default to quizzes[0] (#284): the in-page picker was
        // replaced by a link to /quizzes, so /client/ without a deep
        // link has no selection to drive. Leaving selectedQuizId null
        // hides the Start button + leaderboard until the player picks
        // a quiz via /quizzes.
        const existing = await this.checkAlreadyPlayed();
        // Resume on reload (#310): when the player is mid-game (e.g. a
        // mobile pull-to-refresh bounces them off the question screen),
        // skip the start screen and jump straight back into the
        // question. /questions/next is idempotent while the current
        // question's answer window is open, so the same question comes
        // back with the same StartedAt/ExpiredAt anchor — the
        // countdown picks up where it left off rather than restarting.
        // The `=== false` form fails closed if the server ever omits
        // the field (rather than silently resuming on an unknown
        // game state).
        if (existing && existing.completed === false) {
            this.gameId = existing.gameId;
            // Hydrate the running-total chip from the server before
            // rendering the resumed question so the HUD picks up the
            // points already banked instead of starting from zero.
            // Best-effort: a failed fetch just leaves the chip at 0,
            // which is the pre-fix behaviour.
            await this.hydrateScoreFromResults();
            try {
                await this.nextQuestion();
            } catch (err) {
                // Roll back so the start screen renders and the player
                // can retry via the Start button — without this, a
                // transient 5xx on the resume's /questions/next leaves
                // the SPA in a blank half-loaded state with no
                // affordance to recover.
                console.error('resume on init failed', err);
                this.gameId = null;
                this.question = null;
            }
        }
    }

    // hydrateScoreFromResults pulls the player's accumulated points
    // from /api/games/{id}/results so the HUD score chip reflects the
    // pre-reload total on a resume. Silently no-ops when /results
    // fails or the player id is unknown — the chip just stays at 0.
    async hydrateScoreFromResults() {
        if (!this.gameId || !this.player) return;
        try {
            const results = await gameService.getResults(this.gameId);
            const playerScores = results && results.playerScores;
            if (!Array.isArray(playerScores)) return;
            const mine = playerScores.find(p => p.playerId === this.player.id);
            if (mine) this.score = mine.score;
        } catch (err) {
            console.warn('hydrateScoreFromResults failed', err);
        }
    }

    // hasCustomName reports whether the current player has explicitly
    // picked their display name (either through PATCH /api/players/me
    // or through the register form). The templates gate every claim
    // affordance on the negation of this, so a player who has already
    // chosen a name does not see the claim modal/links again — which
    // was the bug fixed in #165: the previous gate (isAnonymous, i.e.
    // "no password_hash") stayed truthy after a PATCH because the
    // claim flow does not set a password, re-opening the modal on
    // every subsequent finished quiz.
    hasCustomName() {
        return !!(this.player && this.player.hasCustomName);
    }

    // isAnonymous reports whether the current player has no password set
    // (anonymous in the credential sense). Distinct from hasCustomName:
    // a player who claims a display name without registering stays
    // anonymous. The start-screen "Playing as" card uses this so the
    // affordance keeps showing post-claim, letting the player retune
    // their name before they start a quiz.
    isAnonymous() {
        return !!(this.player && this.player.isAnonymous);
    }

    // hasOffLeaderboardStanding reports whether the requesting player
    // finished the quiz but landed outside the visible top-N: the
    // leaderboard payload's currentPlayer field is populated AND no
    // visible entry carries isCurrentPlayer. The dedicated "Your
    // score" card on the leaderboard view gates on this so a player
    // who placed 11th+ still sees their own rank and score (#181).
    hasOffLeaderboardStanding() {
        if (!this.leaderboard || !this.leaderboard.currentPlayer) return false;
        return !this.leaderboard.entries.some(e => e.isCurrentPlayer);
    }

    // openClaimModal is the single entry point that both claim
    // affordances (pre-leaderboard auto-open, start-screen "Set your
    // name" link) call. The modal template is mounted via x-if so
    // each open gets a fresh claimNameForm instance with empty
    // input/error state.
    openClaimModal() {
        this.claimModalOpen = true;
    }

    // closeClaimModal hides the modal. Used by the Cancel button,
    // the modal-background click, and the ESC key handler.
    closeClaimModal() {
        this.claimModalOpen = false;
    }

    // claimFromModal is the single onSubmit callback wired into the
    // shared claimNameForm. Returns the discriminated result from
    // PlayerService so the form can render an error banner without
    // knowing anything about HTTP status codes. On success it updates
    // `this.player` (so hasCustomName flips to true and every gated
    // template hides at once), closes the modal, and — if the
    // leaderboard is already rendered — re-fetches it so the player's
    // row swaps from the auto-petname to the chosen name. The re-fetch
    // is best-effort: a failure leaves the stale leaderboard in place
    // (the new name will appear on the next page load) rather than
    // surfacing an error on the success path, since the PATCH itself
    // already succeeded.
    async claimFromModal(username) {
        const result = await playerService.claimName(username);
        if (result.ok) {
            this.player = result.player;
            this.claimModalOpen = false;
            if (this.finished && this.quizSlugId) {
                try {
                    this.leaderboard = await gameService.getQuizLeaderboard(this.quizSlugId);
                    this.animateLeaderboard();
                } catch (err) {
                    console.warn('leaderboard re-fetch after claim failed; row will update on next load', err);
                }
            }
            return result;
        }
        // #289: the server says this account is already non-anonymous,
        // which means our cached `this.player.hasCustomName` was stale
        // (a logged-in admin with a freshly-set password_hash but
        // username_claimed still 0 ended up here). Refresh /me so
        // hasCustomName flips to true, then dismiss the modal — there
        // is nothing for the user to do here.
        if (result.kind === 'already_claimed') {
            const refreshed = await playerService.getMe();
            if (refreshed) this.player = refreshed;
            this.claimModalOpen = false;
        }
        return result;
    }

    // findDeepLinkedQuiz extracts the quiz ID from /play/<slug>-<id> and
    // returns the matching quiz, or null if the path is not a deep link or
    // the ID does not match a known quiz.
    findDeepLinkedQuiz() {
        const match = window.location.pathname.match(PLAY_PATH_PATTERN);
        if (!match) return null;
        const id = parseInt(match[1], 10);
        return this.quizzes.find(q => q.id === id) || null;
    }

    // slugIdFor returns the `${slug}-${id}` form for the selected quiz, or
    // null when no matching quiz exists in this.quizzes.
    slugIdFor(quizId) {
        const quiz = this.quizzes.find(q => q.id === parseInt(quizId));
        return quiz ? `${quiz.slug}-${quiz.id}` : null;
    }

    // selectedQuiz returns the quiz row for selectedQuizId (or null).
    // Drives the share buttons' enabled state and the dialog text;
    // pulled out so the template can `:disabled="!selectedQuiz()"`
    // without re-deriving the row every render.
    selectedQuiz() {
        if (!this.selectedQuizId) return null;
        return this.quizzes.find(q => q.id === parseInt(this.selectedQuizId)) || null;
    }

    // shareCurrentQuiz opens the share dialog with an invitation
    // message for the currently selected quiz. Used by the start-screen
    // share button. The URL points at /play/{slug-id}, the same
    // deep-link the admin share modal emits, so a recipient lands
    // straight on the quiz with OG metadata pre-populated.
    shareCurrentQuiz() {
        const quiz = this.selectedQuiz();
        if (!quiz) return;
        const url = new URL(`/play/${quiz.slug}-${quiz.id}`, window.location.origin).href;
        openShareDialog({
            title: quiz.title,
            text: `Play this quiz: ${quiz.title}`,
            url,
        });
    }

    // shareCurrentResult opens the share dialog with a brag-and-
    // challenge message after the player has finished a quiz. The
    // score is read from the loaded leaderboard payload (see
    // scoreFromLeaderboard) so a revisit or post-finish refresh
    // shares the actual score instead of the JS counter's default
    // of zero.
    shareCurrentResult() {
        if (!this.quizSlugId) return;
        const quiz = this.quizzes.find(q => `${q.slug}-${q.id}` === this.quizSlugId);
        const title = quiz ? quiz.title : 'Top Banana!';
        const url = new URL(`/play/${this.quizSlugId}`, window.location.origin).href;
        const score = this.scoreFromLeaderboard();
        openShareDialog({
            title,
            text: `I scored ${score} on ${title} — think you can beat me?`,
            url,
        });
    }

    // scoreFromLeaderboard returns the requesting player's final
    // score for the current quiz. Prefers the server-computed value
    // carried by the leaderboard payload: a top-N finisher appears
    // in `entries` with isCurrentPlayer=true; an off-leaderboard
    // finisher (#181) only carries `currentPlayer`. Either path
    // yields the correct score regardless of whether the player
    // just finished or revisited an already-played quiz.
    //
    // Falls back to the in-memory accumulator when the leaderboard
    // is somehow null at call time — that shouldn't happen because
    // the share button is gated on quizSlugId and the leaderboard
    // is loaded before quizSlugId is set, but the fallback keeps
    // the share text honest if the order ever changes.
    scoreFromLeaderboard() {
        if (this.leaderboard) {
            const me = this.leaderboard.entries.find(e => e.isCurrentPlayer);
            if (me) return me.score;
            if (this.leaderboard.currentPlayer) return this.leaderboard.currentPlayer.score;
        }

        return this.score;
    }

    // checkAlreadyPlayed pre-flights the resume probe so the start screen
    // can show the "already completed" notification before the player
    // bothers clicking Start. Called from init, on dropdown changes, and
    // after reset so a returning player sees the lockout immediately.
    // Returns the existing game payload (or null) so callers can avoid a
    // second round-trip in startGame.
    //
    // When the player has already completed the selected quiz, the
    // method also primes the leaderboard view (finished=true,
    // quizSlugId set, leaderboard fetched, SSE subscription opened) so
    // the leaderboard renders alongside the start-screen lockout
    // banner. The closeLeaderboardStream / state-reset block at the
    // top makes the helper safe to call repeatedly on dropdown
    // changes: switching to a fresh quiz tears down any leftover
    // already-played view before probing again.
    async checkAlreadyPlayed() {
        this.startError = null;
        // Reset any prior already-played view before probing the new
        // selection. Idempotent: no-ops when nothing is open.
        this.closeLeaderboardStream();
        this.finished = false;
        this.leaderboard = null;
        this.quizSlugId = null;

        const slugId = this.slugIdFor(this.selectedQuizId);
        if (!slugId) return null;

        // Hoist quizSlugId + leaderboard fetch above the completed gate
        // so the start screen surfaces the leaderboard for the selected
        // quiz BEFORE the player clicks Start (#234). The completed
        // branch below still upgrades to the "Game Finished!" view +
        // SSE; the start-screen case stays read-only (no SSE — the
        // player sees current scores at decision time, not a live
        // ticker). Best-effort: a failed fetch lands an empty entries
        // list so the section degrades to its "be the first" state.
        this.quizSlugId = slugId;
        try {
            this.leaderboard = await gameService.getQuizLeaderboard(slugId);
        } catch (err) {
            console.warn('start-screen leaderboard fetch failed', err);
            this.leaderboard = { quizId: 0, entries: [], currentPlayer: null };
        }

        const existing = await gameService.getMyGameForQuiz(slugId);
        if (existing && existing.completed) {
            this.startError = "You've already completed this quiz.";
            this.finished = true;
            // SSE stream so the row repaints when other finishers land
            // (or this player renames themselves via the claim flow).
            this.subscribeLeaderboardStream();
        }

        return existing;
    }

    async startGame() {
        const existing = await this.checkAlreadyPlayed();
        if (this.startError) return;
        const slugId = this.slugIdFor(this.selectedQuizId);
        if (!slugId) return;
        this.quizSlugId = slugId;
        this.score = 0;
        if (existing) {
            this.gameId = existing.gameId;
        } else {
            try {
                const data = await gameService.startGame(this.selectedQuizId);
                this.gameId = data.id;
            } catch (err) {
                // #287: 409 means a game already exists for this
                // (player, quiz) pair — usually a two-tab race past
                // the checkAlreadyPlayed gate above. Recover by
                // re-fetching the existing game so the player still
                // gets through; any other error (500, network) gives
                // up with a visible startError.
                if (err && err.status === 409) {
                    const recovered = await gameService.getMyGameForQuiz(slugId);
                    if (!recovered) {
                        console.error('startGame: 409 with no recoverable game', err);
                        this.startError = "Couldn't start the quiz — please refresh and try again.";
                        return;
                    }
                    this.gameId = recovered.gameId;
                } else {
                    console.error('startGame failed', err);
                    this.startError = "Couldn't start the quiz — please refresh and try again.";
                    return;
                }
            }
        }
        await this.nextQuestion();
    }

    async nextQuestion() {
        if (this.timer) {
            clearInterval(this.timer);
            this.timer = null;
        }
        if (this.revealTimer) {
            clearInterval(this.revealTimer);
            this.revealTimer = null;
        }
        this.revealing = false;
        this.splash = null;
        this.splashOn = false;
        this.submitError = false;
        const question = await gameService.getNextQuestion(this.gameId);
        if (!question) {
            this.finished = true;
            // Re-fetch /me so the player's claim status is current.
            // Could in principle have flipped since page load (rare,
            // but cheap to verify and keeps the UI honest if they
            // logged in mid-quiz from a second tab).
            const fresh = await playerService.getMe();
            if (fresh) this.player = fresh;
            // Render the leaderboard first so the player sees their
            // row populated immediately; then open the claim modal on
            // top — but only if the player has not already chosen a
            // display name. On a successful claim the modal handler
            // re-fetches the leaderboard so the row updates from the
            // auto-petname to the chosen name.
            this.leaderboard = await gameService.getQuizLeaderboard(this.quizSlugId);
            this.animateLeaderboard();
            // Live updates for new finishers landing after this player.
            // EventSource auto-reconnects on transient drops; we close
            // it explicitly on beforeunload so the server-side
            // subscriber map stays clean.
            this.subscribeLeaderboardStream();
            if (!this.hasCustomName()) {
                this.openClaimModal();
            }
            return;
        }
        this.imageError = false;
        this.syncClockFrom(question);
        this.question = question;
        this.startRevealCountdown();
        this.animateQuestionEntrance();
    }

    // syncClockFrom recomputes clockOffset from the serverNow that
    // travels with every question payload. A per-question reset keeps
    // drift bounded without needing a separate clock-sync endpoint;
    // the only remaining error is one-way network delay (RTT/2), which
    // is negligible against a 10-second answer window. A missing
    // serverNow (older server) leaves clockOffset at 0 — the existing
    // skew-vulnerable behaviour, not a regression.
    syncClockFrom(question) {
        if (!question || !question.serverNow) return;
        const serverMs = new Date(question.serverNow).getTime();
        if (!Number.isFinite(serverMs)) return;
        this.clockOffset = serverMs - Date.now();
    }

    // serverTime returns the current time in ms as the server sees it,
    // using clockOffset captured on the last question payload. All
    // per-question countdown math goes through this helper so a
    // skewed device clock can't push the timer past expiredAt
    // (forward skew) or hold it open past the server window (backward
    // skew) — see #180.
    serverTime() {
        return Date.now() + this.clockOffset;
    }

    // startRevealCountdown drives the pre-answer beat (#247) by filling
    // the same progress bar that runs the answer-window countdown.
    // The bar grows 0 → 100 in cyan during the reveal, then on
    // completion the helper flips to startCountdown, which drains the
    // bar 100 → 0 in accent. One element, two phases, continuous
    // visual story.
    //
    // Falls through to startCountdown immediately if the server's
    // startedAt is already in the past — resume on an older game
    // (issued before #247) should not stall on a reveal it never
    // had.
    startRevealCountdown() {
        const startAt = new Date(this.question.startedAt).getTime();
        const revealStart = this.serverTime();
        if (revealStart >= startAt) {
            this.revealing = false;
            this.startCountdown();
            return;
        }
        const revealTotal = startAt - revealStart;
        this.revealing = true;
        this.progress = 0;
        this.revealTimer = setInterval(() => {
            const now = this.serverTime();
            if (now >= startAt) {
                this.progress = 100;
                clearInterval(this.revealTimer);
                this.revealTimer = null;
                this.revealing = false;
                this.startCountdown();
                return;
            }
            this.progress = Math.min(100, ((now - revealStart) / revealTotal) * 100);
        }, 100);
    }

    // animateQuestionEntrance carries the question and answer buttons in
    // with generous travel and a longer settle — the page is intentionally
    // calm at rest, so the entrance is where the personality lives. Run
    // inside requestAnimationFrame so Alpine has committed the new markup
    // before anime.js targets it.
    animateQuestionEntrance() {
        requestAnimationFrame(() => {
            runAnim('.subtitle', {
                opacity: [0, 1],
                translateY: [36, 0],
                duration: 520,
                easing: 'easeOutQuart',
            });
            runAnim('.buttons .button', {
                opacity: [0, 1],
                translateY: [48, 0],
                scale: [0.96, 1],
                duration: 460,
                delay: staggerDelay(85),
                easing: 'easeOutQuart',
            });
        });
    }

    // subscribeLeaderboardStream opens a Server-Sent Events connection
    // for the current quiz's leaderboard and updates `this.leaderboard`
    // on every event. Idempotent: a second call closes any prior
    // subscription before opening a new one. Safe no-op when the
    // browser lacks EventSource (very old WebKit).
    subscribeLeaderboardStream() {
        this.closeLeaderboardStream();
        if (typeof EventSource === 'undefined' || !this.quizSlugId) return;
        const url = `/api/quizzes/${encodeURIComponent(this.quizSlugId)}/leaderboard/stream`;
        const source = new EventSource(url);
        source.onmessage = (ev) => {
            try {
                this.leaderboard = JSON.parse(ev.data);
            } catch (err) {
                console.warn('leaderboard SSE payload was not valid JSON', err);
            }
        };
        source.onerror = () => {
            // EventSource auto-reconnects unless we close it. Leave the
            // existing leaderboard in place so the UI doesn't flicker
            // while the browser retries.
        };
        this.leaderboardEventSource = source;
    }

    // closeLeaderboardStream is safe to call regardless of subscription
    // state. Used both to clean up after a finished quiz and as a
    // defensive guard before opening a fresh subscription.
    closeLeaderboardStream() {
        if (this.leaderboardEventSource) {
            this.leaderboardEventSource.close();
            this.leaderboardEventSource = null;
        }
    }

    // animateLeaderboard slides the leaderboard rows in from the right
    // with a generous stagger so the table assembles itself one row at a
    // time. Defensive against an empty leaderboard.
    animateLeaderboard() {
        requestAnimationFrame(() => {
            runAnim('.table tbody tr', {
                opacity: [0, 1],
                translateX: [40, 0],
                duration: 480,
                delay: staggerDelay(85),
                easing: 'easeOutQuart',
            });
        });
    }

    // animateFeedback gives the feedback notification a noticeable kick
    // — pop-in for correct answers, a bigger shake for wrong ones. The
    // amplitudes are larger than the static design because the rest of
    // the page stays still, so the motion has to carry the moment.
    animateFeedback(correct) {
        requestAnimationFrame(() => {
            if (correct) {
                runAnim('[data-feedback]', {
                    scale: [0.9, 1.06, 1],
                    rotate: ['-1.2deg', '1deg', '0deg'],
                    duration: 560,
                    easing: 'easeOutBack',
                });
            } else {
                runAnim('[data-feedback]', {
                    translateX: [-18, 18, -14, 14, -8, 8, 0],
                    duration: 460,
                    easing: 'easeOutQuad',
                });
            }
        });
    }

    // showSplash flashes a full-screen verdict overlay (#253) for a
    // brief hold before auto-clearing. The fade-in AND the fade-out
    // are both driven by Alpine x-transition classes on the splash
    // element — here we just flip `this.splash` to a variant and
    // then back to null; Alpine animates the transitions in/out via
    // the matching CSS classes (.splash-anim-*).
    //
    // Variants:
    //   'correct'  -> success skin
    //   'wrong'    -> danger skin
    //   'timeout'  -> warning skin
    //
    // Reduced-motion users still see the verdict — the media-query
    // override in _tailwind.css zeroes out the transitions so the
    // element snaps in and out without easing. The button-level
    // correctness reveal (#233) underneath stays visible for the
    // rest of the resolveAndAdvance pause.
    showSplash(variant) {
        this.splash = variant;
        this.splashOn = true;
        // 700ms visible hold before the leave transition kicks in.
        // Combined with the ~280ms enter and ~280ms leave the splash
        // is gone after ~1.26s, well within the resolveAndAdvance
        // pause (2–3s) so the button-level reveal still has time
        // to land. Only `splashOn` flips back — `splash` stays set so
        // the verdict text and skin remain stable through the fade-out
        // (otherwise the ternary in x-text would fall through to
        // 'Time out!' during leave).
        setTimeout(() => {
            this.splashOn = false;
        }, 700);
    }

    // animateTimeout settles the timeout banner in with a soft scale + fade,
    // intentionally quieter than the wrong-answer shake: the player did not
    // make a wrong decision — the clock simply ran out — so the motion
    // should feel like a gentle "moving on" rather than a buzzer.
    animateTimeout() {
        requestAnimationFrame(() => {
            runAnim('[data-feedback]', {
                opacity: [0, 1],
                scale: [0.96, 1],
                duration: 420,
                easing: 'easeOutQuart',
            });
        });
    }

    startCountdown() {
        const start = new Date(this.question.startedAt).getTime();
        const end = new Date(this.question.expiredAt).getTime();
        const total = end - start;

        this.progress = 100;

        this.timer = setInterval(() => {
            const now = this.serverTime();
            const remaining = end - now;
            this.progress = Math.max(0, (remaining / total) * 100);

            if (this.progress <= 0) {
                clearInterval(this.timer);
                this.timer = null;
                // Fire-and-forget: setInterval callbacks can't await,
                // and resolveAndAdvance handles its own teardown.
                void this.handleTimeout();
            }
        }, 100);
    }

    // handleTimeout fires when the per-question countdown reaches zero
    // without a submitted answer. Skips when feedback is already set
    // (the user beat the clock), while a submit is in flight (the
    // POST is racing the timer — let it finish and use the real
    // result), or when a splash is already on screen (defence in
    // depth against the verdict splash being overwritten with
    // "Time out!" right after the player saw "Correct!"). On a real
    // timeout it shows a "Time out!" splash and auto-advances via
    // resolveAndAdvance. No POST is issued: the server's
    // GetNextQuestion advances on the "asked" set rather than the
    // "answered" set, and a missing answer row already produces a
    // zero-score on the leaderboard.
    async handleTimeout() {
        if (this.feedback || this.submittingAnswer || this.splashOn) return;
        this.feedback = { timedOut: true, correct: false, score: 0 };
        this.showSplash('timeout');
        this.animateTimeout();
        await this.resolveAndAdvance();
    }

    async submitAnswer(optionId) {
        if (this.feedback || this.submittingAnswer) return;
        // Clear any prior retry banner so re-clicking after a failed
        // POST visibly dismisses it before the new attempt starts.
        this.submitError = false;
        this.submittingAnswer = true;
        // Stop the per-question countdown the moment the player
        // clicks, BEFORE the POST is in flight. Without this, a
        // setInterval tick could fire during the POST, hit
        // progress<=0, and queue handleTimeout — and even though
        // handleTimeout's guards normally catch the race, an
        // unlucky interleaving showed up in practice as
        // "Correct! → Time out!" rapidly swapping in the splash.
        // Clearing here eliminates the race at its source.
        if (this.timer) {
            clearInterval(this.timer);
            this.timer = null;
        }
        try {
            const fb = await gameService.submitAnswer(this.gameId, this.question.id, optionId);
            // Track which option the player picked so the template can
            // keep the buttons visible during feedback and style the
            // pick separately from the correct option(s) — see #233.
            fb.pickedOptionId = optionId;
            this.feedback = fb;
            this.score += fb.score || 0;
            this.showSplash(fb.correct ? 'correct' : 'wrong');
            this.animateFeedback(this.feedback.correct);
        } catch (err) {
            // POST failed. The retry banner (#179) only makes sense
            // for transient failures the player can recover from by
            // re-clicking: 5xx and network drops. A 400/404 means the
            // server rejected the option or game-question for a
            // permanent reason, so re-clicking would just re-fail —
            // log and let the countdown / timeout flow advance the
            // game instead of pinning the player on a bad banner
            // (#287). Status undefined == network (no response).
            const status = err && err.status;
            const retryable = status === undefined || status >= 500;
            console.error('submitAnswer:', err);
            if (retryable) {
                // Re-arm the countdown so the player keeps the time
                // they had left (expiredAt is server-set and absolute,
                // so startCountdown computes the real remaining
                // window). If expiredAt has already passed, the next
                // tick fires handleTimeout normally and the game
                // still advances.
                this.submitError = true;
                this.startCountdown();

                return;
            }
            // Non-retryable: synthesize a "no answer" feedback so the
            // splash beat + auto-advance path runs and the player
            // doesn't get stuck on a blank, button-less screen.
            this.feedback = { timedOut: true, correct: false, score: 0 };
            this.showSplash('timeout');
            this.animateTimeout();
            await this.resolveAndAdvance();

            return;
        } finally {
            this.submittingAnswer = false;
        }

        // Hold longer when the pick was wrong so the player has time
        // to read the highlighted correct option (#233).
        const pauseMs = this.feedback.correct ? 2000 : 3000;
        await this.resolveAndAdvance(pauseMs);
    }

    // resolveAndAdvance waits the per-question feedback pause and then
    // tears down current-question state before fetching the next one.
    // Shared by submitAnswer and handleTimeout so both paths look the
    // same to the user — only the feedback banner differs. Clears the
    // previous question alongside the feedback so the new render swaps
    // to the "Loading question..." placeholder; without this, the
    // buttons region (gated only on `!feedback`) re-mounts for one
    // frame with the old question's options before nextQuestion()
    // resolves and re-binds them.
    async resolveAndAdvance(pauseMs = 2000) {
        await new Promise(resolve => setTimeout(resolve, pauseMs));
        this.question = null;
        this.feedback = null;
        await this.nextQuestion();
    }

    // optionStateClass returns the class string for an answer button.
    // Composes two layers:
    //   1. Answer-phase TONE — Kahoot-style per-option colour driven
    //      by the option's index, applied on top of .btn-answer
    //      (#253).
    //   2. Feedback SKIN — once the player picks, the correctness
    //      state (correct / wrong / dim) overrides the tone entirely
    //      so the reveal (#233) wins post-pick.
    // Timed-out questions have no correctOptionIds (the server isn't
    // told about a timeout), so every option falls through to dim.
    optionStateClass(option, idx) {
        if (this.feedback) {
            const correctIds = this.feedback.correctOptionIds || [];
            if (correctIds.includes(option.id)) return 'btn-answer-correct';
            if (this.feedback.pickedOptionId === option.id) return 'btn-answer-wrong';
            return 'btn-answer-dim';
        }
        const tones = ['btn-answer-tone-a', 'btn-answer-tone-b', 'btn-answer-tone-c', 'btn-answer-tone-d'];
        return `btn-answer ${tones[idx % tones.length]}`;
    }

}
