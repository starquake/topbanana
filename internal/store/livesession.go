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
	"github.com/starquake/topbanana/internal/livesession"
)

// LiveSessionStore is the SQLite-backed implementation of
// [livesession.Store] for hosted live sessions (MP-1 / #678).
type LiveSessionStore struct {
	q      *db.Queries
	db     *sql.DB
	logger *slog.Logger
}

// NewLiveSessionStore initializes a LiveSessionStore with the provided
// database connection and logger.
func NewLiveSessionStore(conn *sql.DB, logger *slog.Logger) *LiveSessionStore {
	return &LiveSessionStore{q: db.New(conn), db: conn, logger: logger}
}

// Ping verifies the database connection.
func (s *LiveSessionStore) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	return nil
}

// CreateSession inserts a sessions row with a fresh xid and the
// caller-supplied join code, populating s.ID / s.CreatedAt / s.Phase from
// the returned row. The room's quiz is optional (#836): a nil sess.QuizID opens
// an empty room (quiz_id NULL, the "no game running yet" state). A join_code
// UNIQUE collision (the loser of a probe race in the service) surfaces as
// [livesession.ErrJoinCodeUnavailable].
func (s *LiveSessionStore) CreateSession(ctx context.Context, sess *livesession.Session) error {
	var quizID sql.NullInt64
	if sess.QuizID != nil {
		quizID = sql.NullInt64{Int64: *sess.QuizID, Valid: true}
	}
	row, err := s.q.CreateSession(ctx, db.CreateSessionParams{
		ID:           xid.New().String(),
		QuizID:       quizID,
		HostPlayerID: sess.HostPlayerID,
		JoinCode:     sess.JoinCode,
	})
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return livesession.ErrJoinCodeUnavailable
		}

		return fmt.Errorf("failed to create session: %w", err)
	}

	applySessionRow(sess, row)

	return nil
}

// JoinCodeExists reports whether a session already uses the candidate join
// code.
func (s *LiveSessionStore) JoinCodeExists(ctx context.Context, joinCode string) (bool, error) {
	exists, err := s.q.JoinCodeExists(ctx, joinCode)
	if err != nil {
		return false, fmt.Errorf("failed to check join code exists: %w", err)
	}

	return exists, nil
}

// GetSessionByJoinCode resolves a room code to its session with the lobby
// roster populated. Returns [livesession.ErrSessionNotFound] when no
// session uses the code.
func (s *LiveSessionStore) GetSessionByJoinCode(
	ctx context.Context, joinCode string,
) (*livesession.Session, error) {
	row, err := s.q.GetSessionByJoinCode(ctx, joinCode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, livesession.ErrSessionNotFound
		}

		return nil, fmt.Errorf("failed to get session by join code: %w", err)
	}

	sess := sessionFromRow(row)
	sess.Players, err = s.listPlayers(ctx, sess.ID)
	if err != nil {
		return nil, err
	}

	return sess, nil
}

// GetActiveSessionForHost returns the host's most recent active (non-finished)
// room with its roster populated, or (nil, nil) when the host has no active room
// (#836). Absence is the normal "nothing to resume" answer, not an error, so the
// service can offer the "Resume hosting" link only when a room comes back.
//
//nolint:nilnil // (nil, nil) is the deliberate "no active room" result; absence is not an error here.
func (s *LiveSessionStore) GetActiveSessionForHost(
	ctx context.Context, hostPlayerID int64,
) (*livesession.Session, error) {
	row, err := s.q.GetActiveSessionForHost(ctx, hostPlayerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}

		return nil, fmt.Errorf("failed to get active session for host: %w", err)
	}

	sess := sessionFromRow(row)
	sess.Players, err = s.listPlayers(ctx, sess.ID)
	if err != nil {
		return nil, err
	}

	return sess, nil
}

