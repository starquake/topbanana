package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/rs/xid"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/starquake/topbanana/internal/database"
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

// GetGameByPlayerAndQuiz returns the most-recent game for the given (player,
// quiz) pair, with Questions populated so callers can call IsCompleted once
// they wire the Quiz onto the returned game.
// Returns [game.ErrGameNotFound] if the player has no game for the quiz.
func (s *GameStore) GetGameByPlayerAndQuiz(ctx context.Context, playerID, quizID int64) (*game.Game, error) {
	row, err := s.q.GetGameByPlayerAndQuiz(ctx, db.GetGameByPlayerAndQuizParams{
		PlayerID: playerID,
		QuizID:   quizID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, game.ErrGameNotFound
		}

		return nil, fmt.Errorf("failed to get game by player %d and quiz %d: %w", playerID, quizID, err)
	}

	g := &game.Game{
		ID:        row.ID,
		QuizID:    row.QuizID,
		CreatedAt: row.CreatedAt,
	}

	if row.StartedAt.Valid {
		g.StartedAt = &row.StartedAt.Time
	}

	g.Questions, err = s.listGameQuestions(ctx, g.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list game questions for game %q: %w", g.ID, err)
	}

	return g, nil
}

// CreateGame creates a new game record in the database using the provided game details and updates the game with generated data.
func (s *GameStore) CreateGame(ctx context.Context, g *game.Game) error {
	var err error
	id := xid.New()
	row, err := s.q.CreateGame(ctx, db.CreateGameParams{ID: id.String(), QuizID: g.QuizID})
	if err != nil {
		return fmt.Errorf("failed to create game: %w", err)
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

	if database.MustRowsAffected(res) == 0 {
		return fmt.Errorf("failed to start game with id %q: %w", id, game.ErrStartingGameNoRowsAffected)
	}

	return nil
}

// CreateParticipant adds a new participant to a game and populates the
// participant's ID and joined time fields. The UNIQUE INDEX on
// game_participants (player_id, quiz_id) added in
// 20260520180000_unique_participant_per_player_quiz.sql will surface a
// SQLite UNIQUE constraint failure here when a second concurrent
// Service.CreateGame for the same (player, quiz) loses the race; the
// caller maps it to [game.ErrGameAlreadyExists] (#273).
func (s *GameStore) CreateParticipant(ctx context.Context, p *game.Participant) error {
	row, err := s.q.CreateParticipant(ctx, db.CreateParticipantParams{
		GameID:   p.GameID,
		PlayerID: p.PlayerID,
		QuizID:   p.QuizID,
	})
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return game.ErrGameAlreadyExists
		}

		return fmt.Errorf("failed to create participant: %w", err)
	}

	p.ID = row.ID
	p.JoinedAt = row.JoinedAt

	return nil
}

// CreateGameAndParticipant inserts the games row, the matching
// game_participants row, and stamps started_at all inside a single
// transaction so a crash mid-flow can't leave an orphan games row
// without a participant (#351). On success g.ID / g.CreatedAt and
// p.ID / p.JoinedAt are populated as if the three writes had been
// called individually. The UNIQUE(player_id, quiz_id) constraint
// added by migration 20260520180000 still surfaces as
// [game.ErrGameAlreadyExists] inside the txn so callers don't have
// to special-case the loser of a concurrent insert race.
func (s *GameStore) CreateGameAndParticipant(
	ctx context.Context, g *game.Game, p *game.Participant,
) error {
	err := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
		return execCreateGameAndParticipant(ctx, q, g, p)
	})
	if err != nil {
		// ErrGameAlreadyExists must propagate as the sentinel so
		// callers can errors.Is it directly (the service maps it to
		// the "resume" path); other failures get a wrap for context.
		if errors.Is(err, game.ErrGameAlreadyExists) {
			return game.ErrGameAlreadyExists
		}

		return fmt.Errorf("failed to create game and participant: %w", err)
	}

	return nil
}

