package livesession

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/starquake/topbanana/internal/quiz"
)

// StartHosting opens or reuses the host's live room for a quiz, enforcing one
// room per host (#851). It backs the quiz-view "Host live" control:
//   - No active room -> open a new lobby armed with the quiz (host starts it
//     when players are in), same as the prior "Play live".
//   - Active empty staging lobby OR between-games intermission -> arm the quiz
//     in it but stay in the lobby (reusing ArmQuiz), so the host gathers players
//     and presses Start, the same as the no-active-room case (#863, #875). No
//     second room is spawned, and the post-game pick never auto-starts.
//   - Active room with a game in flight -> leave it untouched and return it, so
//     a stray pick never disrupts a running game (the end-and-restart confirm
//     is deferred to #853).
//
// It returns the room the host should be redirected to; only its JoinCode is
// authoritative (the redirect target). In the reuse branch the returned snapshot
// predates the arm, so its Phase/QuizID/StartedAt may lag the now-armed room -
// callers must re-read if they need post-arm state. [quiz.ErrQuizNotFound] and
// [ErrNotLiveQuiz] propagate so the handler can bounce an unhostable quiz to the
// quiz list.
func (s *Service) StartHosting(ctx context.Context, quizID, hostPlayerID int64) (*Session, error) {
	active, err := s.store.GetActiveSessionForHost(ctx, hostPlayerID)
	if err != nil {
		return nil, fmt.Errorf(errGetActiveSessionFmt, err)
	}

	if active == nil {
		var sess *Session
		if sess, err = s.CreateSession(ctx, &quizID, hostPlayerID); err != nil {
			return nil, err
		}
		s.logger.InfoContext(ctx, "host started hosting: opened new room",
			slog.String(logJoinCodeKey, sess.JoinCode),
			slog.Int64(logHostKey, hostPlayerID),
			slog.Int64(logQuizKey, quizID))

		return sess, nil
	}

	if !canArmQuiz(active) {
		// A game is in flight: leave the running room untouched and return it so
		// a stray pick never disrupts it. The end-and-restart confirm is #853.
		s.logger.InfoContext(ctx, "host started hosting: reused running room",
			slog.String(logJoinCodeKey, active.JoinCode),
			slog.Int64(logHostKey, hostPlayerID),
			slog.Int64(logQuizKey, quizID))

		return active, nil
	}

	// An empty staging lobby and the between-games intermission both take a new
	// quiz without spawning a second room, and both arm it and stay in the lobby
	// so the host gathers players and presses Start (#875), matching the
	// no-active-session case which also lands on an armed lobby. canArmQuiz
	// guarantees active is one of these two; ArmQuiz handles both - RearmSession
	// re-arms an intermission room back to an armed lobby with started_at cleared.
	err = s.ArmQuiz(ctx, active.JoinCode, hostPlayerID, quizID)
	switch {
	case err == nil, errors.Is(err, ErrGameInFlight):
		// ErrGameInFlight means the room raced into flight between the read above
		// and the arm; treat it the same as "in flight, do nothing" and return
		// the room so the host lands on it.
		s.logger.InfoContext(ctx, "host started hosting: reused active room",
			slog.String(logJoinCodeKey, active.JoinCode),
			slog.Int64(logHostKey, hostPlayerID),
			slog.Int64(logQuizKey, quizID),
			slog.String(logPhaseKey, string(active.Phase)))

		return active, nil
	default:
		return nil, err
	}
}

// ArmQuiz arms a quiz to play in a room but leaves it in the lobby, not started
// (#863): the host then begins it with the existing "Start now" control once
// players are in, matching the no-active-session flow rather than starting the
// game outright. Only the host may call it, and only when no game is in flight.
// Returns the same errors as [Service.armRoomForHost].
func (s *Service) ArmQuiz(ctx context.Context, joinCode string, hostPlayerID, quizID int64) error {
	sess, err := s.armRoomForHost(ctx, joinCode, hostPlayerID, quizID)
	if err != nil {
		return err
	}

	// A newly armed quiz changes what the lobby shows (the Start controls appear),
	// so signal subscribers to re-GET.
	s.publish(sess.JoinCode, PhaseLobby)

	s.logger.InfoContext(ctx, "live quiz armed",
		slog.String(logJoinCodeKey, sess.JoinCode),
		slog.Int64(logQuizKey, quizID),
		slog.Int64(logHostKey, hostPlayerID))

	return nil
}