// AddPlayer adds (or revives on re-join) a roster row for the player. The
// display name is no longer stored per session (#716): the roster reads join
// players and select the current players.display_name, so the returned Player
// carries no name (the lobby/state read fans it out from the live join).
func (s *LiveSessionStore) AddPlayer(
	ctx context.Context, sessionID string, playerID int64,
) (*livesession.Player, error) {
	row, err := s.q.UpsertSessionPlayer(ctx, db.UpsertSessionPlayerParams{
		SessionID: sessionID,
		PlayerID:  playerID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to add session player: %w", err)
	}

	return playerFromSessionRow(row), nil
}

// SetReady toggles a participant's ready flag. Returns
// [livesession.ErrNotParticipant] when the UPDATE matches no roster row
// (the player has not joined the session).
//
//nolint:revive // ready is the desired boolean state of the ready toggle (a value to store), not a behavioural mode switch.
func (s *LiveSessionStore) SetReady(
	ctx context.Context, sessionID string, playerID int64, ready bool,
) error {
	var isReady int64
	if ready {
		isReady = 1
	}
	res, err := s.q.SetSessionPlayerReady(ctx, db.SetSessionPlayerReadyParams{
		IsReady:   isReady,
		SessionID: sessionID,
		PlayerID:  playerID,
	})
	if err != nil {
		return fmt.Errorf("failed to set session player ready: %w", err)
	}
	if database.MustRowsAffected(res) == 0 {
		return livesession.ErrNotParticipant
	}

	return nil
}

// GetSessionByID resolves a session by its primary key with the lobby roster
// populated. Returns [livesession.ErrSessionNotFound] when the id is unknown.
func (s *LiveSessionStore) GetSessionByID(ctx context.Context, id string) (*livesession.Session, error) {
	row, err := s.q.GetSession(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, livesession.ErrSessionNotFound
		}

		return nil, fmt.Errorf("failed to get session by id: %w", err)
	}

	sess := sessionFromRow(row)
	sess.Players, err = s.listPlayers(ctx, sess.ID)
	if err != nil {
		return nil, err
	}

	return sess, nil
}

// MarkStarted stamps started_at on a lobby session and reports whether it won
// the race. The UPDATE is scoped to started_at IS NULL, so exactly one of a
// host Start / armed-countdown firing sees a row affected.
func (s *LiveSessionStore) MarkStarted(ctx context.Context, sessionID string) (bool, error) {
	res, err := s.q.StartSession(ctx, sessionID)
	if err != nil {
		return false, fmt.Errorf("failed to start session: %w", err)
	}

	return database.MustRowsAffected(res) > 0, nil
}

// ArmStart stamps start_at (the last-call countdown deadline) on a lobby
// session. Returns [livesession.ErrNotInLobby] when the UPDATE matches no
// lobby row (the session has already left the lobby).
func (s *LiveSessionStore) ArmStart(ctx context.Context, sessionID string, startAt time.Time) error {
	res, err := s.q.ArmSessionStart(ctx, db.ArmSessionStartParams{
		StartAt: sql.NullTime{Time: startAt, Valid: true},
		ID:      sessionID,
	})
	if err != nil {
		return fmt.Errorf("failed to arm session start: %w", err)
	}
	if database.MustRowsAffected(res) == 0 {
		return livesession.ErrNotInLobby
	}

	return nil
}

// CancelStart clears start_at on a lobby session, stopping an armed countdown.
// Returns [livesession.ErrNotInLobby] when the UPDATE matches no lobby row.
func (s *LiveSessionStore) CancelStart(ctx context.Context, sessionID string) error {
	res, err := s.q.CancelSessionStart(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to cancel session start: %w", err)
	}
	if database.MustRowsAffected(res) == 0 {
		return livesession.ErrNotInLobby
	}

	return nil
}

// EnterRoundIntro moves the session into the round_intro phase for the round.
func (s *LiveSessionStore) EnterRoundIntro(ctx context.Context, sessionID string, roundID int64) error {
	if err := s.q.SetSessionRoundIntro(ctx, db.SetSessionRoundIntroParams{
		CurrentRoundID: sql.NullInt64{Int64: roundID, Valid: true},
		ID:             sessionID,
	}); err != nil {
		return fmt.Errorf("failed to enter round intro: %w", err)
	}

	return nil
}

// EnterQuestion issues a question with its server answer window.
func (s *LiveSessionStore) EnterQuestion(
	ctx context.Context, sessionID string, roundID, questionID int64, startedAt, expiresAt time.Time,
) error {
	if err := s.q.SetSessionQuestion(ctx, db.SetSessionQuestionParams{
		CurrentRoundID:    sql.NullInt64{Int64: roundID, Valid: true},
		CurrentQuestionID: sql.NullInt64{Int64: questionID, Valid: true},
		QuestionStartedAt: sql.NullTime{Time: startedAt, Valid: true},
		QuestionExpiresAt: sql.NullTime{Time: expiresAt, Valid: true},
		ID:                sessionID,
	}); err != nil {
		return fmt.Errorf("failed to enter question: %w", err)
	}

	return nil
}

