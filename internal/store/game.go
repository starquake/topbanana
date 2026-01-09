package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/rs/xid"

	"github.com/starquake/topbanana/internal/db"
	"github.com/starquake/topbanana/internal/game"
)

// GameStore provides methods for managing game-related data in a database, including queries and transactions.
type GameStore struct {
	q      *db.Queries
	db     *sql.DB
	logger *slog.Logger
}

// NewGameStore initializes and returns a GameStore instance with the provided database connection and logger.
func NewGameStore(conn *sql.DB, logger *slog.Logger) *GameStore {
	return &GameStore{q: db.New(conn), db: conn, logger: logger}
}

// Ping verifies the connection to the database, returning an error if the ping operation fails.
func (s *GameStore) Ping(ctx context.Context) error {
	err := s.db.PingContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	return nil
}

// GetGame retrieves a game by its ID from the database, returning the game details or an error if not found or failed.
// Returns game.ErrGameNotFound if the game is not found.
func (s *GameStore) GetGame(ctx context.Context, id string) (*game.Game, error) {
	var err error
	row, err := s.q.GetGame(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, game.ErrGameNotFound
		}

		return nil, fmt.Errorf("failed to get game: %w", err)
	}

	g := &game.Game{
		ID:        row.ID,
		QuizID:    row.QuizID,
		CreatedAt: row.CreatedAt,
	}

	if row.StartedAt.Valid {
		g.StartedAt = &row.StartedAt.Time
	}

	g.Questions, err = s.listGameQuestions(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to list game questions for game %q: %w", id, err)
	}

	g.Participants, err = s.listParticipants(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to list participants for game %q: %w", id, err)
	}

	return g, nil
}

// CreateGame creates a new game record in the database using the provided game details and updates the game with generated data.
func (s *GameStore) CreateGame(ctx context.Context, g *game.Game) error {
	var err error
	id := xid.New()
	row, err := s.q.CreateGame(ctx, db.CreateGameParams{ID: id.String(), QuizID: g.QuizID})
	if err != nil {
		return fmt.Errorf("failed to create g: %w", err)
	}

	g.ID = row.ID
	g.CreatedAt = row.CreatedAt

	return nil
}

// StartGame starts a game with the given ID, updating its status in the database, and returns an error if the operation fails.
// Returns game.ErrStartingGameNoRowsAffected if no rows were affected by the operation.
func (s *GameStore) StartGame(ctx context.Context, id string) error {
	res, err := s.q.StartGame(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to start game: %w", err)
	}

	if mustRowsAffected(res) == 0 {
		return fmt.Errorf("failed to start game with id %q: %w", id, game.ErrStartingGameNoRowsAffected)
	}

	return nil
}

// CreateParticipant adds a new participant to a game and populates the participant's ID and joined time fields.
func (s *GameStore) CreateParticipant(ctx context.Context, p *game.Participant) error {
	var err error
	row, err := s.q.CreateParticipant(ctx, db.CreateParticipantParams{GameID: p.GameID, PlayerID: p.PlayerID})
	if err != nil {
		return fmt.Errorf("failed to create participant: %w", err)
	}

	p.ID = row.ID
	p.JoinedAt = row.JoinedAt

	return nil
}

// CreateQuestion saves a new game question in the database and updates the provided Question object with generated values.
func (s *GameStore) CreateQuestion(ctx context.Context, gq *game.Question) error {
	var err error
	row, err := s.q.CreateGameQuestion(
		ctx,
		db.CreateGameQuestionParams{
			GameID:     gq.GameID,
			QuestionID: gq.QuestionID,
			StartedAt:  gq.StartedAt,
			ExpiredAt:  gq.ExpiredAt,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create game question: %w", err)
	}

	gq.ID = row.ID
	gq.StartedAt = row.StartedAt
	gq.ExpiredAt = row.ExpiredAt

	return nil
}

// CreateAnswer saves a new answer in the database and updates the provided Answer object with generated values.
func (s *GameStore) CreateAnswer(ctx context.Context, a *game.Answer) error {
	var err error
	row, err := s.q.CreateAnswer(ctx, db.CreateAnswerParams{
		GameID: a.GameID, PlayerID: a.PlayerID, GameQuestionID: a.GameQuestionID, OptionID: a.OptionID,
	})
	if err != nil {
		return fmt.Errorf("failed to create answer: %w", err)
	}

	a.ID = row.ID
	a.AnsweredAt = row.AnsweredAt

	return nil
}

func (s *GameStore) listGameQuestions(ctx context.Context, gameID string) ([]*game.Question, error) {
	var err error
	rows, err := s.q.ListGameQuestionsByGameID(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("failed to list game questions for game %q: %w", gameID, err)
	}

	gameQuestions := make([]*game.Question, 0, len(rows))
	for _, r := range rows {
		gqs := &game.Question{
			ID:         r.ID,
			GameID:     r.GameID,
			QuestionID: r.QuestionID,
			StartedAt:  r.StartedAt,
			ExpiredAt:  r.ExpiredAt,
		}

		gqs.Answers, err = s.listAnswers(ctx, gqs.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to list answers for game question: %w", err)
		}

		gameQuestions = append(gameQuestions, gqs)
	}

	return gameQuestions, nil
}

func (s *GameStore) listParticipants(ctx context.Context, gameID string) ([]*game.Participant, error) {
	var err error
	rows, err := s.q.ListParticipantsByGameID(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("failed to list participants for game %q: %w", gameID, err)
	}

	participants := make([]*game.Participant, 0, len(rows))
	for _, r := range rows {
		participants = append(participants, &game.Participant{
			ID:       r.ID,
			GameID:   r.GameID,
			PlayerID: r.PlayerID,
			JoinedAt: r.JoinedAt,
		})
	}

	return participants, nil
}

func (s *GameStore) listAnswers(ctx context.Context, gameQuestionID int64) ([]*game.Answer, error) {
	var err error
	rows, err := s.q.ListAnswersByGameQuestionID(ctx, gameQuestionID)
	if err != nil {
		return nil, fmt.Errorf("failed to list answers for game question: %w", err)
	}
	answers := make([]*game.Answer, 0, len(rows))
	for _, r := range rows {
		a := &game.Answer{
			ID:             r.ID,
			GameID:         r.GameID,
			PlayerID:       r.PlayerID,
			GameQuestionID: r.GameQuestionID,
			OptionID:       r.OptionID,
			AnsweredAt:     r.AnsweredAt,
		}

		answers = append(answers, a)
	}

	return answers, nil
}
