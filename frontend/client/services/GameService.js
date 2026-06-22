import { ApiError, jsonOrThrow } from './api.js';

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

    // getAudioManifest returns the audio-preload manifest for a solo game: the
    // audio-bearing questions in play order, each { questionId, audioUrl,
    // audioRepeat } (#1088). The engine preloads every clip at game start so
    // each question plays an already-decoded Howl with no per-question decode
    // race. Returns { clips: [] } for an audio-free quiz.
    async getAudioManifest(gameId) {
        const response = await fetch(`/api/games/${gameId}/audio`);
        return jsonOrThrow(response);
    }

    // markRoundSeen acknowledges one phase of a round boundary (#548):
    // `phase` is 'intro' (before the round's first question) or
    // 'results' (after the round's questions). The server returns 204
    // No Content; any other status is a real error the caller surfaces
    // as a retry banner — silently dropping the click would strand the
    // player on the round card with no recovery. Idempotent at the
    // server, so a retry after a transient failure is safe.
    async markRoundSeen(gameId, roundId, phase) {
        const response = await fetch(`/api/games/${gameId}/rounds/${roundId}/seen/${phase}`, {
            method: 'POST',
        });
        if (response.ok) return;
        let body = '';
        try {
            body = await response.text();
        } catch {
            // status is the load-bearing field; empty body is fine.
        }
        throw new ApiError(`HTTP ${response.status}: ${body.slice(0, 200)}`, response.status, body);
    }

    async getQuizLeaderboard(slugId) {
        const response = await fetch(`/api/quizzes/${slugId}/leaderboard`);
        return jsonOrThrow(response);
    }
}

export const gameService = new GameService();
