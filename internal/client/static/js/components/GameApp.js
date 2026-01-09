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
        const question = await gameService.getNextQuestion(this.gameId);
        if (!question) {
            this.finished = true;
            this.results = await gameService.getResults(this.gameId);
            return;
        }
        this.question = question;
    }

    async submitAnswer(optionId) {
        await gameService.submitAnswer(this.gameId, this.question.id, optionId);
        await this.nextQuestion();
    }

    reset() {
        this.gameId = null;
        this.question = null;
        this.finished = false;
        this.results = null;
    }
}
