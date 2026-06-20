import { quizService } from '../services/QuizService.js';
import { gameService } from '../services/GameService.js';
import { playerService } from '../services/PlayerService.js';
import { runAnim } from '@shared/anim.js';
import { clockOffsetFromServerNow, serverTime } from '@shared/serverClock.js';
import { optionStateClass } from '../util/answerOptions.js';
import { openShareDialog } from '@shared/share.js';
import { preloadImage } from '@shared/preloadImage.js';
import { createQuestionAudio, initialMuted } from '@shared/questionAudio.js';
import { loadAudioClip } from '@shared/audioLoader.js';

// PLAY_PATH_PATTERN matches /play/<anything>-<integer>; the integer suffix
// is the quiz ID.
const PLAY_PATH_PATTERN = /^\/play\/.+-(\d+)\/?$/;

export class GameApp {
    constructor() {
        this.quizzes = [];
        this.selectedQuizId = null;
        this.gameId = null;
        this.question = null;
        // In-flight pre-fetch of the next item, kicked off when feedback lands
        // so the network round trip overlaps the feedback pause. nextQuestion
        // consumes it instead of making a fresh fetch, so the swap to the new
        // question lands as one Alpine tick rather than going through a
        // visible "Loading question..." gap (#982).
        this.nextItemPromise = null;
        // Current round-boundary item shown to the player (#444). Set
        // when /next returns type=round_boundary; cleared when the player
        // clicks Continue (markRoundSeen) before fetching the next item.
        // Only one of `question` and `roundItem` is non-null at a time —
        // the play sequence is strictly serial.
        this.roundItem = null;
        // Position of the last question issued. The server doesn't bump
        // position over a round boundary, so the HUD chip on the
        // round-summary card reads this value (rather than
        // question.position, which gets nulled in resolveAndAdvance
        // before the round payload lands).
        this.lastQuestionPosition = 0;
        // Surfaces a "couldn't continue" banner on the round-summary card
        // when the POST /rounds/{id}/seen call fails. Cleared on the next
        // Continue click. Same shape as submitError, but distinct so a
        // retry banner from a prior question doesn't ghost-render on
        // the round-summary card.
        this.roundContinueError = false;
        // True while the markRoundSeen POST is in flight. Guards the
        // Continue button so a double-click doesn't fire two POSTs.
        this.continuingRound = false;
        // Drives the round-boundary countdown bar (#548). Starts at 100
        // and drains 100 -> 0 over the boundary window (the quiz's
        // default per-question duration), mirroring the answer-window
        // bar's visual. When it reaches zero the card auto-advances via
        // the same path Continue uses. `roundTimer` holds the interval
        // handle so it can be cleared on manual continue, on advance,
        // and when a new item replaces the boundary.
        this.roundProgress = 100;
        this.roundTimer = null;
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
        // True when the URL matched the /play/<slug>-<id> pattern but the id
        // resolved to no quiz in the loaded list (deleted, hidden, or a stale
        // link). Surfaces a "That quiz isn't available" note above the picker
        // instead of silently dropping the player onto the generic picker
        // (#802). Reset whenever a real quiz is picked.
        this.deepLinkUnavailable = false;
        // Gates the deep-link title/description so it only paints once
        // checkAlreadyPlayed has resolved the start state. Without it the
        // header renders optimistically on a deep link, then vanishes when
        // an already-completed quiz flips `finished` true after the
        // probe's two fetches — a visible title/leaderboard layout shift
        // on load. Flipped true at the tail of every checkAlreadyPlayed
        // call and reset to false whenever the selected quiz changes so a
        // later switch re-hides until the new probe lands.
        this.startStateResolved = false;
        // Current player as returned by GET /api/players/me. Stays null
        // until init() resolves; templates guard with `player &&`. When
        // the player renames, the PATCH response replaces this object
        // so player.displayName and player.hasCustomName flow through every
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
        // Mute state for the per-question audio (#1059), seeded from the
        // persisted preference so a player who muted earlier stays muted.
        // Bound to the mute control and to the <audio> element's `muted`
        // attribute. Default unmuted.
        this.audioMuted = initialMuted();
        // True when the browser blocked autoplay (the play() promise rejected),
        // so the template surfaces an explicit play control as a fallback. A
        // reload mid-game or a strict autoplay policy can leave the first clip
        // blocked until the player taps the play control.
        this.audioBlocked = false;
        // True while the audio loading beat is on screen for a question with a
        // audio (#1070): the clip is buffering before the question is revealed.
        // The view shows a spinner + "Loading audio..." and the reveal countdown
        // does not start until the clip is ready, times out, or errors.
        this.audioLoading = false;
        // Shared play / mute / replay / per-question-guard controller (#1070).
        // Its methods take the live Alpine `this` on each call so reactive
        // writes go through the proxy; the guard mirrors the big screen's
        // once-per-question-id rule so a re-render mid-question cannot restart
        // the clip.
        this.audio = createQuestionAudio();
        if (typeof window !== 'undefined') {
            window.addEventListener('beforeunload', () => {
                this.clearRoundTimer();
            });
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
        } else if (this.hasDeepLinkPath()) {
            // The URL is a /play/<slug>-<id> deep link but the id matched no
            // quiz in the loaded list (deleted, hidden, or a stale link).
            // Note it above the picker rather than dropping the player onto
            // the generic picker with no explanation (#802).
            this.deepLinkUnavailable = true;
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
                this.roundItem = null;
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
    // anonymous. Distinct from !isAuthenticated: an OAuth-only player
    // also has no password but IS authenticated. Retained for paths
    // that specifically care about the credential layer.
    isAnonymous() {
        return !!(this.player && this.player.isAnonymous);
    }

    // isAuthenticated reports whether the player is known to the
    // system through some credential (password, OAuth identity, or
    // the seeded admin role). The claim-name CTA and end-of-quiz
    // auto-open both gate on the negation of this: a signed-in
    // player already has a stable identity and should never see the
    // "Set your name" prompt — displayName changes for them belong on
    // the future profile page (#410), not the in-game modal.
    isAuthenticated() {
        return !!(this.player && this.player.isAuthenticated);
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
    async claimFromModal(displayName) {
        const result = await playerService.claimName(displayName);
        if (result.ok) {
            this.player = result.player;
            this.claimModalOpen = false;
            if (this.finished && this.quizSlugId) {
                try {
                    this.leaderboard = await gameService.getQuizLeaderboard(this.quizSlugId);
                } catch (err) {
                    console.warn('leaderboard re-fetch after claim failed; row will update on next load', err);
                }
            }
            return result;
        }
        // #289: the server says this account is already non-anonymous,
        // which means our cached `this.player.hasCustomName` was stale
        // (a logged-in admin with a freshly-set password_hash but
        // displayName_claimed still 0 ended up here). Refresh /me so
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

    // hasDeepLinkPath reports whether the current URL is a /play/<slug>-<id>
    // deep link, regardless of whether that id resolves to a loaded quiz. Used
    // to tell a stale/hidden deep link (note "not available") apart from a bare
    // /client/ visit (show the picker quietly).
    hasDeepLinkPath() {
        return PLAY_PATH_PATTERN.test(window.location.pathname);
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
    // When the player has already completed the selected quiz, the method
    // also primes the leaderboard view (finished=true, quizSlugId set,
    // leaderboard fetched) so it renders alongside the start-screen lockout
    // banner. The state-reset block at the top makes the helper safe to call
    // repeatedly on dropdown changes: switching to a fresh quiz tears down
    // any leftover already-played view before probing again.
    async checkAlreadyPlayed() {
        this.startError = null;

        const slugId = this.slugIdFor(this.selectedQuizId);
        // A real quiz is selected, so any "deep link unavailable" note from a
        // stale URL no longer applies (#802).
        if (slugId) this.deepLinkUnavailable = false;
        // Only tear down the prior leaderboard view when the selected
        // quiz actually changed. checkAlreadyPlayed is also re-entered
        // from startGame() for the same quiz; closing + reopening the
        // SSE there shows up as a spurious NS_ERROR_PARTIAL_TRANSFER
        // in Firefox even though the round-trip is intentional.
        if (slugId !== this.quizSlugId) {
            this.finished = false;
            this.leaderboard = null;
            this.quizSlugId = null;
            // Re-hide the deep-link header until this probe resolves the
            // new quiz's start state, so a switch can't flash the prior
            // resolved header against the new quiz's pending state.
            this.startStateResolved = false;
        }

        if (!slugId) {
            this.startStateResolved = true;

            return null;
        }

        // Hoist quizSlugId + leaderboard fetch above the completed gate so the
        // start screen surfaces the leaderboard for the selected quiz BEFORE
        // the player clicks Start (#234). The in-game view is intentionally
        // leaderboard-free to keep the answer flow uncluttered. Best-effort: a
        // failed fetch lands an empty entries list so the section degrades to
        // its "be the first" state.
        const firstVisitForQuiz = this.quizSlugId !== slugId;
        this.quizSlugId = slugId;
        if (firstVisitForQuiz) {
            try {
                this.leaderboard = await gameService.getQuizLeaderboard(slugId);
            } catch (err) {
                console.warn('start-screen leaderboard fetch failed', err);
                this.leaderboard = { quizId: 0, entries: [], currentPlayer: null };
            }
        }

        const existing = await gameService.getMyGameForQuiz(slugId);
        if (existing && existing.completed) {
            this.startError = "You've already completed this quiz.";
            this.finished = true;
        }

        // Start state is now known: either the deep-link header may paint
        // (not completed) or it stays hidden behind `!finished`. Flipping
        // this last means the header appears once, in its final position,
        // rather than flashing in then out.
        this.startStateResolved = true;

        return existing;
    }

    async startGame() {
        const existing = await this.checkAlreadyPlayed();
        if (this.startError) return;
        const slugId = this.slugIdFor(this.selectedQuizId);
        if (!slugId) return;
        this.quizSlugId = slugId;
        this.score = 0;
        // Clear any leftover round-card state from a prior session in
        // the same tab so the gameplay view doesn't render the old
        // round-summary card for one frame before /next resolves.
        this.roundItem = null;
        this.roundContinueError = false;
        this.lastQuestionPosition = 0;
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

    // prefetchNextItem kicks off the /next round trip while the feedback
    // pause is still on screen, so by the time the pause expires the
    // response is already in hand and nextQuestion can swap to the new
    // question without an intermediate "Loading question..." render (#982).
    // Idempotent: a second call while one is in flight is a no-op.
    prefetchNextItem() {
        if (this.nextItemPromise || !this.gameId) return;
        this.nextItemPromise = gameService.getNextQuestion(this.gameId).catch((err) => {
            console.warn('prefetch next item failed', err);
            this.nextItemPromise = null;

            return null;
        });
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
        this.clearRoundTimer();
        // Stop a still-playing clip before swapping to the next item so the
        // prior question's audio never bleeds into the next beat (#1070).
        this.audio.stop(this);
        this.audioLoading = false;
        this.revealing = false;
        this.submitError = false;
        let item;
        if (this.nextItemPromise) {
            item = await this.nextItemPromise;
            this.nextItemPromise = null;
        }
        if (!item) {
            item = await gameService.getNextQuestion(this.gameId);
        }
        if (!item) {
            this.feedback = null;
            this.finished = true;
            // Re-fetch /me so the player's claim status is current.
            // Could in principle have flipped since page load (rare,
            // but cheap to verify and keeps the UI honest if they
            // logged in mid-quiz from a second tab). getMe already
            // swallows its own errors and returns null, but wrap the
            // await defensively so a future change there can never
            // leave the player stuck short of the finished view.
            try {
                const fresh = await playerService.getMe();
                if (fresh) this.player = fresh;
            } catch (err) {
                console.warn('finish /me refresh failed', err);
            }
            // Render the leaderboard first so the player sees their
            // row populated immediately; then open the claim modal on
            // top — but only if the player has not already chosen a
            // display name. On a successful claim the modal handler
            // re-fetches the leaderboard so the row updates from the
            // auto-petname to the chosen name. The SSE stream was
            // already opened in checkAlreadyPlayed() (#244), so this
            // fetch is just a defensive snapshot for the case where
            // SSE never connected — re-subscribing here would tear
            // down the live connection and produce a spurious
            // NS_ERROR_PARTIAL_TRANSFER in Firefox.
            //
            // A 5xx / network blip on this fetch used to escape past
            // finished=true and leave the player stuck on the
            // "Loading leaderboard..." fallback; degrade gracefully
            // to an empty board so the SSE event (or the next page
            // load) can repaint it (#361).
            try {
                this.leaderboard = await gameService.getQuizLeaderboard(this.quizSlugId);
            } catch (err) {
                console.warn('finish leaderboard fetch failed', err);
                this.leaderboard = { quizId: 0, entries: [], currentPlayer: null };
            }
            // Auto-open the claim modal only for visitors who have not
            // both (a) authenticated AND (b) picked a custom name yet.
            // Authenticated players never see the modal — their
            // displayName is already stable and changes go through the
            // profile page (#410). Anonymous players who claimed a
            // petname via PATCH /api/players/me also skip the prompt
            // (hasCustomName true) so the modal does not re-open on
            // every finished quiz (#165).
            if (!this.isAuthenticated() && !this.hasCustomName()) {
                this.openClaimModal();
            }
            return;
        }
        // Round-boundary variant (#444): no timing, no answer window —
        // the player clicks Continue to acknowledge. The HUD chip on
        // the round-summary card reads `lastQuestionPosition` (the server
        // doesn't bump position over a round boundary, and
        // resolveAndAdvance has already nulled `question` by the time we
        // land here). serverNow lives on both variants, so the
        // clock-offset reconciliation still happens.
        if (item.type === 'round_boundary') {
            this.syncClockFrom(item);
            this.feedback = null;
            this.roundItem = item;
            // Keep the running-score chip honest: the server hands us
            // its authoritative total on the round payload so a
            // resume mid-game also picks up the right value.
            if (typeof item.score === 'number') this.score = item.score;
            this.startRoundCountdown();

            return;
        }
        this.imageError = false;
        this.syncClockFrom(item);
        this.feedback = null;
        this.roundItem = null;
        this.question = item;
        if (typeof item.position === 'number') this.lastQuestionPosition = item.position;
        // Fire-and-forget so the read beat starts immediately while the
        // browser fetches the bytes in parallel; by the time the answer
        // window opens, the <img> below renders from cache.
        if (item.imageUrl) void preloadImage(item.imageUrl);
        // Reset the autoplay-blocked fallback for the new question; the audio
        // controller re-flags it if the fresh play() is rejected.
        this.audioBlocked = false;
        if (item.audioUrl) {
            // Loading beat (#1070): buffer the clip behind a spinner before the
            // question is revealed, then play it from the top and start the
            // reveal countdown. The loading screen drives the single fetch of
            // the bytes (the <audio> element uses preload="none"), so there is
            // no double-fetch. loadAudioClip resolves on canplaythrough, a
            // ~5s timeout, or an error, so the question always proceeds.
            await this.runAudioLoadingBeat(item);
            return;
        }
        this.startRevealCountdown();
    }

    // runAudioLoadingBeat shows the loading screen while the current question's
    // clip buffers, then plays it from the top and hands off to the reveal
    // countdown (#1070). A re-entrant nextQuestion (the player advanced, or a
    // resume re-fetched) clears audioLoading and swaps `question`, so the guard
    // below abandons a stale beat without starting the reveal or the clip.
    async runAudioLoadingBeat(item) {
        this.audioLoading = true;
        await loadAudioClip(item.audioUrl);
        // Bail if the question moved on while the clip was loading.
        if (!this.question || this.question.id !== item.id) return;
        this.audioLoading = false;
        if (this.$nextTick) {
            this.$nextTick(() => this.audio.start(this, this.question.id, false, this.question.audioRepeat));
        } else {
            this.audio.start(this, this.question.id, false, this.question.audioRepeat);
        }
        this.startRevealCountdown();
    }

    // syncClockFrom recomputes clockOffset from the serverNow that
    // travels with every question payload. A per-question reset keeps
    // drift bounded without needing a separate clock-sync endpoint;
    // the only remaining error is one-way network delay (RTT/2), which
    // is negligible against a 10-second answer window. A missing
    // serverNow (older server) leaves clockOffset at 0 — the existing
    // skew-vulnerable behaviour, not a regression.
    syncClockFrom(question) {
        const offset = clockOffsetFromServerNow(question && question.serverNow);
        if (offset !== null) this.clockOffset = offset;
    }

    // serverTime returns the current time in ms as the server sees it,
    // using clockOffset captured on the last question payload. All
    // per-question countdown math goes through this helper so a
    // skewed device clock can't push the timer past expiredAt
    // (forward skew) or hold it open past the server window (backward
    // skew) — see #180.
    serverTime() {
        return serverTime(this.clockOffset);
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

    // animateRoundIntro plays the round intro card's entrance: a brief
    // fade + rise. The from-state (opacity 0, translateY) is supplied by
    // anime via the [from, to] array form, NOT by a CSS class — runAnim
    // no-ops under reduced motion or a missing global, and in that case
    // the card must already be at its visible resting state rather than
    // stuck at opacity 0. Triggered from x-init with $el as the card.
    animateRoundIntro(el) {
        requestAnimationFrame(() => {
            runAnim(el, {
                opacity: [0, 1],
                translateY: [12, 0],
                duration: 380,
                ease: 'outQuad',
            });
        });
    }

    // animateRoundResults plays the recap card's entrance, then staggers
    // the recap figures (score, correct/total, running total) so the
    // numbers land one after another. As with animateRoundIntro the
    // from-state is anime-driven, so a reduced-motion user or a missing
    // anime global sees the fully visible card and figures immediately.
    animateRoundResults(el) {
        requestAnimationFrame(() => {
            runAnim(el, {
                opacity: [0, 1],
                translateY: [12, 0],
                duration: 380,
                ease: 'outQuad',
            });
            const a = typeof window !== 'undefined' ? window.anime : null;
            const figures = el.querySelectorAll('[data-recap-figure]');
            runAnim(figures, {
                opacity: [0, 1],
                translateY: [10, 0],
                duration: 420,
                delay: a && typeof a.stagger === 'function' ? a.stagger(120, { start: 120 }) : 120,
                ease: 'outBack',
            });
        });
    }

    startCountdown() {
        const start = new Date(this.question.startedAt).getTime();
        const end = new Date(this.question.expiredAt).getTime();
        const total = end - start;

        // A non-positive or NaN window never crosses zero, so the interval would
        // spin forever; resolve to the timed-out state immediately instead.
        if (!Number.isFinite(total) || total <= 0) {
            this.progress = 0;
            void this.handleTimeout();
            return;
        }

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
    // (the user beat the clock — `feedback` is the single source of the
    // verdict eyebrow, so this also stops a "Time up" from overwriting a
    // "Correct!"/"Not quite" the player already saw) or while a submit
    // is in flight (the POST is racing the timer — let it finish and use
    // the real result). On a real timeout it sets timed-out feedback so
    // the eyebrow reads "Time up" and auto-advances via
    // resolveAndAdvance. No POST is issued: the server's
    // GetNextQuestion advances on the "asked" set rather than the
    // "answered" set, and a missing answer row already produces a
    // zero-score on the leaderboard.
    async handleTimeout() {
        if (this.feedback || this.submittingAnswer) return;
        this.feedback = { timedOut: true, correct: false, score: 0 };
        this.prefetchNextItem();
        await this.resolveAndAdvance();
    }

    // startRoundCountdown drives the round-boundary auto-advance (#548).
    // It drains the same kind of progress bar the answer window uses,
    // 100 -> 0 over [startedAt, expiredAt], on the server-clock basis
    // serverTime() provides (clockOffset is reconciled from serverNow in
    // the round_boundary handler before this runs). When the bar hits
    // zero it advances by the same path Continue takes (continueRound),
    // so the manual and automatic exits share one code path. If
    // expiredAt is already in the past (e.g. a resume that lands on an
    // expired boundary), it advances promptly without spinning an
    // interval.
    startRoundCountdown() {
        this.clearRoundTimer();
        if (!this.roundItem || !this.roundItem.expiredAt) return;
        const start = new Date(this.roundItem.startedAt).getTime();
        const end = new Date(this.roundItem.expiredAt).getTime();
        const total = end - start;
        if (!Number.isFinite(total) || total <= 0) {
            this.roundProgress = 0;
            void this.continueRound();
            return;
        }
        if (this.serverTime() >= end) {
            this.roundProgress = 0;
            void this.continueRound();
            return;
        }
        this.roundProgress = 100;
        this.roundTimer = setInterval(() => {
            const remaining = end - this.serverTime();
            this.roundProgress = Math.max(0, (remaining / total) * 100);
            if (this.roundProgress <= 0) {
                this.clearRoundTimer();
                // Fire-and-forget: setInterval callbacks can't await,
                // and continueRound handles its own teardown.
                void this.continueRound();
            }
        }, 100);
    }

    // clearRoundTimer cancels the round-boundary auto-advance interval.
    // Safe to call when no timer is pending. Called before starting a
    // fresh countdown, on manual continue, on successful advance, and
    // whenever a new item replaces the boundary so a stale interval
    // can't fire after the card is gone.
    clearRoundTimer() {
        if (this.roundTimer) {
            clearInterval(this.roundTimer);
            this.roundTimer = null;
        }
    }

    async submitAnswer(optionId) {
        // Defence in depth (#444): no answer buttons render on the
        // round-summary card, but if a synthetic click ever reached here
        // mid-round-boundary the POST would 404 (the questionID is from
        // the prior question, which is already answered) and confuse the
        // submitError retry path. Bail before the fetch.
        if (this.roundItem) return;
        if (this.feedback || this.submittingAnswer) return;
        // Capture the tap time at the top of the handler, BEFORE any
        // awaits, so the server-side clamp gets the click instant
        // rather than the post-POST commit time. The server re-clamps
        // it against [question.startedAt, time.Now()] so we can send
        // Date.now() verbatim — no need to apply clockOffset here
        // (#237).
        const tappedAt = new Date().toISOString();
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
        // "Correct! → Time up" rapidly swapping in the verdict.
        // Clearing here eliminates the race at its source.
        if (this.timer) {
            clearInterval(this.timer);
            this.timer = null;
        }
        try {
            const fb = await gameService.submitAnswer(this.gameId, this.question.id, optionId, tappedAt);
            // Track which option the player picked so the template can
            // keep the buttons visible during feedback and style the
            // pick separately from the correct option(s) — see #233.
            fb.pickedOptionId = optionId;
            this.feedback = fb;
            this.score += fb.score || 0;
            this.prefetchNextItem();
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
            // reveal beat + auto-advance path runs (the eyebrow reads
            // "Time up") and the player doesn't get stuck on a blank,
            // button-less screen.
            this.feedback = { timedOut: true, correct: false, score: 0 };
            this.prefetchNextItem();
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
    // swaps to the next item. Shared by submitAnswer and handleTimeout so
    // both paths look the same to the user; only the verdict eyebrow text
    // differs. The next item is pre-fetched via prefetchNextItem the moment
    // feedback lands, so nextQuestion mostly consumes a resolved promise and
    // the swap to the new question lands as one Alpine tick. Feedback is
    // cleared AFTER question/roundItem is set so the buttons grid never
    // re-mounts a frame with the old question's options between
    // feedback-clear and question-set (#233).
    async resolveAndAdvance(pauseMs = 2000) {
        await new Promise(resolve => setTimeout(resolve, pauseMs));
        await this.nextQuestion();
    }

    // continueRound is the Continue button's click handler on both the
    // round intro and round recap cards (#548). POSTs the seen ack for
    // the current phase ('intro' or 'results'), then calls
    // nextQuestion() to load whatever comes next (the round's first
    // question, another round boundary, or 404 → finished).
    //
    // The round card stays mounted until nextQuestion() swaps roundItem
    // and question together in one tick. Clearing roundItem here while
    // question still held the previous round's last question flashed
    // that stale question for a frame (#1049). continuingRound stays
    // true across the whole flow so a second click cannot re-enter while
    // the round card is still on screen.
    //
    // On a network / 5xx failure the round card stays visible with a
    // retry banner — silently losing the click would strand the player
    // on a screen with no affordance to recover. The store-side ack is
    // idempotent so a retry after a transient failure is safe.
    async continueRound() {
        if (!this.roundItem || this.continuingRound) return;
        // Cancel the auto-advance countdown the moment we commit to
        // advancing (#548). Without this a pending interval tick could
        // fire a second continueRound while the markRoundSeen POST is
        // in flight; the continuingRound guard catches the re-entry,
        // but clearing the timer here stops it at the source so the
        // manual skip and the auto-advance can never both fire.
        this.clearRoundTimer();
        this.continuingRound = true;
        this.roundContinueError = false;
        try {
            await gameService.markRoundSeen(this.gameId, this.roundItem.id, this.roundItem.phase);
            await this.nextQuestion();
        } catch (err) {
            console.error('continueRound:', err);
            this.roundContinueError = true;
        } finally {
            this.continuingRound = false;
        }
    }

    // roundTitle is the round-intro heading: the current round's title, or an
    // empty string when there is no round to name. Mirrors JoinApp.roundTitle
    // so the shared round-intro partial binds the same expression on both the
    // solo and live surfaces.
    roundTitle() {
        return this.roundItem && this.roundItem.title ? this.roundItem.title : '';
    }

    // roundSummary is the optional copy beneath the round title, empty when the
    // round has no summary so the partial's x-if skips it. Mirrors
    // JoinApp.roundSummary.
    roundSummary() {
        return this.roundItem && this.roundItem.summary ? this.roundItem.summary : '';
    }

    // getAudioEl returns the persistent <audio> element. It is a permanent child
    // of the always-rendered game container (#1085), so this resolves it for
    // every question and start() can never run before it exists. The lookup goes
    // through document, not this.$root: start() runs from a deferred $nextTick on
    // a re-entrant async path (resolveAndAdvance -> nextQuestion ->
    // runAudioLoadingBeat) where the magic this.$root is not reliably bound, so a
    // $root query intermittently saw null and dropped the next question into the
    // blocked fallback. The element is the only one with this testid on the page.
    getAudioEl() {
        return document.querySelector('[data-testid="question-audio"]') || null;
    }

    // replayAudio restarts the current question's audio from the play/replay
    // control. The shared controller clears the blocked fallback (the click is a
    // user gesture) and bypasses the per-question guard.
    replayAudio() {
        this.audio.replay(this, this.question?.audioRepeat);
    }

    // toggleMute flips and persists the mute preference through the shared
    // controller, which applies it to the live <audio> element at once so a
    // mid-clip toggle takes effect immediately.
    toggleMute() {
        this.audio.toggleMute(this);
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
        return optionStateClass(option, idx, {
            revealed: !!this.feedback,
            correctIds: this.feedback ? this.feedback.correctOptionIds || [] : [],
            pickedId: this.feedback ? this.feedback.pickedOptionId : null,
        });
    }

}