// execCreateGameAndParticipant is the body of the
// [GameStore.CreateGameAndParticipant] transaction; pulled out so the
// public method stays under revive's function-length limit and the
// txn flow reads top-to-bottom.
func execCreateGameAndParticipant(
	ctx context.Context, q *db.Queries, g *game.Game, p *game.Participant,
) error {
	id := xid.New()
	gameRow, err := q.CreateGame(ctx, db.CreateGameParams{ID: id.String(), QuizID: g.QuizID})
	if err != nil {
		return fmt.Errorf("create game: %w", err)
	}
	g.ID = gameRow.ID
	g.CreatedAt = gameRow.CreatedAt

	p.GameID = g.ID
	partRow, err := q.CreateParticipant(ctx, db.CreateParticipantParams{
		GameID:   p.GameID,
		PlayerID: p.PlayerID,
		QuizID:   p.QuizID,
	})
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return game.ErrGameAlreadyExists
		}

		return fmt.Errorf("create participant: %w", err)
	}
	p.ID = partRow.ID
	p.JoinedAt = partRow.JoinedAt

	res, err := q.StartGame(ctx, g.ID)
	if err != nil {
		return fmt.Errorf("start game: %w", err)
	}
	if database.MustRowsAffected(res) == 0 {
		return fmt.Errorf("start game with id %q: %w", g.ID, game.ErrStartingGameNoRowsAffected)
	}

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
// The caller supplies a.AnsweredAt — the service clamps the client's tappedAt
// to [question.StartedAt, [time.Now]] before invoking the store (#237) so the
// recorded value is always a Go-passed parameter rather than SQLite's
// CURRENT_TIMESTAMP, which would otherwise reflect commit time rather than
// when the player actually tapped.
//
// Returns [game.ErrAnswerAlreadyRecorded] when the UNIQUE(game_id,
// player_id, game_question_id) constraint trips — a double-tap or
// network retry — so the handler can serve an idempotent response
// instead of a 500 (#353).
func (s *GameStore) CreateAnswer(ctx context.Context, a *game.Answer) error {
	row, err := s.q.CreateAnswer(ctx, db.CreateAnswerParams{
		GameID:         a.GameID,
		PlayerID:       a.PlayerID,
		GameQuestionID: a.QuestionID,
		OptionID:       a.OptionID,
		AnsweredAt:     a.AnsweredAt,
	})
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return game.ErrAnswerAlreadyRecorded
		}

		return fmt.Errorf("failed to create answer: %w", err)
	}

	a.ID = row.ID
	a.AnsweredAt = row.AnsweredAt

	return nil
}

// DeleteGamesForPlayerOnQuiz hard-deletes every game (and its dependent
// participants, questions, and answers) the given player has on the given
// quiz. The four statements run inside a single transaction; rollback on
// any error so a partial reset never leaves orphans behind.
//
// The IDs are gathered up front because the per-statement subqueries we
// would otherwise need to scope each DELETE rely on rows that earlier
// statements have already removed (e.g. the games delete needs participants
// to scope, but participants are gone by then). Snapshotting the IDs once
// sidesteps that ordering puzzle entirely.
//
// No-op if the player has no games for the quiz.
func (s *GameStore) DeleteGamesForPlayerOnQuiz(ctx context.Context, playerID, quizID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	if err = s.deleteGamesForPlayerOnQuizTx(ctx, tx, playerID, quizID); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("delete games failed: %w (rollback error: %w)", err, rbErr)
		}

		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit reset transaction: %w", err)
	}

	return nil
}

// ListAnswersForQuizLeaderboard returns one flat row per game answer across
// every game of the given quiz. The rows carry just enough fields for
// [game.Service.GetQuizLeaderboard] to reuse [game.Service.CalculateScore]
// without re-loading the option / question / player rows individually.
func (s *GameStore) ListAnswersForQuizLeaderboard(
	ctx context.Context, quizID int64,
) ([]*game.LeaderboardAnswer, error) {
	rows, err := s.q.ListAnswersForQuizLeaderboard(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to list leaderboard answers for quiz %d: %w", quizID, err)
	}

	answers := make([]*game.LeaderboardAnswer, 0, len(rows))
	for _, r := range rows {
		answers = append(answers, &game.LeaderboardAnswer{
			PlayerID:          r.PlayerID,
			Username:          r.Username,
			QuestionStartedAt: r.QuestionStartedAt,
			QuestionExpiredAt: r.QuestionExpiredAt,
			AnsweredAt:        r.AnsweredAt,
			Correct:           r.IsCorrect,
			// is_completed is a SQLite CASE expression that comes back
			// as 1/0; treat anything non-zero as "this row belongs to a
			// game that has issued every quiz question".
			IsCompleted: r.IsCompleted != 0,
		})
	}

	return answers, nil
}

