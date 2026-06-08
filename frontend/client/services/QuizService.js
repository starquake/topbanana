import { jsonOrThrow } from './api.js';

// QuizService wraps the quiz-list REST endpoint. Throws [ApiError]
// on non-2xx (#287) — the caller in GameApp.init catches the throw
// and falls back to an empty quizzes list so the page still renders.
export class QuizService {
    async getQuizzes() {
        const response = await fetch('/api/quizzes');
        return jsonOrThrow(response);
    }
}

export const quizService = new QuizService();
