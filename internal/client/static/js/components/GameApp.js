import { quizService } from '../services/QuizService.js';
import { gameService } from '../services/GameService.js';
import { playerService } from '../services/PlayerService.js';

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
        // unaffected.
        const [quizzes, player] = await Promise.all([
            quizService.getQuizzes(),
            playerService.getMe(),
        ]);
        this.quizzes = quizzes;
        this.player = player;
        const deepLinked = this.findDeepLinkedQuiz();
        if (deepLinked) {
            this.deepLinkedQuiz = deepLinked;
            this.selectedQuizId = deepLinked.id;
        } else if (this.quizzes.length > 0) {
            this.selectedQuizId = this.quizzes[0].id;
        }
        await this.checkAlreadyPlayed();
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

    // openClaimModal is the single entry point that any of the three
    // affordances (pre-leaderboard auto-open, inline "Set my name"
    // link, start-screen "Set your name" link) calls. The modal
    // template is mounted via x-if so each open gets a fresh
    // claimNameForm instance with empty input/error state.
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
        const existing = await gameService.getMyGameForQuiz(slugId);
        if (existing && existing.completed) {
            this.startError = "You've already completed this quiz.";
            this.quizSlugId = slugId;
            this.leaderboard = await gameService.getQuizLeaderboard(slugId);
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
        if (existing) {
            this.gameId = existing.gameId;
        } else {
            const data = await gameService.startGame(this.selectedQuizId);
            this.gameId = data.id;
        }
        await this.nextQuestion();
    }

    async nextQuestion() {
        if (this.timer) {
            clearInterval(this.timer);
            this.timer = null;
        }
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
        this.question = question;
        this.startCountdown();
        this.animateQuestionEntrance();
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
            const now = new Date().getTime();
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
    // (the user beat the clock) or while a submit is in flight (the
    // POST is racing the timer — let it finish and use the real
    // result). On a real timeout it shows a "Time out!" notification
    // and auto-advances via resolveAndAdvance after the same 2s pause
    // the answered path uses. No POST is issued: the server's
    // GetNextQuestion advances on the "asked" set rather than the
    // "answered" set, and a missing answer row already produces a
    // zero-score on the leaderboard.
    async handleTimeout() {
        if (this.feedback || this.submittingAnswer) return;
        this.feedback = { timedOut: true, correct: false, score: 0 };
        this.animateTimeout();
        await this.resolveAndAdvance();
    }

    async submitAnswer(optionId) {
        if (this.feedback || this.submittingAnswer) return;
        this.submittingAnswer = true;
        try {
            this.feedback = await gameService.submitAnswer(this.gameId, this.question.id, optionId);
            this.animateFeedback(this.feedback.correct);
        } finally {
            this.submittingAnswer = false;
        }

        // Stop the countdown so it cannot fire handleTimeout on top
        // of a real submission while we wait for the 2s feedback pause.
        if (this.timer) {
            clearInterval(this.timer);
            this.timer = null;
        }

        await this.resolveAndAdvance();
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
    async resolveAndAdvance() {
        await new Promise(resolve => setTimeout(resolve, 2000));
        this.question = null;
        this.feedback = null;
        await this.nextQuestion();
    }

}
