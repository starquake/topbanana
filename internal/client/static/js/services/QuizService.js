export class QuizService {
    async getQuizzes() {
        const response = await fetch('/api/quizzes');
        return await response.json();
    }
}

export const quizService = new QuizService();
