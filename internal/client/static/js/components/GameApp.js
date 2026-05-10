import { quizService } from '../services/QuizService.js';
import { gameService } from '../services/GameService.js';

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
    }

    async init() {
        this.quizzes = await quizService.getQuizzes();
        const deepLinked = this.findDeepLinkedQuiz();
        if (deepLinked) {
            this.deepLinkedQuiz = deepLinked;
            this.selectedQuizId = deepLinked.id;
        } else if (this.quizzes.length > 0) {
            this.selectedQuizId = this.quizzes[0].id;
        }
        await this.checkAlreadyPlayed();
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
    async checkAlreadyPlayed() {
        this.startError = null;
        const slugId = this.slugIdFor(this.selectedQuizId);
        if (!slugId) return null;
        const existing = await gameService.getMyGameForQuiz(slugId);
        if (existing && existing.completed) {
            this.startError = "You've already completed this quiz.";
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
            this.leaderboard = await gameService.getQuizLeaderboard(this.quizSlugId);
            this.animateLeaderboard();
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
                runAnim('.notification', {
                    scale: [0.9, 1.06, 1],
                    rotate: ['-1.2deg', '1deg', '0deg'],
                    duration: 560,
                    easing: 'easeOutBack',
                });
            } else {
                runAnim('.notification', {
                    translateX: [-18, 18, -14, 14, -8, 8, 0],
                    duration: 460,
                    easing: 'easeOutQuad',
                });
            }
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
            }
        }, 100);
    }

    async submitAnswer(optionId) {
        if (this.feedback) return;
        this.feedback = await gameService.submitAnswer(this.gameId, this.question.id, optionId);
        this.animateFeedback(this.feedback.correct);

        // Stop timer
        if (this.timer) {
            clearInterval(this.timer);
            this.timer = null;
        }

        // Wait for 2 seconds before moving to next question
        await new Promise(resolve => setTimeout(resolve, 2000));

        // Clear the previous question alongside the feedback so the new
        // render swaps to the "Loading question..." placeholder. Without
        // this, the buttons region (gated only on `!feedback`) re-mounts
        // for one frame with the *old* question's options before
        // nextQuestion() resolves and re-binds them.
        this.question = null;
        this.feedback = null;
        await this.nextQuestion();
    }

}