// EnterReveal moves the session into the reveal phase.
func (s *LiveSessionStore) EnterReveal(ctx context.Context, sessionID string) error {
	if err := s.q.SetSessionReveal(ctx, sessionID); err != nil {
		return fmt.Errorf("failed to enter reveal: %w", err)
	}

	return nil
}

// EnterRoundResults moves the session into the round_results phase.
func (s *LiveSessionStore) EnterRoundResults(ctx context.Context, sessionID string) error {
	if err := s.q.SetSessionRoundResults(ctx, sessionID); err != nil {
		return fmt.Errorf("failed to enter round results: %w", err)
	}

	return nil
}

// Finish ends the session terminally.
func (s *LiveSessionStore) Finish(ctx context.Context, sessionID string) error {
	if err := s.q.SetSessionFinished(ctx, sessionID); err != nil {
		return fmt.Errorf("failed to finish session: %w", err)
	}

	return nil
}

// Intermission ends a game without closing the room: marks it intermission and
// clears the per-question runner columns, leaving the room alive (#836). When
// bumpPlayCount is true AND the session actually transitioned (it was
// not already in intermission or finished), the same transaction also bumps
// quizzes.play_count for the quiz this session is playing (#891), so the durable
// "times played" counter rides the same commit as the natural game-end
// transition and an accidental repeat call from a terminal phase cannot
// double-bump.
//
//nolint:revive // bumpPlayCount signals whether this game-end should count as a play (a play-count bump input), not a behavioural mode switch.
func (s *LiveSessionStore) Intermission(ctx context.Context, sessionID string, bumpPlayCount bool) error {
	err := database.ExecTx(ctx, s.db, func(q *db.Queries) error {
		res, ierr := q.SetSessionIntermission(ctx, sessionID)
		if ierr != nil {
			return fmt.Errorf("set session intermission: %w", ierr)
		}
		transitioned := database.MustRowsAffected(res) > 0
		if bumpPlayCount && transitioned {
			if berr := q.BumpQuizPlayCountForSession(ctx, sessionID); berr != nil {
				return fmt.Errorf("bump quiz play count: %w", berr)
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to move session to intermission: %w", err)
	}

	return nil
}

// RearmSession arms a quiz to play in a room whenever no game is running (#836):
// points it at the quiz, resets to the lobby, clears the per-game runner columns
// (game_seq bumped only from intermission), and clears every roster player's
// ready flag, in one transaction so the re-arm and the ready reset cannot be
// partially applied. Returns [livesession.ErrGameInFlight] when a game is already
// running or the room is terminally finished (the RearmSession UPDATE matches no
// row).
func (s *LiveSessionStore) RearmSession(ctx context.Context, sessionID string, quizID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin rearm session transaction: %w", err)
	}

	if err = s.rearmSessionTx(ctx, tx, sessionID, quizID); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("rearm session failed: %w (rollback error: %w)", err, rbErr)
		}

		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit rearm session transaction: %w", err)
	}

	return nil
}

