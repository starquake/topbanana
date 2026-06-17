package livesession

import (
	"context"
	"log/slog"
)

// logSessionKey is the slog attribute key the runner logs the session id
// under, kept distinct from logJoinCodeKey so the operator-facing id and the
// player-facing code never collide in a log line.
const logSessionKey = "session"

// slog attribute keys shared by the domain-layer log lines, so every
// live-session log line names the same field the same way. logPhaseKey
// matches the literal "phase" the runner already logs under.
const (
	logJoinCodeKey = "joinCode"
	logPlayerKey   = "player"
	logHostKey     = "host"
	logQuizKey     = "quiz"
	logPhaseKey    = "phase"
	logQuestionKey = "question"
	logOptionKey   = "option"
	logReadyKey    = "ready"
	logDeadlineKey = "deadline"
	logReasonKey   = "reason"
)

// logNonHostAttempt logs an Info line for a non-host caller trying a
// host-gated control, naming the action and who attempted it. A host who
// cannot start or end their room is a thing the host wants to see explained,
// not just a bare 403 in the access log.
func (s *Service) logNonHostAttempt(ctx context.Context, action, joinCode string, playerID int64) {
	s.logger.InfoContext(ctx, "live session control rejected: not host",
		slog.String("action", action),
		slog.String(logJoinCodeKey, joinCode),
		slog.Int64(logPlayerKey, playerID))
}

// logAnswerNotOpen logs a Debug line for an answer rejected because no question
// is open for it, naming the reason (wrong-phase / out-of-window /
// option-mismatch). Debug because a late tap can repeat the line per stray
// submit, while a host who wants to know why a pick did not land still has it.
func (s *Service) logAnswerNotOpen(ctx context.Context, sess *Session, playerID int64, reason string) {
	s.logger.DebugContext(ctx, "answer rejected: question not open",
		slog.String(logJoinCodeKey, sess.JoinCode),
		slog.Int64(logPlayerKey, playerID),
		slog.String(logPhaseKey, string(sess.Phase)),
		slog.String(logReasonKey, reason))
}
