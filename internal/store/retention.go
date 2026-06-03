package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"slices"

	"github.com/starquake/topbanana/internal/database"
	"github.com/starquake/topbanana/internal/db"
)

// retentionBatchSize caps how many ids each batched DELETE binds at once.
// modernc/sqlite's SQLITE_MAX_VARIABLE_NUMBER is 32766; on the first sweep
// against a backlog the snapshot id list can exceed that and a single
// `IN (...)` would fail with "too many SQL variables". 1000 stays well under
// the limit and keeps each per-batch write-lock short.
const retentionBatchSize = 1000

// RetentionStore runs the periodic data-retention sweeps: it prunes stale
// anonymous players together with all their game data (#626) and abandoned,
// never-finished games regardless of player (#627). Both sweeps compute their
// cutoff in SQL (datetime('now','-N days')) so they take no time argument.
type RetentionStore struct {
	q      *db.Queries
	db     *sql.DB
	logger *slog.Logger
}

// NewRetentionStore initializes a new RetentionStore with the provided
// database connection and returns it.
func NewRetentionStore(conn *sql.DB, logger *slog.Logger) *RetentionStore {
	return &RetentionStore{q: db.New(conn), db: conn, logger: logger}
}

// SweepStaleAnonymousPlayers hard-deletes anonymous players minted more than
// 90 days ago and every game row that references them (#626). The dependent
// game_* rows are dropped in foreign-key order before the player rows;
// game_seen_rounds cascades from games on delete, so it needs no explicit
// pass. Guests holding a finished game are excluded by
// ListStaleAnonymousPlayerIDs and kept regardless of age, so the sweep
// never erases a leaderboard score; only finished-game-free cruft is pruned.
//
// Work is committed in batches (one transaction per player chunk) rather than
// a single mega-transaction: the SQLite write-lock is released between
// batches so concurrent writers are not stalled, and partial progress
// survives a transient failure. That is acceptable for a background sweep --
// the next pass picks up whatever the failed batch left behind. Within a
// batch the FK-ordered delete (answers -> questions -> participants -> games,
// then players) leaves no dangling children.
func (s *RetentionStore) SweepStaleAnonymousPlayers(ctx context.Context) error {
	playerIDs, err := s.q.ListStaleAnonymousPlayerIDs(ctx)
	if err != nil {
		return fmt.Errorf("failed to list stale anonymous players: %w", err)
	}

	for playerBatch := range slices.Chunk(playerIDs, retentionBatchSize) {
		if err := s.sweepPlayerBatch(ctx, playerBatch); err != nil {
			return err
		}
	}

	return nil
}

// SweepAbandonedGames hard-deletes games created more than 30 days ago that
// never finished, along with their dependent game_* rows (#627). It does not
// touch players. "Finished" means every question of the game's quiz has been
// issued; see ListAbandonedGameIDs for the predicate.
//
// The game ids are snapshotted once, then deleted in batches with one
// transaction per chunk so the write-lock is released between batches and
// partial progress survives a transient failure -- acceptable for a
// background sweep, which simply re-snapshots on the next pass.
func (s *RetentionStore) SweepAbandonedGames(ctx context.Context) error {
	gameIDs, err := s.q.ListAbandonedGameIDs(ctx)
	if err != nil {
		return fmt.Errorf("failed to list abandoned games: %w", err)
	}

	for gameBatch := range slices.Chunk(gameIDs, retentionBatchSize) {
		err := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
			return deleteGamesByIDs(ctx, q, gameBatch)
		})
		if err != nil {
			return fmt.Errorf("failed to sweep abandoned games: %w", err)
		}
	}

	return nil
}

// sweepPlayerBatch deletes one chunk of anonymous players and all their game
// data inside a single transaction. The player rows go last so every game_*
// row that references them is already gone; a batch may reference more than
// retentionBatchSize games, so the game-id deletes are chunked too.
func (s *RetentionStore) sweepPlayerBatch(ctx context.Context, playerIDs []int64) error {
	err := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
		gameIDs, err := q.ListGameIDsForPlayers(ctx, playerIDs)
		if err != nil {
			return fmt.Errorf("failed to list games for stale anonymous players: %w", err)
		}
		for gameBatch := range slices.Chunk(gameIDs, retentionBatchSize) {
			if err := deleteGamesByIDs(ctx, q, gameBatch); err != nil {
				return err
			}
		}

		if err := q.DeletePlayersByIDs(ctx, playerIDs); err != nil {
			return fmt.Errorf("failed to delete stale anonymous players: %w", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to sweep stale anonymous players: %w", err)
	}

	return nil
}

// deleteGamesByIDs drops every game_* row attached to the given game IDs in
// foreign-key order, then the games themselves. game_seen_rounds references
// games(id) ON DELETE CASCADE, so it is removed implicitly when the games go.
// Shared by both retention sweeps and a no-op on an empty slice. Callers chunk
// the id slice to at most retentionBatchSize before calling.
func deleteGamesByIDs(ctx context.Context, q *db.Queries, gameIDs []string) error {
	if len(gameIDs) == 0 {
		return nil
	}
	if err := q.DeleteGameAnswersByGameIDs(ctx, gameIDs); err != nil {
		return fmt.Errorf("failed to delete game answers: %w", err)
	}
	if err := q.DeleteGameQuestionsByGameIDs(ctx, gameIDs); err != nil {
		return fmt.Errorf("failed to delete game questions: %w", err)
	}
	if err := q.DeleteGameParticipantsByGameIDs(ctx, gameIDs); err != nil {
		return fmt.Errorf("failed to delete game participants: %w", err)
	}
	if err := q.DeleteGamesByIDs(ctx, gameIDs); err != nil {
		return fmt.Errorf("failed to delete games: %w", err)
	}

	return nil
}
