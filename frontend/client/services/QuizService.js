import { jsonOrThrow } from './api.js';

// QuizService wraps the quiz-list REST endpoint. Throws [ApiError]
// on non-2xx (#287) — the caller in GameApp.init catches the throw
// and falls back to an empty quizzes list so the page still renders.
export class QuizService {
    async getQuizzes() {
        const response = await fetch('/api/quizzes');
        return jsonOrThrow(response);
    }

    // getQuizMeta resolves a single quiz's metadata by its `${slug}-${id}`
    // deep-link key so a private or unlisted quiz absent from the public list
    // still renders its play header and leaderboard (#1214). Returns null only
    // on 404 (missing, not permitted, or not a solo-playable published quiz) so
    // the caller can show the "not available" note; a transient / network
    // failure throws instead, which the caller treats as retryable.
    async getQuizMeta(slugId) {
        const response = await fetch(`/api/quizzes/${slugId}`);
        if (response.status === 404) {
            return null;
        }
        return jsonOrThrow(response);
    }
}

export const quizService = new QuizService();