// ListRoundStandings returns one Standing per roster player with the score
// earned in the given round and their cumulative session total, ordered
// best-first. Rank is left 0 for the service to stamp.
func (s *LiveSessionStore) ListRoundStandings(
	ctx context.Context, sessionID string, roundID int64,
) ([]*livesession.Standing, error) {
	rows, err := s.q.ListSessionStandings(ctx, db.ListSessionStandingsParams{
		RoundID:   roundID,
		SessionID: sessionID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list session round standings: %w", err)
	}

	standings := make([]*livesession.Standing, 0, len(rows))
	for _, r := range rows {
		standings = append(standings, &livesession.Standing{
			PlayerID:    r.PlayerID,
			DisplayName: r.DisplayName,
			RoundScore:  int(r.RoundScore),
			TotalScore:  int(r.TotalScore),
		})
	}

	return standings, nil
}

// ListFinalStandings returns one Standing per roster player with their
// cumulative session total, ordered best-first. RoundScore is 0 and Rank is
// left 0 for the service to stamp.
func (s *LiveSessionStore) ListFinalStandings(
	ctx context.Context, sessionID string,
) ([]*livesession.Standing, error) {
	rows, err := s.q.ListSessionFinalStandings(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to list session final standings: %w", err)
	}

	standings := make([]*livesession.Standing, 0, len(rows))
	for _, r := range rows {
		standings = append(standings, &livesession.Standing{
			PlayerID:    r.PlayerID,
			DisplayName: r.DisplayName,
			TotalScore:  int(r.TotalScore),
		})
	}

	return standings, nil
}

// RecordAnswer records (or overwrites) a player's pick for the current
// session question and, atomically, refreshes that player's last_seen_at to
// the answer's timestamp. An answer is proof of liveness, so a player who just
// picked must count as active even without a held SSE heartbeat; running both
// writes in one transaction keeps the answer and the liveness bump from being
// partially applied (see #712).
func (s *LiveSessionStore) RecordAnswer(
	ctx context.Context, sessionID string, questionID, playerID, optionID int64, answeredAt time.Time,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin record answer transaction: %w", err)
	}

	if err = s.recordAnswerTx(ctx, tx, sessionID, questionID, playerID, optionID, answeredAt); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("record answer failed: %w (rollback error: %w)", err, rbErr)
		}

		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit record answer transaction: %w", err)
	}

	return nil
}

