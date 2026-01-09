import { quizService } from '../services/QuizService.js';
import { gameService } from '../services/GameService.js';

export class GameApp {
    constructor() {
        this.quizzes = [];
        this.selectedQuizId = null;
        this.gameId = null;
        this.question = null;
        this.finished = false;
        this.results = null;
        this.feedback = null;
        this.progress = 100;
        this.timer = null;
    }

    async init() {
        this.quizzes = await quizService.getQuizzes();
        if (this.quizzes.length > 0) {
            this.selectedQuizId = this.quizzes[0].id;
        }
    }

    async startGame() {
        const data = await gameService.startGame(this.selectedQuizId);
        this.gameId = data.id;
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
            this.results = await gameService.getResults(this.gameId);
            return;
        }
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

    reset() {
        if (this.timer) {
            clearInterval(this.timer);
            this.timer = null;
        }
        this.gameId = null;
        this.question = null;
        this.finished = false;
        this.results = null;
        this.feedback = null;
        this.progress = 100;
    }
}
