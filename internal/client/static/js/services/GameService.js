import { jsonOrThrow } from './api.js';

// GameService wraps the gameplay REST endpoints. Every method throws
// [ApiError] on non-2xx (#287) so callers can branch on status
// instead of falling into a JSON-parse SyntaxError. The two methods
// where 404 has a defined "absent" meaning (next question, my-game)
// short-circuit before jsonOrThrow so callers keep the existing
// null return signal.
export class GameService {
    async startGame(quizId) {
        const response = await fetch('/api/games', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ quizId: parseInt(quizId) })
        });
        return jsonOrThrow(response);
    }

    async getNextQuestion(gameId) {
        const response = await fetch(`/api/games/${gameId}/questions/next`);
        if (response.status === 404) {
            return null;
        }
        return jsonOrThrow(response);
    }

    async getMyGameForQuiz(slugId) {
        const response = await fetch(`/api/quizzes/${slugId}/my-game`);
        if (response.status === 404) {
            return null;
        }
        return jsonOrThrow(response);
    }

    // tappedAt is captured by the caller at click time and forwarded
    // here so the server can refund the network-latency portion of
    // AnsweredAt instead of stamping commit time (#237). ISO-8601 so
    // the server's time.Time JSON decoder accepts it directly; the
    // service-side clamp re-validates the value either way.
    async submitAnswer(gameId, questionId, optionId, tappedAt) {
        const response = await fetch(`/api/games/${gameId}/questions/${questionId}/answers`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ optionId: optionId, tappedAt: tappedAt })
        });
        return jsonOrThrow(response);
    }

    async getResults(gameId) {
        const response = await fetch(`/api/games/${gameId}/results`);
        return jsonOrThrow(response);
    }

    async getQuizLeaderboard(slugId) {
        const response = await fetch(`/api/quizzes/${slugId}/leaderboard`);
        return jsonOrThrow(response);
    }
}

export const gameService = new GameService();
