import { quizService } from '../services/QuizService.js';
import { gameService } from '../services/GameService.js';

// PLAY_PATH_PATTERN matches /play/<anything>-<integer>; the integer suffix
// is the quiz ID.
const PLAY_PATH_PATTERN = /^\/play\/.+-(\d+)\/?$/;

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
            return;
        }
        this.imageError = false;
        this.question = question;
        this.startCountdown();
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
        
        // Stop timer
        if (this.timer) {
            clearInterval(this.timer);
            this.timer = null;
        }

        // Wait for 2 seconds before moving to next question
        await new Promise(resolve => setTimeout(resolve, 2000));
        
        this.feedback = null;
        await this.nextQuestion();
    }

}