// canArmQuiz reports whether a quiz can be armed to play in the room right now:
// from an empty lobby that never started (the first game) or from the
// between-games intermission (the next game). A game in flight (any other phase,
// or a lobby already marked started) and a terminally finished room cannot.
// Mirrors the RearmSession query's WHERE so the service rejects early with a
// clear error, while the scoped UPDATE stays the real arbiter against a race.
func canArmQuiz(sess *Session) bool {
	if sess.Phase == PhaseIntermission {
		return true
	}

	return sess.Phase == PhaseLobby && sess.StartedAt == nil
}

// EndSession closes a room for good (#836): the host control that terminally
// finishes the room (finished + evict) from any live phase, so a host who is
// done can shut it down rather than leaving it for the idle auto-close. Only the
// host may call it. An already-finished room is treated as an idempotent no-op
// (the host double-posted or a stale tab re-ended it). Returns
// [ErrSessionNotFound] for an unknown code and [ErrNotHost] when the caller is
// not the host.
func (s *Service) EndSession(ctx context.Context, joinCode string, hostPlayerID int64) error {
	sess, err := s.store.GetSessionByJoinCode(ctx, normalizeJoinCode(joinCode))
	if err != nil {
		return fmt.Errorf(errGetSessionByCodeFmt, err)
	}
	if sess.HostPlayerID != hostPlayerID {
		s.logNonHostAttempt(ctx, "end", sess.JoinCode, hostPlayerID)

		return ErrNotHost
	}
	if sess.Phase == PhaseFinished {
		return nil
	}

	if err = s.store.Finish(ctx, sess.ID); err != nil {
		return fmt.Errorf("failed to finish session: %w", err)
	}

	// The room is now terminal; signal subscribers to re-GET so every surface
	// lands on the finished state and the live clients tear down.
	s.publish(sess.JoinCode, PhaseFinished)

	s.logger.InfoContext(ctx, "live session ended",
		slog.String(logJoinCodeKey, sess.JoinCode),
		slog.Int64(logHostKey, hostPlayerID))

	return nil
}

// GetActiveSessionForHost returns the host's current active (non-finished) room,
// or nil when the host has none (#836). Backs the "Resume hosting" link: a host
// who opened a room up front and browsed away can return to it. Not gated beyond
// the host id it is keyed on; the route layer host-gates the caller.
//
//nolint:nilnil // (nil, nil) is the deliberate "no active room" result; absence is not an error here.
func (s *Service) GetActiveSessionForHost(ctx context.Context, hostPlayerID int64) (*Session, error) {
	sess, err := s.store.GetActiveSessionForHost(ctx, hostPlayerID)
	if err != nil {
		return nil, fmt.Errorf(errGetActiveSessionFmt, err)
	}

	return sess, nil
}

// armRoomForHost validates that hostPlayerID hosts the room and that quizID is a
// live quiz that can be armed now, then points the room at it via RearmSession
// (which resets it to the lobby, not started) and returns the session. It does
// not publish or start - the caller decides: [Service.ArmQuiz] stops here (the
// room waits in the lobby), [Service.StartQuiz] marks it started. Returns
// [ErrSessionNotFound] for an unknown code, [ErrNotHost] for a foreign host,
// [quiz.ErrQuizNotFound] / [ErrNotLiveQuiz] for an unhostable quiz, and
// [ErrGameInFlight] when a game is already running.
func (s *Service) armRoomForHost(ctx context.Context, joinCode string, hostPlayerID, quizID int64) (*Session, error) {
	sess, err := s.store.GetSessionByJoinCode(ctx, normalizeJoinCode(joinCode))
	if err != nil {
		return nil, fmt.Errorf(errGetSessionByCodeFmt, err)
	}
	if sess.HostPlayerID != hostPlayerID {
		s.logNonHostAttempt(ctx, "arm", sess.JoinCode, hostPlayerID)

		return nil, ErrNotHost
	}
	if !canArmQuiz(sess) {
		return nil, ErrGameInFlight
	}

	qz, err := s.quizzes.GetQuiz(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to get quiz to arm: %w", err)
	}
	if qz.Mode != quiz.ModeLive {
		return nil, ErrNotLiveQuiz
	}

	// RearmSession is scoped to "no game in flight", so it is the real arbiter if
	// the room left that state between the read above and this write (a concurrent
	// arm or a start that raced this one); it returns ErrGameInFlight to the loser.
	if err = s.store.RearmSession(ctx, sess.ID, qz.ID); err != nil {
		return nil, fmt.Errorf("failed to arm session: %w", err)
	}

	return sess, nil
}
