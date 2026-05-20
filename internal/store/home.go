package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/starquake/topbanana/internal/db"
	"github.com/starquake/topbanana/internal/home"
)

// HomeStore is the data-access layer for the public start page. It wraps
// the sqlc-generated queries that aggregate plays and finishers across
// the games + quizzes + players tables.
type HomeStore struct {
	q      *db.Queries
	logger *slog.Logger
}

// NewHomeStore wires a HomeStore against the supplied database
// connection. The logger is held for future error annotation; the
// current methods return wrapped errors directly so the caller logs
// once at the handler layer.
func NewHomeStore(conn *sql.DB, logger *slog.Logger) *HomeStore {
	return &HomeStore{q: db.New(conn), logger: logger}
}

// ListPopularQuizzes returns the top-ranked quizzes by play count in the
// last 30 days. The underlying query orders by play_count DESC; the
// caller slices to the desired top-N.
func (s *HomeStore) ListPopularQuizzes(ctx context.Context) ([]*home.PopularQuiz, error) {
	rows, err := s.q.ListPopularQuizzes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list popular quizzes: %w", err)
	}

	out := make([]*home.PopularQuiz, 0, len(rows))
	for _, r := range rows {
		out = append(out, &home.PopularQuiz{
			ID:          r.ID,
			Title:       r.Title,
			Slug:        r.Slug,
			Description: r.Description,
			PlayCount:   int(r.PlayCount),
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
			Username:      r.Username,
			FinishedCount: int(r.FinishedCount),
		})
	}

	return out, nil
}