// CountAnswers returns how many players have picked for the session question.
func (s *LiveSessionStore) CountAnswers(ctx context.Context, sessionID string, questionID int64) (int, error) {
	count, err := s.q.CountSessionAnswersForQuestion(ctx, db.CountSessionAnswersForQuestionParams{
		SessionID:  sessionID,
		QuestionID: questionID,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to count session answers: %w", err)
	}

	return int(count), nil
}

// TouchLastSeen refreshes the participant's last_seen_at heartbeat. Returns
// [livesession.ErrNotParticipant] when no roster row matches the (join code,
// player) pair.
func (s *LiveSessionStore) TouchLastSeen(ctx context.Context, joinCode string, playerID int64) error {
	res, err := s.q.TouchSessionPlayerLastSeen(ctx, db.TouchSessionPlayerLastSeenParams{
		PlayerID: playerID,
		JoinCode: joinCode,
	})
	if err != nil {
		return fmt.Errorf("failed to touch session player last seen: %w", err)
	}
	if database.MustRowsAffected(res) == 0 {
		return livesession.ErrNotParticipant
	}

	return nil
}

// TouchHostLastSeen refreshes the host's host_last_seen_at heartbeat for the
// session identified by join code. Returns [livesession.ErrSessionNotFound]
// when no session uses the code.
func (s *LiveSessionStore) TouchHostLastSeen(ctx context.Context, joinCode string) error {
	res, err := s.q.TouchSessionHostLastSeen(ctx, joinCode)
	if err != nil {
		return fmt.Errorf("failed to touch session host last seen: %w", err)
	}
	if database.MustRowsAffected(res) == 0 {
		return livesession.ErrSessionNotFound
	}

	return nil
}

// MarkPlayerLeft stamps left_at on the participant's roster row in the
// session identified by join code, dropping them from the live reads
// (roster, answered-order badges, standings). Idempotent: a second leave
// matches no active row and returns [livesession.ErrNotParticipant], same as
// a leave from a player who never joined.
func (s *LiveSessionStore) MarkPlayerLeft(ctx context.Context, joinCode string, playerID int64) error {
	res, err := s.q.MarkSessionPlayerLeft(ctx, db.MarkSessionPlayerLeftParams{
		PlayerID: playerID,
		JoinCode: joinCode,
	})
	if err != nil {
		return fmt.Errorf("failed to mark session player left: %w", err)
	}
	if database.MustRowsAffected(res) == 0 {
		return livesession.ErrNotParticipant
	}

	return nil
}

// sqliteTimestampLayout matches SQLite's CURRENT_TIMESTAMP text encoding
// ('YYYY-MM-DD HH:MM:SS'). The active-window cutoff is formatted with it so the
// last_seen_at comparison stays a same-encoding string compare; binding a Go
// [time.Time] would arrive in a different format and the comparison would
// silently lie (see retention.sql for the same trap).
const sqliteTimestampLayout = "2006-01-02 15:04:05"

// CountActiveUnanswered returns how many roster players are still active
// (last_seen_at at or after since) yet have not picked for the session
// question.
func (s *LiveSessionStore) CountActiveUnanswered(
	ctx context.Context, sessionID string, questionID int64, since time.Time,
) (int, error) {
	count, err := s.q.CountActivePlayersUnansweredForQuestion(ctx, db.CountActivePlayersUnansweredForQuestionParams{
		SessionID:  sessionID,
		Since:      since.UTC().Format(sqliteTimestampLayout),
		QuestionID: questionID,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to count active unanswered players: %w", err)
	}

	return int(count), nil
}

// CountActive returns how many roster players are still active (last_seen_at
// at or after since).
func (s *LiveSessionStore) CountActive(ctx context.Context, sessionID string, since time.Time) (int, error) {
	count, err := s.q.CountActivePlayersForSession(ctx, db.CountActivePlayersForSessionParams{
		SessionID: sessionID,
		Since:     since.UTC().Format(sqliteTimestampLayout),
	})
	if err != nil {
		return 0, fmt.Errorf("failed to count active players: %w", err)
	}

	return int(count), nil
}

// ListAnswers returns every pick for the session question in answered order,
// with the chosen option's correctness.
func (s *LiveSessionStore) ListAnswers(
	ctx context.Context, sessionID string, questionID int64,
) ([]*livesession.SessionAnswer, error) {
	rows, err := s.q.ListSessionAnswersForQuestion(ctx, db.ListSessionAnswersForQuestionParams{
		SessionID:  sessionID,
		QuestionID: questionID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list session answers: %w", err)
	}

	answers := make([]*livesession.SessionAnswer, 0, len(rows))
	for _, r := range rows {
		a := &livesession.SessionAnswer{
			PlayerID:   r.PlayerID,
			OptionID:   r.OptionID,
			AnsweredAt: r.AnsweredAt,
			Correct:    r.IsCorrect,
		}
		if r.Score.Valid {
			score := int(r.Score.Int64)
			a.Score = &score
		}
		answers = append(answers, a)
	}

	return answers, nil
}

// SetAnswerScore writes the computed score for one pick at close.
func (s *LiveSessionStore) SetAnswerScore(
	ctx context.Context, sessionID string, questionID, playerID int64, score int,
) error {
	if err := s.q.SetSessionAnswerScore(ctx, db.SetSessionAnswerScoreParams{
		Score:      sql.NullInt64{Int64: int64(score), Valid: true},
		SessionID:  sessionID,
		QuestionID: questionID,
		PlayerID:   playerID,
	}); err != nil {
		return fmt.Errorf("failed to set session answer score: %w", err)
	}

	return nil
}

// ListLiveSessionIDs returns the ids of every session not yet finished.
func (s *LiveSessionStore) ListLiveSessionIDs(ctx context.Context) ([]string, error) {
	ids, err := s.q.ListLiveSessionIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list live session ids: %w", err)
	}

	return ids, nil
}

// listPlayers loads the lobby roster for a session in join order.
func (s *LiveSessionStore) listPlayers(ctx context.Context, sessionID string) ([]*livesession.Player, error) {
	rows, err := s.q.ListSessionPlayers(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to list session players for %q: %w", sessionID, err)
	}

	players := make([]*livesession.Player, 0, len(rows))
	for _, r := range rows {
		players = append(players, playerFromRosterRow(r))
	}

	return players, nil
}

func (s *LiveSessionStore) recordAnswerTx(
	ctx context.Context, tx *sql.Tx, sessionID string, questionID, playerID, optionID int64, answeredAt time.Time,
) error {
	q := s.q.WithTx(tx)

	if err := q.UpsertSessionAnswer(ctx, db.UpsertSessionAnswerParams{
		SessionID:  sessionID,
		QuestionID: questionID,
		PlayerID:   playerID,
		OptionID:   optionID,
		AnsweredAt: answeredAt,
	}); err != nil {
		return fmt.Errorf("failed to record session answer: %w", err)
	}

	if err := q.RefreshSessionPlayerLastSeenAt(ctx, db.RefreshSessionPlayerLastSeenAtParams{
		Seen:      answeredAt.UTC().Format(sqliteTimestampLayout),
		SessionID: sessionID,
		PlayerID:  playerID,
	}); err != nil {
		return fmt.Errorf("failed to refresh session player last seen: %w", err)
	}

	return nil
}

func (s *LiveSessionStore) rearmSessionTx(ctx context.Context, tx *sql.Tx, sessionID string, quizID int64) error {
	q := s.q.WithTx(tx)

	res, err := q.RearmSession(ctx, db.RearmSessionParams{
		QuizID: sql.NullInt64{Int64: quizID, Valid: true},
		ID:     sessionID,
	})
	if err != nil {
		return fmt.Errorf("failed to rearm session: %w", err)
	}
	if database.MustRowsAffected(res) == 0 {
		return livesession.ErrGameInFlight
	}

	if err = q.ResetSessionPlayersReady(ctx, sessionID); err != nil {
		return fmt.Errorf("failed to reset session players ready: %w", err)
	}

	return nil
}

// sessionFromRow maps a generated sessions row onto the domain type
// (without the roster, which the caller fans out separately).
func sessionFromRow(row db.Session) *livesession.Session {
	sess := &livesession.Session{
		ID:           row.ID,
		HostPlayerID: row.HostPlayerID,
		JoinCode:     row.JoinCode,
		Phase:        livesession.Phase(row.Phase),
		GameSeq:      row.GameSeq,
		CreatedAt:    row.CreatedAt,
	}
	if row.QuizID.Valid {
		sess.QuizID = &row.QuizID.Int64
	}
	if row.CurrentRoundID.Valid {
		sess.CurrentRoundID = &row.CurrentRoundID.Int64
	}
	if row.CurrentQuestionID.Valid {
		sess.CurrentQuestionID = &row.CurrentQuestionID.Int64
	}
	if row.QuestionStartedAt.Valid {
		sess.QuestionStartedAt = &row.QuestionStartedAt.Time
	}
	if row.QuestionExpiresAt.Valid {
		sess.QuestionExpiresAt = &row.QuestionExpiresAt.Time
	}
	if row.StartedAt.Valid {
		sess.StartedAt = &row.StartedAt.Time
	}
	if row.FinishedAt.Valid {
		sess.FinishedAt = &row.FinishedAt.Time
	}
	if row.HostLastSeenAt.Valid {
		sess.HostLastSeenAt = &row.HostLastSeenAt.Time
	}
	if row.StartAt.Valid {
		sess.StartAt = &row.StartAt.Time
	}

	return sess
}

// applySessionRow copies the DB-assigned fields back onto a session the
// caller built for an insert.
func applySessionRow(sess *livesession.Session, row db.Session) {
	sess.ID = row.ID
	sess.JoinCode = row.JoinCode
	sess.Phase = livesession.Phase(row.Phase)
	sess.GameSeq = row.GameSeq
	sess.CreatedAt = row.CreatedAt
	if row.StartedAt.Valid {
		sess.StartedAt = &row.StartedAt.Time
	}
	if row.FinishedAt.Valid {
		sess.FinishedAt = &row.FinishedAt.Time
	}
}

// playerFromRosterRow maps a roster read (joined to players for the current
// display_name, #716) onto the domain roster type.
func playerFromRosterRow(row db.ListSessionPlayersRow) *livesession.Player {
	return &livesession.Player{
		ID:          row.ID,
		SessionID:   row.SessionID,
		PlayerID:    row.PlayerID,
		DisplayName: row.DisplayName,
		IsReady:     row.IsReady != 0,
		JoinedAt:    row.JoinedAt,
		LastSeenAt:  row.LastSeenAt,
	}
}

// playerFromSessionRow maps the bare session_players row returned by the
// AddPlayer upsert onto the domain roster type. The row no longer carries a
// display name (#716): the lobby/state read fans the current
// players.display_name out via playerFromRosterRow.
func playerFromSessionRow(row db.SessionPlayer) *livesession.Player {
	return &livesession.Player{
		ID:         row.ID,
		SessionID:  row.SessionID,
		PlayerID:   row.PlayerID,
		IsReady:    row.IsReady != 0,
		JoinedAt:   row.JoinedAt,
		LastSeenAt: row.LastSeenAt,
	}
}
