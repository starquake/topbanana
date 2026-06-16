package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/starquake/topbanana/internal/db"
	"github.com/starquake/topbanana/internal/home"
)

// HomeStore is the data-access layer for the public start page. It wraps
// the sqlc-generated queries that aggregate plays and finishers across
// the games + quizzes + players tables.
type HomeStore struct {
	q *db.Queries
}

// NewHomeStore wires a HomeStore against the supplied database connection.
func NewHomeStore(conn *sql.DB) *HomeStore {
	return &HomeStore{q: db.New(conn)}
}

// ListPopularQuizzes returns the top-ranked quizzes by recent play
// activity. The underlying query ranks by the 30-day finished-game count
// (recent_play_count DESC) but each row carries the durable lifetime
// play_count the card displays (#891/#927); the caller slices to top-N.
func (s *HomeStore) ListPopularQuizzes(ctx context.Context) ([]*home.PopularQuiz, error) {
	rows, err := s.q.ListPopularQuizzes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list popular quizzes: %w", err)
	}

	out := make([]*home.PopularQuiz, 0, len(rows))
	for _, r := range rows {
		out = append(out, &home.PopularQuiz{
			ID:                   r.ID,
			Title:                r.Title,
			Slug:                 r.Slug,
			Description:          r.Description,
			CreatedAt:            r.CreatedAt,
			CreatedByDisplayName: r.CreatedByDisplayName,
			PlayCount:            int(r.PlayCount),
			RoundCount:           int(r.RoundCount),
			QuestionCount:        int(r.QuestionCount),
		})
	}

	return out, nil
}

// ListNewestQuizzes returns the most-recently-created public quizzes,
// newest first. The underlying query orders by created_at DESC; the
// caller slices to the desired top-N.
func (s *HomeStore) ListNewestQuizzes(ctx context.Context) ([]*home.NewestQuiz, error) {
	rows, err := s.q.ListNewestQuizzes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list newest quizzes: %w", err)
	}

	out := make([]*home.NewestQuiz, 0, len(rows))
	for _, r := range rows {
		out = append(out, &home.NewestQuiz{
			ID:                   r.ID,
			Title:                r.Title,
			Slug:                 r.Slug,
			Description:          r.Description,
			CreatedAt:            r.CreatedAt,
			CreatedByDisplayName: r.CreatedByDisplayName,
			PlayCount:            int(r.PlayCount),
			RoundCount:           int(r.RoundCount),
			QuestionCount:        int(r.QuestionCount),
		})
	}

	return out, nil
}

// ListMostActivePlayers returns the players ranked by number of finished
// games, descending. Anonymous (unclaimed petname) players are filtered
// out in SQL so this list only shows display names a player explicitly
// picked.
func (s *HomeStore) ListMostActivePlayers(ctx context.Context) ([]*home.ActivePlayer, error) {
	rows, err := s.q.ListMostActivePlayers(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list most active players: %w", err)
	}

	out := make([]*home.ActivePlayer, 0, len(rows))
	for _, r := range rows {
		out = append(out, &home.ActivePlayer{
			ID:            r.ID,
			DisplayName:   r.DisplayName,
			FinishedCount: int(r.FinishedCount),
		})
	}

	return out, nil
}
