# Game logic

The player #123 has decided to play quiz #1. Selects it from the list and starts playing.

There currently is only one type of game: single player. The Game exists because later there will be multiplayer games. 
And so while we are waiting for players to join, the game state will be waiting. And when the game starts, it will be started. But for now, this all will not be implemented.
For singleplayer, the game will start automatically after creation.

## Games sequence diagram (Single player)
```mermaid

sequenceDiagram
    participant Player
    participant API
    participant GameService
    participant Store

    Note over Player, Store: Start a New Game
    Player->>API: POST /api/games {quizId: 1}
    API->>GameService: CreateGame(quizID, player123)
    GameService->>Store: GetQuiz(1)
    GameService->>Store: CreateGame(quizID)
    GameService->>Store: CreateParticipant(gameID, player123)
    GameService->>Store: StartGame(gameID)
    GameService-->>API: *game.Game
    API-->>Player: {gameId, startedAt}

    loop Every question
        Note over Player, Store: Progressing the Quiz
        Player->>API: GET /api/games/{id}/questions/next
        API->>GameService: GetNextQuestion(gameID)
        GameService->>Store: GetGame(gameID)
        GameService->>Store: GetQuiz(quizID)
        Note right of GameService: Logic: Identify next unanswered question
        GameService->>Store: CreateGameQuestion(gameID, questionID)
        GameService-->>API: *quiz.Question
        API-->>Player: {question, expiresAt}

        Note over Player, Store: Answering
        Player->>API: POST /api/games/{id}/answers {optionId: 3}
        API->>GameService: SubmitAnswer(gameID, player123, optionId)
        GameService->>Store: CreateAnswer(...)
        GameService->>Store: GetQuestion(questionID)
        Note right of GameService: Logic: Verify correctness & calculate points
        GameService-->>API: {correct: true, points: 1200}
        API-->>Player: {correct: true, points: 1200}
    end

    Note over Player, Store: Results
    Player->>API: GET /api/games/{id}/results
    API->>GameService: GetResults(gameID)
    GameService->>Store: GetGame(gameID)
    Note right of GameService: Logic: Aggregate score
    GameService-->>API: {score: 9000}
    API-->>Player: {score: 9000}

```
