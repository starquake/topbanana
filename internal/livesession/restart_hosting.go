package livesession

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/starquake/topbanana/internal/quiz"
)

// HostHasRunningGame reports whether the host has an active session with a game
// in flight (#853): one that has left the lobby/intermission staging and so
// cannot take a new quiz without ending it. The quiz view uses this to gate the
// "Host live" confirm-and-restart prompt. An empty staging lobby, an
// armed-but-not-started lobby, and the between-games intermission all return
// false (a pick just arms them, no confirm needed).
func (s *Service) HostHasRunningGame(ctx context.Context, hostPlayerID int64) (bool, error) {
	active, err := s.store.GetActiveSessionForHost(ctx, hostPlayerID)
	if err != nil {
		return false, fmt.Errorf(errGetActiveSessionFmt, err)
	}

	return active != nil && !canArmQuiz(active), nil
}

// RestartHosting ends the host's active session (if any) and opens a new room
// hosting quizID (#853) - the deliberate "switch the live quiz" path the host
// confirms when a game is already running. The target quiz is validated BEFORE
// the running session is ended, so an unhostable pick ([quiz.ErrQuizNotFound] /
// [ErrNotLiveQuiz]) bounces with nothing torn down. A rarer failure of the
// create after the end (e.g. join-code exhaustion) surfaces as an error; the old
// session is already gone, so the host's retry simply opens the new room.
func (s *Service) RestartHosting(ctx context.Context, quizID, hostPlayerID int64) (*Session, error) {
	// Validate the target quiz up front; CreateSession re-checks it, but doing it
	// here first guarantees we never end the running game for an unhostable pick.
	qz, err := s.quizzes.GetQuiz(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to get quiz for restart: %w", err)
	}
	if qz.Mode != quiz.ModeLive {
		return nil, ErrNotLiveQuiz
	}

	active, err := s.store.GetActiveSessionForHost(ctx, hostPlayerID)
	if err != nil {
		return nil, fmt.Errorf(errGetActiveSessionFmt, err)
	}
	if active != nil {
		if err = s.EndSession(ctx, active.JoinCode, hostPlayerID); err != nil {
			return nil, fmt.Errorf("failed to end running session: %w", err)
		}
	}

	// No active session now (just ended, or never any): CreateSession opens a new
	// armed lobby hosting the picked quiz.
	var sess *Session
	if sess, err = s.CreateSession(ctx, &quizID, hostPlayerID); err != nil {
		return nil, err
	}
	s.logger.InfoContext(ctx, "host restarted hosting: opened new room",
		slog.String(logJoinCodeKey, sess.JoinCode),
		slog.Int64(logHostKey, hostPlayerID),
		slog.Int64(logQuizKey, quizID))

	return sess, nil
}
