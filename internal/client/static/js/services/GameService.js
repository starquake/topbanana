export class GameService {
    async startGame(quizId) {
        const response = await fetch('/api/games', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ quizId: parseInt(quizId) })
        });
        return await response.json();
    }

    async getNextQuestion(gameId) {
        const response = await fetch(`/api/games/${gameId}/questions/next`);
        if (response.status === 404) {
            return null;
        }
        return await response.json();
    }

    async submitAnswer(gameId, questionId, optionId) {
        const response = await fetch(`/api/games/${gameId}/questions/${questionId}/answers`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ optionId: optionId })
        });
        return await response.json();
    }

    async getResults(gameId) {
        const response = await fetch(`/api/games/${gameId}/results`);
        return await response.json();
    }
}

export const gameService = new GameService();