// ListParticipantsForQuizLeaderboard returns one row per player joined
// to the quiz, flagged with IsCompleted and IsStale (#336). Pass
// [time.Now]-stalePeriod for staleBefore. Canonical entry set per #335.
func (s *GameStore) ListParticipantsForQuizLeaderboard(
	ctx context.Context, quizID int64, staleBefore time.Time,
) ([]*game.LeaderboardParticipant, error) {
	rows, err := s.q.ListParticipantsForQuizLeaderboard(ctx, db.ListParticipantsForQuizLeaderboardParams{
		ExpiredAt: staleBefore,
		QuizID:    quizID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list leaderboard participants for quiz %d: %w", quizID, err)
	}

	participants := make([]*game.LeaderboardParticipant, 0, len(rows))
	for _, r := range rows {
		participants = append(participants, &game.LeaderboardParticipant{
			PlayerID: r.PlayerID,
			Username: r.Username,
			// CASE returns 1/0.
			IsCompleted: r.IsCompleted != 0,
			IsStale:     r.IsStale != 0,
		})
	}

	return participants, nil
}

// ListQuizIDsForPlayer returns the distinct quiz IDs the player has
// joined. Used by the claim-name flow to repaint every affected
// leaderboard SSE stream when a player changes their display name.
//
// Reads from game_participants so post-#335 joined-but-unanswered
// players also get their leaderboard repainted on rename (#354).
// quiz_id is NOT NULL since migration 20260524200000 (#357), so the
// generated layer returns plain []int64.
func (s *GameStore) ListQuizIDsForPlayer(ctx context.Context, playerID int64) ([]int64, error) {
	ids, err := s.q.ListQuizIDsForPlayer(ctx, playerID)
	if err != nil {
		return nil, fmt.Errorf("failed to list quiz IDs for player %d: %w", playerID, err)
	}

	return ids, nil
}

func (s *GameStore) deleteGamesForPlayerOnQuizTx(
	ctx context.Context, tx *sql.Tx, playerID, quizID int64,
) error {
	q := s.q.WithTx(tx)

	gameIDs, err := q.ListGameIDsForPlayerOnQuiz(ctx, db.ListGameIDsForPlayerOnQuizParams{
		PlayerID: playerID,
		QuizID:   quizID,
	})
	if err != nil {
		return fmt.Errorf("failed to list game IDs for player %d on quiz %d: %w", playerID, quizID, err)
	}

	if len(gameIDs) == 0 {
		return nil
	}

	if err = q.DeleteGameAnswersByGameIDs(ctx, gameIDs); err != nil {
		return fmt.Errorf("failed to delete answers: %w", err)
	}
	if err = q.DeleteGameQuestionsByGameIDs(ctx, gameIDs); err != nil {
		return fmt.Errorf("failed to delete questions: %w", err)
	}
	if err = q.DeleteGameParticipantsByGameIDs(ctx, gameIDs); err != nil {
		return fmt.Errorf("failed to delete participants: %w", err)
	}
	if err = q.DeleteGamesByIDs(ctx, gameIDs); err != nil {
		return fmt.Errorf("failed to delete games: %w", err)
	}

	return nil
}

func (s *GameStore) listGameQuestions(ctx context.Context, gameID string) ([]*game.Question, error) {
	rows, err := s.q.ListGameQuestionsByGameID(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("failed to list game questions for game %q: %w", gameID, err)
	}

	// One shot for every answer in the game, partitioned in Go below
	// (#356). The old per-question fetch was an N+1 against
	// game_answers and forced a full-table scan per call until the
	// game_question_id index landed in the same bundle.
	answerRows, err := s.q.ListAnswersByGameID(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("failed to list answers for game %q: %w", gameID, err)
	}
	answersByGQ := make(map[int64][]*game.Answer, len(rows))
	for _, r := range answerRows {
		answersByGQ[r.GameQuestionID] = append(answersByGQ[r.GameQuestionID], &game.Answer{
			ID:         r.ID,
			GameID:     r.GameID,
			PlayerID:   r.PlayerID,
			QuestionID: r.GameQuestionID,
			OptionID:   r.OptionID,
			AnsweredAt: r.AnsweredAt,
		})
	}

	gameQuestions := make([]*game.Question, 0, len(rows))
	for _, r := range rows {
		gameQuestions = append(gameQuestions, &game.Question{
			ID:         r.ID,
			GameID:     r.GameID,
			QuestionID: r.QuestionID,
			StartedAt:  r.StartedAt,
			ExpiredAt:  r.ExpiredAt,
			Answers:    answersByGQ[r.ID],
		})
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
		// quiz_id became NOT NULL in 20260524200000 (#357), so the
		// generated row carries it as int64 — no more Valid-guard.
		participants = append(participants, &game.Participant{
			ID:       r.ID,
			GameID:   r.GameID,
			PlayerID: r.PlayerID,
			QuizID:   r.QuizID,
			JoinedAt: r.JoinedAt,
		})
	}

	return participants, nil
}
