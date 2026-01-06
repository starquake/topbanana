// Package game contains the game domain logic.
package game

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/starquake/topbanana/internal/quiz"
)

const defaultExpiration = 10 * time.Second

// ErrGameNotFound is returned when a game is not found.
var (
	ErrGameNotFound               = errors.New("game not found")
	ErrPlayerNotFound             = errors.New("player not found")
	ErrNoMoreQuestions            = errors.New("no more questions")
	ErrStartingGameNoRowsAffected = errors.New("no rows affected when starting game")
)

// Game represents a game.
type Game struct {
	ID            string
	QuizID        int64
	Quiz          *quiz.Quiz
	CreatedAt     time.Time
	StartedAt     *time.Time
	GameQuestions []*Question
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

// Question represents a question in a game.
type Question struct {
	ID         int64
	GameID     string
	QuestionID int64
	StartedAt  time.Time
	ExpiredAt  time.Time
	Answers    []*Answer
}

// Answer represents an answer for a question.
type Answer struct {
	ID             int64
	GameID         string
	PlayerID       int64
	GameQuestionID int64
	OptionID       int64
	AnsweredAt     time.Time
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
	CreateGameQuestion(ctx context.Context, gq *Question) error
}

// Service represents a game service.
type Service struct {
	store     Store
	quizStore quiz.Store
}

// NewService initializes and returns a new instance of Service with the provided game and quiz stores.
func NewService(gameStore Store, quizStore quiz.Store) *Service {
	return &Service{
		store:     gameStore,
		quizStore: quizStore,
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
func (s *Service) GetNextQuestion(ctx context.Context, gameID string) (*quiz.Question, error) {
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
	for _, gqs := range g.GameQuestions {
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

	// If we found a question, register it as a GameQuestion (starting the timer)
	if nextQuestion != nil {
		gq := &Question{
			GameID:     gameID,
			QuestionID: nextQuestion.ID,
			StartedAt:  time.Now(),
			ExpiredAt:  time.Now().Add(defaultExpiration), // 10s limit
		}
		if err = s.store.CreateGameQuestion(ctx, gq); err != nil {
			return nil, fmt.Errorf("failed to record game question: %w", err)
		}
	}

	if nextQuestion == nil {
		return nil, ErrNoMoreQuestions
	}

	return nextQuestion, nil
}
