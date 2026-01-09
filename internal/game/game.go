// Package game contains the game domain logic.
package game

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/starquake/topbanana/internal/quiz"
)

const (
	defaultExpiration = 10 * time.Second
	maxPoints         = 1000
)

// ErrGameNotFound is returned when a game is not found.
var (
	ErrGameNotFound               = errors.New("game not found")
	ErrNoMoreQuestions            = errors.New("no more questions")
	ErrQuestionNotInGame          = errors.New("question not in game")
	ErrStartingGameNoRowsAffected = errors.New("no rows affected when starting game")
)

// Game represents a game. It is an instance of a quiz being played by a player.
type Game struct {
	ID           string
	QuizID       int64
	Quiz         *quiz.Quiz
	CreatedAt    time.Time
	StartedAt    *time.Time
	Questions    []*Question
	Participants []*Participant
}

// Player represents a player.
type Player struct {
	ID        int64
	Username  string
	Email     string
	CreatedAt time.Time
}

// Participant represents a player participating in a game.
type Participant struct {
	ID       int64
	GameID   string
	PlayerID int64
	JoinedAt time.Time
}

// Question represents a question in a game. It references a quiz question.
type Question struct {
	ID           int64
	GameID       string
	QuestionID   int64
	QuizQuestion *quiz.Question
	StartedAt    time.Time
	// TODO: change this to time duration like 10s instead of timestamp?
	ExpiredAt time.Time
	Answers   []*Answer
}

// Answer represents an answer for a question. Answers are recorded for a specific game and player.
type Answer struct {
	ID         int64
	GameID     string
	PlayerID   int64
	QuestionID int64
	Question   *Question
	OptionID   int64
	Option     *quiz.Option
	AnsweredAt time.Time
}

// Results represents the accumulated score for each player in a game.
type Results struct {
	GameID string

	// PlayerScores maps a player's ID to their accumulated CalculateScore in the game.
	PlayerScores map[int64]int
}

// Store represents a game store.
type Store interface {
	// Ping returns the status of the database connection.
	Ping(ctx context.Context) error
	GetGame(ctx context.Context, id string) (*Game, error)
	// CreateGame creates a new game.
	CreateGame(ctx context.Context, g *Game) error
	StartGame(ctx context.Context, id string) error
	CreateParticipant(ctx context.Context, p *Participant) error
	CreateQuestion(ctx context.Context, gq *Question) error
	CreateAnswer(ctx context.Context, a *Answer) error
}

// Service represents a game service.
type Service struct {
	store     Store
	quizStore quiz.Store
	logger    *slog.Logger
}

// NewService initializes and returns a new instance of Service with the provided game and quiz stores.
func NewService(gameStore Store, quizStore quiz.Store, logger *slog.Logger) *Service {
	return &Service{
		store:     gameStore,
		quizStore: quizStore,
		logger:    logger,
	}
}

// CreateGame creates a new game with the specified quiz and player, linking the player and starting the game immediately.
// Returns the newly created game or an error if the operation fails.
func (s *Service) CreateGame(ctx context.Context, quizID, playerID int64) (*Game, error) {
	var err error
	// verify that the quiz exists
	qz, err := s.quizStore.GetQuiz(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to get quiz: %w", err)
	}

	// Create the game record
	g := &Game{QuizID: qz.ID}
	if err = s.store.CreateGame(ctx, g); err != nil {
		return nil, fmt.Errorf("failed to create game: %w", err)
	}

	g.Quiz = qz

	// Add the player to the game
	pa := &Participant{GameID: g.ID, PlayerID: playerID}
	if err = s.store.CreateParticipant(ctx, pa); err != nil {
		return nil, fmt.Errorf("failed to create participant: %w", err)
	}

	// Start the game (Single player game starts immediately)
	if err = s.store.StartGame(ctx, g.ID); err != nil {
		return nil, fmt.Errorf("failed to start game: %w", err)
	}

	return g, nil
}

