//go:build integration

package clientapi_test

import (
	"context"

	"github.com/starquake/topbanana/internal/game"
)

// errGameStore is a fault-injection game.Store for the two GetNext branches
// that real seeded data cannot reach. It embeds a real game.Store so every
// other method behaves normally, and overrides GetGame:
//
//   - getGameErr injects a chosen non-sentinel error, pinning #274 (the API
//     must not echo an internal store error string into a 5xx body). A real
//     failure (e.g. a closed DB) returns a driver-specific message, not the
//     recognisable marker string the leak test asserts is absent.
//   - injectedGame returns a synthetic game whose QuizID points at no quiz
//     row, so the service's GetQuiz returns the real quiz.ErrQuizNotFound. A
//     real game always references a real quiz (FK), so the "game exists, quiz
//     vanished" branch can't be produced from seeded data.
//
// HandleQuestionNext takes a concrete *game.Service, so the seam lives at
// the game store the service wraps.
type errGameStore struct {
	game.Store

	getGameErr   error
	injectedGame *game.Game
}

func (s errGameStore) GetGame(ctx context.Context, id string) (*game.Game, error) {
	if s.getGameErr != nil {
		return nil, s.getGameErr
	}
	if s.injectedGame != nil {
		return s.injectedGame, nil
	}

	return s.Store.GetGame(ctx, id)
}