// GetNextQuestion retrieves the next unanswered question for the specified game or returns nil if all are answered.
func (s *Service) GetNextQuestion(ctx context.Context, gameID string) (*Question, error) {
	// Get the game
	g, err := s.store.GetGame(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("failed to get game: %w", err)
	}

	// Get the quiz
	qz, err := s.quizStore.GetQuiz(ctx, g.QuizID)
	if err != nil {
		return nil, fmt.Errorf("failed to get quiz: %w", err)
	}

	g.Quiz = qz

	// Create a lookup map for questions already asked in this game
	askedQuestions := make(map[int64]bool)
	for _, gqs := range g.Questions {
		askedQuestions[gqs.QuestionID] = true
	}

	var nextQuestion *quiz.Question
	// Find the first question in the quiz that hasn't been asked yet
	for _, q := range qz.Questions {
		if !askedQuestions[q.ID] {
			nextQuestion = q

			break
		}
	}

	var gq *Question
	// If we found a quiz question, register it as a GameQuestion (starting the timer)
	if nextQuestion != nil {
		gq = &Question{
			GameID:       gameID,
			QuestionID:   nextQuestion.ID,
			QuizQuestion: nextQuestion,
			StartedAt:    time.Now(),
			ExpiredAt:    time.Now().Add(defaultExpiration), // 10s limit
		}
		if err = s.store.CreateQuestion(ctx, gq); err != nil {
			return nil, fmt.Errorf("failed to record game question: %w", err)
		}
	}

	if nextQuestion == nil {
		return nil, ErrNoMoreQuestions
	}

	return gq, nil
}

// SubmitAnswer records an answer from a player for a specific question in a game.
// It validates that the game exists and the question belongs to the game before saving the answer.
// Returns the saved answer or nil if the question was not found in the game.
// Returns an error if the operation fails.
func (s *Service) SubmitAnswer(
	ctx context.Context,
	gameID string,
	playerID, questionID, optionID int64,
) (*Answer, error) {
	var err error

	g, err := s.store.GetGame(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("failed to get game: %w", err)
	}

	var question *Question
	for _, qs := range g.Questions {
		if qs.QuestionID == questionID {
			question = qs

			break
		}
	}

	if question == nil {
		return nil, fmt.Errorf("question %d not found in game %s: %w", questionID, gameID, ErrQuestionNotInGame)
	}

	a := &Answer{
		GameID:     gameID,
		PlayerID:   playerID,
		QuestionID: question.ID,
		Question:   question,
		OptionID:   optionID,
	}

	if err = s.store.CreateAnswer(ctx, a); err != nil {
		return nil, fmt.Errorf("failed to create answer: %w", err)
	}
	a.Option, err = s.quizStore.GetOption(ctx, optionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get option: %w", err)
	}

	return a, nil
}

// GetResults calculates the accumulated score for each player in a game and returns the results.
func (s *Service) GetResults(ctx context.Context, gameID string) (*Results, error) {
	var err error
	g, err := s.store.GetGame(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("failed to get game: %w", err)
	}

	plsMap := make(map[int64]int, len(g.Participants))

	for _, gqs := range g.Questions {
		for _, ga := range gqs.Answers {
			ga.Question = gqs
			ga.Option, err = s.quizStore.GetOption(ctx, ga.OptionID)
			if err != nil {
				return nil, fmt.Errorf("failed to get option: %w", err)
			}

			plsMap[ga.PlayerID] += s.CalculateScore(ctx, ga)
		}
	}

	r := &Results{GameID: g.ID, PlayerScores: plsMap}

	return r, nil
}

// CalculateScore calculates the score for a given answer.
func (s *Service) CalculateScore(ctx context.Context, a *Answer) int {
	// TODO: Should this be the points for answering immediately? Or within one second?

	if !a.Option.Correct {
		return 0
	}

	if a.AnsweredAt.After(a.Question.ExpiredAt) {
		s.logger.InfoContext(ctx, "score=0, a.AnsweredAt > question.ExpiredAt, answered too late!")

		return 0
	}

	answerWindow := a.Question.ExpiredAt.Sub(a.Question.StartedAt)
	duration := a.AnsweredAt.Sub(a.Question.StartedAt)

	score := int(float64(maxPoints) - (duration.Seconds() / answerWindow.Seconds() * float64(maxPoints)))

	return score
}
