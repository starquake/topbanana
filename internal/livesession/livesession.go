// Package livesession contains the hosted live-session domain (MP-1 /
// #678): a host opens a session for a live quiz, players join anonymously
// and toggle ready, and one read returns the authoritative lobby state.
//
// It is named livesession rather than session to avoid colliding with
// internal/session, which is the auth cookie manager - an unrelated
// concern that happens to share the English word.
package livesession

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/starquake/topbanana/internal/quiz"
)

var (
	// ErrSessionNotFound is returned when a session lookup (by id or join
	// code) finds no matching row. Handlers map it to 404.
	ErrSessionNotFound = errors.New("session not found")

	// ErrNotLiveQuiz is returned by [Service.CreateSession] when the quiz
	// exists and is visible to the host but is not mode='live'. Only live
	// quizzes are hostable (MP-0 / #677).
	ErrNotLiveQuiz = errors.New("quiz is not a live quiz")

	// ErrNotParticipant is returned by participant-gated reads/writes when
	// the caller has not joined the session. Handlers map it to 404 so the
	// join code stays opaque to non-participants, mirroring the game
	// participant gate (#272).
	ErrNotParticipant = errors.New("player is not a session participant")

	// ErrJoinCodeUnavailable is returned by [Service.CreateSession] when
	// the generator could not find a free join code within its attempt
	// budget. Effectively impossible with the code space in use; surfaced
	// as a sentinel so the handler can map it to a 500 rather than leaking
	// the retry-exhaustion detail.
	ErrJoinCodeUnavailable = errors.New("could not allocate a unique join code")

	// ErrNotHost is returned by host-gated actions ([Service.Start]) when
	// the caller is not the session's host. Handlers map it to 403.
	ErrNotHost = errors.New("player is not the session host")

	// ErrSessionAlreadyStarted is returned by [Service.Start] when the
	// session has already left the lobby. Handlers treat it as an
	// idempotent no-op (the host clicked start twice or the auto-start
	// raced the host).
	ErrSessionAlreadyStarted = errors.New("session has already started")

	// ErrQuestionNotOpen is returned by [Service.SubmitAnswer] when the
	// session is not currently in the question phase, the option is not
	// part of the current question, or the answer window has closed.
	ErrQuestionNotOpen = errors.New("no question is open for answers")

	// ErrAlreadyPlayed is returned by [Service.Join] when the player has
	// already sat through a finished session of the quiz. A live quiz may
	// be played once; an admin reset clears the participation so the player
	// can join again. Handlers map it to 403.
	ErrAlreadyPlayed = errors.New("player has already played this quiz")

	// ErrLobbyClosed is returned by [Service.Join] when the session has
	// already left the lobby: the lobby closes at start and v1 has no
	// late join. Handlers map it to 409.
	ErrLobbyClosed = errors.New("session lobby has closed")
)

// Phase is the server-authoritative state-machine label for a session.
// The runner (MP-5 / #682, MP-6 / #683) advances a session through the
// gameplay phases. The DB CHECK on sessions.phase enforces the same set.
type Phase string

// Session phases. The runner advances lobby -> round_intro -> question ->
// reveal (repeating per question) -> round_results (after the last question
// of a round) -> the next round's round_intro, and ends at finished.
const (
	PhaseLobby        Phase = "lobby"
	PhaseRoundIntro   Phase = "round_intro"
	PhaseQuestion     Phase = "question"
	PhaseReveal       Phase = "reveal"
	PhaseRoundResults Phase = "round_results"
	PhaseFinished     Phase = "finished"
)

const (
	// HeartbeatInterval is how often a held SSE connection refreshes the
	// participant's last_seen_at. The runner reads last_seen_at to decide who
	// is still active; a player who answered but whose tab is now in the
	// background still beats on this cadence, so they keep counting as active.
	HeartbeatInterval = 10 * time.Second
	// ActiveWindow is how long after a participant's last heartbeat the runner
	// still counts them as active. Set to 3x HeartbeatInterval so a single
	// dropped or delayed beat does not prematurely mark a present player stale;
	// a genuinely disconnected player ages out within this window and stops
	// holding a question open.
	ActiveWindow = 3 * HeartbeatInterval
)

// errGetSessionByCodeFmt is the wrap format every code-keyed lookup shares
// when GetSessionByJoinCode fails, so the wrapped sentinel
// ([ErrSessionNotFound]) still surfaces to callers via [errors.Is].
const errGetSessionByCodeFmt = "failed to get session by join code: %w"

// Session is one hosted run of a live quiz. Players holds the lobby roster
// when populated by [Store.GetSessionByJoinCode]; it is nil on the bare
// create/get paths that do not fan out the roster.
type Session struct {
	ID           string
	QuizID       int64
	HostPlayerID int64
	JoinCode     string
	Phase        Phase
	// CurrentRoundID / CurrentQuestionID point at the question the runner
	// is currently driving; nil in the lobby and once finished.
	CurrentRoundID    *int64
	CurrentQuestionID *int64
	// QuestionStartedAt / QuestionExpiresAt are the server-authoritative
	// answer window for the current question (the same StartedAt/ExpiredAt
	// the solo game uses). Clients drive their countdown off
	// QuestionExpiresAt minus the server clock, never their own wall clock.
	QuestionStartedAt *time.Time
	QuestionExpiresAt *time.Time
	CreatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
	Players           []*Player
}

// Player is one roster row: a player who joined a session, with the
// per-session display name and ready flag the lobby shows.
type Player struct {
	ID          int64
	SessionID   string
	PlayerID    int64
	DisplayName string
	IsReady     bool
	JoinedAt    time.Time
	LastSeenAt  time.Time
}

// LobbyState is the authoritative read returned by
// [Service.GetLobbyState]: the session's phase, its roster, and enough
// quiz metadata for a surface to render the lobby without a second
// round-trip. This is the frozen DTO contract the later FE/BE phases
// (MP-2..MP-5) build on; the JSON wire shape is pinned in the clientapi
// handler.
//
// CurrentQuestion and Answers are populated only in the gameplay phases
// (round_intro onward); they are nil in the lobby. Correctness on the
// question's options and on each answer is surfaced ONLY in the reveal
// phase - before reveal the question carries option text without a correct
// flag and the answers carry player+order only, never which pick was right.
type LobbyState struct {
	Session         *Session
	Quiz            *quiz.Quiz
	CurrentQuestion *quiz.Question
	Answers         []*SessionAnswer
	// Revealed is true once the session is in the reveal phase, the single
	// gate the handler reads to decide whether to expose correctness.
	Revealed bool
	// Standings carries the per-player ranking the bar graph (MP-9) consumes.
	// Populated in the round_results phase (with each player's points-this-round
	// alongside the running total) and in the finished phase (final standings,
	// where RoundScore is 0 since no single round is in focus). Nil in every
	// other phase. Ordered best-first, rank stamped 1-indexed.
	Standings []*Standing
}

// Standing is one player's place in the session ranking shown between rounds
// (round_results) and at the end (finished). RoundScore is the points the
// player earned in the round that just finished (0 in the finished phase, which
// has no single round in focus); TotalScore is their cumulative session score.
// Rank is 1-indexed over the full roster, ties broken by display name so the
// ordering is stable across reads.
type Standing struct {
	PlayerID    int64
	DisplayName string
	RoundScore  int
	TotalScore  int
	Rank        int
}

// Store is the persistence surface the live-session domain needs. Defined
// here (not in internal/store) so domain code does not import the concrete
// store, matching the game/quiz domains.
type Store interface {
	// Ping returns the status of the database connection.
	Ping(ctx context.Context) error
	// CreateSession inserts a session row with the given quiz, host, and
	// pre-checked join code, returning the populated [Session]. A
	// join_code UNIQUE collision surfaces as [ErrJoinCodeUnavailable] so
	// the generator's pre-check race loser can be retried by the caller.
	CreateSession(ctx context.Context, s *Session) error
	// JoinCodeExists reports whether a session already uses the candidate
	// join code, so the generator can regenerate before paying for the
	// INSERT.
	JoinCodeExists(ctx context.Context, joinCode string) (bool, error)
	// GetSessionByJoinCode resolves a room code to its session with the
	// lobby roster populated. Returns [ErrSessionNotFound] when no session
	// uses the code.
	GetSessionByJoinCode(ctx context.Context, joinCode string) (*Session, error)
	// PlayerFinishedSessionForQuiz reports whether the player has a roster
	// row in a finished session of the given quiz. Backs the replay gate in
	// [Service.Join].
	PlayerFinishedSessionForQuiz(ctx context.Context, playerID, quizID int64) (bool, error)
	// AddPlayer adds (or revives) a roster row for the player under the
	// requested display name. Returns [ErrDisplayNameTaken] on a
	// per-session display-name collision so the caller can fall back to a
	// petname.
	AddPlayer(ctx context.Context, sessionID string, playerID int64, displayName string) (*Player, error)
	// SetReady toggles a participant's ready flag. Returns
	// [ErrNotParticipant] when the player has no roster row in the
	// session.
	SetReady(ctx context.Context, sessionID string, playerID int64, ready bool) error
	// GetSessionByID resolves a session by its primary key with the roster
	// populated. The runner works in session ids (its in-memory bookkeeping
	// is keyed by id), while the lobby paths work in join codes. Returns
	// [ErrSessionNotFound] when the id is unknown.
	GetSessionByID(ctx context.Context, id string) (*Session, error)
	// MarkStarted stamps started_at on a session still in the lobby and
	// reports whether it won the race (true) or the session had already
	// started (false). Used by both the host Start and the auto-start so
	// only one of them issues the first round.
	MarkStarted(ctx context.Context, sessionID string) (bool, error)
	// EnterRoundIntro moves the session into the round_intro phase for the
	// given round, clearing the per-question runner columns.
	EnterRoundIntro(ctx context.Context, sessionID string, roundID int64) error
	// EnterQuestion issues a question: records the current round + question
	// and the server answer window, and moves into the question phase.
	EnterQuestion(
		ctx context.Context,
		sessionID string,
		roundID, questionID int64,
		startedAt, expiresAt time.Time,
	) error
	// EnterReveal moves the session into the reveal phase, leaving the
	// current question and window in place.
	EnterReveal(ctx context.Context, sessionID string) error
	// EnterRoundResults moves the session into the round_results phase shown
	// after the last question of a round, leaving current_round_id in place so
	// a reader knows which round just finished.
	EnterRoundResults(ctx context.Context, sessionID string) error
	// Finish ends the session: marks it finished and clears the
	// per-question runner columns.
	Finish(ctx context.Context, sessionID string) error
	// RecordAnswer records (or overwrites) a player's pick for the current
	// session question. Idempotent on (session, question, player).
	RecordAnswer(
		ctx context.Context,
		sessionID string,
		questionID, playerID, optionID int64,
		answeredAt time.Time,
	) error
	// TouchLastSeen refreshes a participant's last_seen_at, the active-player
	// heartbeat. Returns [ErrNotParticipant] when the (join code, player)
	// pair matches no roster row. Keyed on join code so the SSE handler need
	// only carry the code it already gates on.
	TouchLastSeen(ctx context.Context, joinCode string, playerID int64) error
	// MarkPlayerLeft stamps left_at on the participant's roster row in the
	// session identified by join code, dropping them from the live reads
	// (roster, answered-order badges, standings). Returns [ErrNotParticipant]
	// when no active roster row matches, which makes a repeat leave a no-op.
	MarkPlayerLeft(ctx context.Context, joinCode string, playerID int64) error
	// CountActiveUnanswered returns how many roster players are still active
	// (last_seen_at at or after since) yet have not picked for the given
	// session question. The runner early-closes once this reaches 0.
	CountActiveUnanswered(ctx context.Context, sessionID string, questionID int64, since time.Time) (int, error)
	// CountActive returns how many roster players are still active
	// (last_seen_at at or after since), so the runner can tell an empty /
	// all-stale roster (which must time out) from one with a live answerer.
	CountActive(ctx context.Context, sessionID string, since time.Time) (int, error)
	// ListAnswers returns every pick for the given session question in
	// answered order, with the chosen option's correctness, for scoring at
	// close and the answered-order view.
	ListAnswers(ctx context.Context, sessionID string, questionID int64) ([]*SessionAnswer, error)
	// SetAnswerScore writes the computed score for one pick at close.
	SetAnswerScore(ctx context.Context, sessionID string, questionID, playerID int64, score int) error
	// ListLiveSessionIDs returns the ids of every session not yet finished,
	// in creation order, so the runner can scan active rooms each beat.
	ListLiveSessionIDs(ctx context.Context) ([]string, error)
	// ListRoundStandings returns one row per roster player with the score they
	// earned in the given round and their cumulative session total, ordered
	// best-first. Used to populate the round_results state.
	ListRoundStandings(ctx context.Context, sessionID string, roundID int64) ([]*Standing, error)
	// ListFinalStandings returns one row per roster player with their
	// cumulative session total, ordered best-first. Used to populate the
	// finished state. RoundScore on each returned Standing is 0.
	ListFinalStandings(ctx context.Context, sessionID string) ([]*Standing, error)
}

// SessionAnswer is one recorded pick. Correct is the chosen option's
// correctness; the runner only ever surfaces it to clients in the reveal
// phase, never before (the no-spoiler guarantee). Score is nil until the
// question closes.
type SessionAnswer struct {
	PlayerID   int64
	OptionID   int64
	AnsweredAt time.Time
	Correct    bool
	Score      *int
}

// ErrDisplayNameTaken is returned by [Store.AddPlayer] when the requested
// per-session display name collides with another roster row. The service
// recovers by retrying with a petname.
var ErrDisplayNameTaken = errors.New("session display name taken")

// QuizReader is the slice of the quiz store the service needs: load the
// full quiz (for mode + lobby metadata). Kept narrow so the service does
// not depend on the whole quiz.Store surface.
type QuizReader interface {
	GetQuiz(ctx context.Context, id int64) (*quiz.Quiz, error)
}

// Publisher is the tiny seam the service uses to signal that a session's
// state has moved (MP-2 / #679). Implemented by *Hub in production;
// nil-by-default so tests that don't care about the event channel don't
// have to wire anything up. The service calls Publish after every
// successful lobby mutation (join, ready) so subscribers re-GET
// /api/sessions/{code}/state. The returned Tick is ignored by the service;
// the SSE handler is the consumer.
type Publisher interface {
	Publish(code string, phase Phase) Tick
}

// Advancer is the seam through which [Service.Start] hands a freshly
// started session to the runner so it begins the first round immediately
// (the host-Start override) instead of waiting for the next runner beat.
// Implemented by *Runner in production; nil-by-default so tests that drive
// the runner directly (or do not need a runner) need not wire one.
type Advancer interface {
	// Begin starts the runner driving the session identified by id from the
	// lobby into its first round. Safe to call more than once for the same
	// session; a session already past the lobby is a no-op.
	Begin(ctx context.Context, sessionID string)
}

// Service orchestrates the live-session use cases over the store layer and
// the quiz reader.
type Service struct {
	store     Store
	quizzes   QuizReader
	logger    *slog.Logger
	publisher Publisher
	advancer  Advancer
	newCode   func() string
	codeTries int
}

// joinCodeAttempts caps how many distinct codes the generator tries
// before giving up. With the ambiguity-free alphabet and code length in
// use the space is large enough that a single collision is rare and N in
// a row is effectively impossible, so a small budget keeps create latency
// bounded while never realistically failing.
const joinCodeAttempts = 8

// NewService wires a live-session service. The join-code generator is the
// package default; tests override it via [NewServiceWithCodeGen].
func NewService(store Store, quizzes QuizReader, logger *slog.Logger) *Service {
	return &Service{
		store:     store,
		quizzes:   quizzes,
		logger:    logger,
		newCode:   GenerateJoinCode,
		codeTries: joinCodeAttempts,
	}
}

// newServiceWithCodeGen builds a service with an injected code generator
// and attempt budget so tests can force collisions deterministically.
// Exposed to the external test package via export_test.go.
func newServiceWithCodeGen(
	store Store, quizzes QuizReader, logger *slog.Logger, newCode func() string, tries int,
) *Service {
	return &Service{
		store:     store,
		quizzes:   quizzes,
		logger:    logger,
		newCode:   newCode,
		codeTries: tries,
	}
}

// SetPublisher wires a publisher invoked after every successful lobby
// mutation so SSE subscribers learn that the session state moved. Optional
// - the service works fine without one (publishes become no-ops).
//
// Not safe for concurrent use: must be called during startup wiring,
// before the service is handed to any HTTP handler that may mutate a
// session. There is no in-flight reconfiguration use case for this field.
func (s *Service) SetPublisher(p Publisher) {
	s.publisher = p
}

// SetAdvancer wires the runner that [Service.Start] hands a started session
// to so it begins immediately. Same startup-only contract as
// [Service.SetPublisher]: call during wiring, before any handler runs.
func (s *Service) SetAdvancer(a Advancer) {
	s.advancer = a
}

// CreateSession opens a hosted session for the given quiz on behalf of the
// host. The route layer has already gated the caller to host/admin; this
// method enforces the domain rules: the quiz must exist and be visible to
// the host (any visibility - a host can view any quiz, decision 4) and
// must be mode='live' (MP-0 / #677). Returns [quiz.ErrQuizNotFound] when
// the quiz does not exist and [ErrNotLiveQuiz] when it is a solo quiz.
func (s *Service) CreateSession(ctx context.Context, quizID, hostPlayerID int64) (*Session, error) {
	qz, err := s.quizzes.GetQuiz(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to get quiz for session: %w", err)
	}
	if qz.Mode != quiz.ModeLive {
		return nil, ErrNotLiveQuiz
	}

	code, err := s.allocateJoinCode(ctx)
	if err != nil {
		return nil, err
	}

	sess := &Session{
		QuizID:       qz.ID,
		HostPlayerID: hostPlayerID,
		JoinCode:     code,
		Phase:        PhaseLobby,
	}
	if err = s.store.CreateSession(ctx, sess); err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	return sess, nil
}

// rosterHasPlayer reports whether playerID already holds a roster row in the
// session, so a re-join past the lobby is treated as a reconnect rather than a
// rejected late join.
func rosterHasPlayer(players []*Player, playerID int64) bool {
	for _, p := range players {
		if p.PlayerID == playerID {
			return true
		}
	}

	return false
}

// Join adds the player to the session identified by join code under the
// requested display name. The display name is required; a per-session
// collision is recovered transparently by retrying with a petname, so the
// caller always lands in the lobby (decision 4 / claim-name parity). The
// chosen display name is carried on the returned [Player]. Returns
// [ErrSessionNotFound] when the code resolves to no session,
// [ErrLobbyClosed] when the session has already left the lobby (v1 has no
// late join), and [ErrAlreadyPlayed] when the player has already finished a
// session of the same quiz (a live quiz is played once until an admin resets
// it).
func (s *Service) Join(
	ctx context.Context, joinCode string, playerID int64, displayName string, petname func() string,
) (*Player, error) {
	sess, err := s.store.GetSessionByJoinCode(ctx, normalizeJoinCode(joinCode))
	if err != nil {
		return nil, fmt.Errorf(errGetSessionByCodeFmt, err)
	}

	// The lobby closes at start: v1 has no late join. A player already on the
	// roster may still re-join once the session has left the lobby - that is a
	// reconnect/resume (their row is revived), not a late join - so only a
	// player with no existing roster row is rejected past the lobby.
	if sess.Phase != PhaseLobby && !rosterHasPlayer(sess.Players, playerID) {
		return nil, ErrLobbyClosed
	}

	played, err := s.store.PlayerFinishedSessionForQuiz(ctx, playerID, sess.QuizID)
	if err != nil {
		return nil, fmt.Errorf("failed to check prior play: %w", err)
	}
	if played {
		return nil, ErrAlreadyPlayed
	}

	player, err := s.addPlayerWithPetnameFallback(ctx, sess.ID, playerID, displayName, petname)
	if err != nil {
		return nil, err
	}

	// A new roster row changes the lobby, so signal subscribers to re-GET.
	s.publish(sess.JoinCode, sess.Phase)

	return player, nil
}

// SetReady toggles the participant's ready flag in the session identified
// by join code. Returns [ErrSessionNotFound] when the code is unknown and
// [ErrNotParticipant] when the caller has not joined.
func (s *Service) SetReady(ctx context.Context, joinCode string, playerID int64, ready bool) error {
	sess, err := s.store.GetSessionByJoinCode(ctx, normalizeJoinCode(joinCode))
	if err != nil {
		return fmt.Errorf(errGetSessionByCodeFmt, err)
	}

	if err = s.store.SetReady(ctx, sess.ID, playerID, ready); err != nil {
		return fmt.Errorf("failed to set ready: %w", err)
	}

	// A flipped ready flag changes the lobby, so signal subscribers to re-GET.
	s.publish(sess.JoinCode, sess.Phase)

	return nil
}

// Start is the host's override to begin the game immediately, bypassing the
// auto-start ready window. Only the host may call it. Marks the session
// started and hands it to the runner to enter the first round at once.
// Returns [ErrSessionNotFound] for an unknown code, [ErrNotHost] when the
// caller is not the host, and [ErrSessionAlreadyStarted] when the session has
// already left the lobby (treated as an idempotent no-op by the handler).
func (s *Service) Start(ctx context.Context, joinCode string, hostPlayerID int64) error {
	sess, err := s.store.GetSessionByJoinCode(ctx, normalizeJoinCode(joinCode))
	if err != nil {
		return fmt.Errorf(errGetSessionByCodeFmt, err)
	}
	if sess.HostPlayerID != hostPlayerID {
		return ErrNotHost
	}

	won, err := s.store.MarkStarted(ctx, sess.ID)
	if err != nil {
		return fmt.Errorf("failed to mark session started: %w", err)
	}
	if !won {
		return ErrSessionAlreadyStarted
	}

	if s.advancer != nil {
		s.advancer.Begin(ctx, sess.ID)
	}

	return nil
}

// SubmitAnswer records the caller's pick for the session's current question.
// The pick is validated against the live question (the option must belong to
// it and the answer window must be open) and stored without its correctness
// being surfaced - the runner scores it at close. Returns [ErrSessionNotFound]
// for an unknown code, [ErrNotParticipant] when the caller has not joined,
// and [ErrQuestionNotOpen] when no question is currently accepting answers or
// the option is not part of it.
func (s *Service) SubmitAnswer(
	ctx context.Context, joinCode string, playerID, optionID int64, answeredAt time.Time,
) error {
	sess, err := s.store.GetSessionByJoinCode(ctx, normalizeJoinCode(joinCode))
	if err != nil {
		return fmt.Errorf(errGetSessionByCodeFmt, err)
	}
	if !s.isParticipant(sess, playerID) {
		return ErrNotParticipant
	}
	if sess.Phase != PhaseQuestion || sess.CurrentQuestionID == nil || sess.QuestionExpiresAt == nil {
		return ErrQuestionNotOpen
	}
	if answeredAt.After(*sess.QuestionExpiresAt) {
		return ErrQuestionNotOpen
	}

	question, err := s.currentQuizQuestion(ctx, sess)
	if err != nil {
		return err
	}
	if !optionBelongsToQuestion(question, optionID) {
		return ErrQuestionNotOpen
	}

	if err = s.store.RecordAnswer(ctx, sess.ID, *sess.CurrentQuestionID, playerID, optionID, answeredAt); err != nil {
		return fmt.Errorf("failed to record session answer: %w", err)
	}

	// A new pick changes the answered-order view, so signal subscribers to
	// re-GET. The runner closes the question on the next beat if everyone has
	// now answered.
	s.publish(sess.JoinCode, sess.Phase)

	return nil
}

// GetLobbyState returns the authoritative lobby state for the session
// identified by join code: the session (with its roster) plus the quiz
// metadata the lobby renders. Participant-gated: only a player on the
// roster (or the host) may read it, so a stranger with the code cannot
// enumerate the room. Returns [ErrSessionNotFound] for an unknown code and
// [ErrNotParticipant] when the caller has not joined.
func (s *Service) GetLobbyState(ctx context.Context, joinCode string, playerID int64) (*LobbyState, error) {
	sess, err := s.store.GetSessionByJoinCode(ctx, normalizeJoinCode(joinCode))
	if err != nil {
		return nil, fmt.Errorf(errGetSessionByCodeFmt, err)
	}

	if !s.isParticipant(sess, playerID) {
		return nil, ErrNotParticipant
	}

	qz, err := s.quizzes.GetQuiz(ctx, sess.QuizID)
	if err != nil {
		return nil, fmt.Errorf("failed to get quiz for lobby state: %w", err)
	}

	state := &LobbyState{Session: sess, Quiz: qz, Revealed: sess.Phase == PhaseReveal}
	if err = s.populateInGame(ctx, state); err != nil {
		return nil, err
	}
	if err = s.populateStandings(ctx, state); err != nil {
		return nil, err
	}

	return state, nil
}

// AuthorizeView resolves a join code to its canonical code and current
// phase, gated to participants exactly like [GetLobbyState]: only the host
// or a roster player passes. The SSE event handler (MP-2 / #679) calls this
// before subscribing so a stranger who knows or guesses the code cannot
// open an event stream and learn the session exists - it returns
// [ErrNotParticipant] (which the handler maps to 404, same as an unknown
// code) for a non-participant. The returned code is the canonical form to
// subscribe under; the phase seeds the stream's initial tick.
func (s *Service) AuthorizeView(ctx context.Context, joinCode string, playerID int64) (string, Phase, error) {
	sess, err := s.store.GetSessionByJoinCode(ctx, normalizeJoinCode(joinCode))
	if err != nil {
		return "", "", fmt.Errorf(errGetSessionByCodeFmt, err)
	}

	if !s.isParticipant(sess, playerID) {
		return "", "", ErrNotParticipant
	}

	return sess.JoinCode, sess.Phase, nil
}

// TouchLastSeen refreshes the participant's last_seen_at heartbeat for the
// session identified by join code, marking them active. The SSE events
// handler calls it when the connection opens and periodically while it is
// held, so a dropped player goes stale and the runner stops counting them as
// active. Returns [ErrNotParticipant] when the caller has no roster row in
// the session.
func (s *Service) TouchLastSeen(ctx context.Context, joinCode string, playerID int64) error {
	if err := s.store.TouchLastSeen(ctx, normalizeJoinCode(joinCode), playerID); err != nil {
		return fmt.Errorf("failed to touch session player last seen: %w", err)
	}

	return nil
}

// Leave drops the calling player from the session identified by join code:
// it stamps left_at so the player falls out of the live reads (roster,
// answered-order badges, standings) and publishes a tick so the host/TV
// surface re-GETs the now-smaller state. Distinct from heartbeat staleness,
// which only stops a dropped player stalling a question - a left player is
// gone from every surface immediately. Returns [ErrSessionNotFound] for an
// unknown code and [ErrNotParticipant] when the caller holds no active roster
// row (which also makes a repeat leave an idempotent no-op).
func (s *Service) Leave(ctx context.Context, joinCode string, playerID int64) error {
	sess, err := s.store.GetSessionByJoinCode(ctx, normalizeJoinCode(joinCode))
	if err != nil {
		return fmt.Errorf(errGetSessionByCodeFmt, err)
	}

	if err = s.store.MarkPlayerLeft(ctx, sess.JoinCode, playerID); err != nil {
		return fmt.Errorf("failed to mark player left: %w", err)
	}

	// The roster shrank, so signal subscribers to re-GET the smaller state.
	s.publish(sess.JoinCode, sess.Phase)

	return nil
}

// currentQuizQuestion loads the quiz question the session is currently
// running. Returns [ErrQuestionNotOpen] when the current question id no
// longer resolves against the quiz (a deleted question mid-game).
func (s *Service) currentQuizQuestion(ctx context.Context, sess *Session) (*quiz.Question, error) {
	qz, err := s.quizzes.GetQuiz(ctx, sess.QuizID)
	if err != nil {
		return nil, fmt.Errorf("failed to get quiz for answer: %w", err)
	}
	for _, q := range qz.Questions {
		if q.ID == *sess.CurrentQuestionID {
			return q, nil
		}
	}

	return nil, ErrQuestionNotOpen
}

// populateInGame fills CurrentQuestion + Answers when the session is running a
// question (the question and reveal phases). Pre-reveal the answers carry no
// correctness; the handler still strips the option correct flag too. Leaves
// the fields nil in the lobby / round_intro / finished phases, which have no
// live question.
func (s *Service) populateInGame(ctx context.Context, state *LobbyState) error {
	sess := state.Session
	if sess.CurrentQuestionID == nil {
		return nil
	}
	if sess.Phase != PhaseQuestion && sess.Phase != PhaseReveal {
		return nil
	}

	for _, q := range state.Quiz.Questions {
		if q.ID == *sess.CurrentQuestionID {
			state.CurrentQuestion = q

			break
		}
	}

	answers, err := s.store.ListAnswers(ctx, sess.ID, *sess.CurrentQuestionID)
	if err != nil {
		return fmt.Errorf("failed to list session answers for state: %w", err)
	}
	state.Answers = answers

	return nil
}

// populateStandings fills Standings with the per-player ranking the bar graph
// consumes: the round delta + running total in the round_results phase (keyed
// on the round that just finished), and the cumulative final standings in the
// finished phase. Leaves Standings nil in every other phase, which has no
// ranking to show. Ranks are stamped 1-indexed over the store's best-first
// ordering.
func (s *Service) populateStandings(ctx context.Context, state *LobbyState) error {
	sess := state.Session
	switch {
	case sess.Phase == PhaseRoundResults && sess.CurrentRoundID != nil:
		standings, err := s.store.ListRoundStandings(ctx, sess.ID, *sess.CurrentRoundID)
		if err != nil {
			return fmt.Errorf("failed to list round standings for state: %w", err)
		}
		state.Standings = rankStandings(standings)
	case sess.Phase == PhaseFinished:
		standings, err := s.store.ListFinalStandings(ctx, sess.ID)
		if err != nil {
			return fmt.Errorf("failed to list final standings for state: %w", err)
		}
		state.Standings = rankStandings(standings)
	default:
		// Every other phase carries no standings.
	}

	return nil
}

// rankStandings stamps a 1-indexed rank onto each standing in store order
// (already best-first, ties broken by display name) and returns the slice.
func rankStandings(standings []*Standing) []*Standing {
	for i, st := range standings {
		st.Rank = i + 1
	}

	return standings
}

// isParticipant reports whether playerID is the host or a roster player.
func (*Service) isParticipant(sess *Session, playerID int64) bool {
	if sess.HostPlayerID == playerID {
		return true
	}
	for _, p := range sess.Players {
		if p.PlayerID == playerID {
			return true
		}
	}

	return false
}

// optionBelongsToQuestion reports whether optionID is one of the question's
// options. The runner records picks only for the live question's own
// options so a crafted client cannot answer with an unrelated option id.
func optionBelongsToQuestion(question *quiz.Question, optionID int64) bool {
	for _, o := range question.Options {
		if o.ID == optionID {
			return true
		}
	}

	return false
}

// normalizeJoinCode canonicalises a user-entered room code before lookup.
// Codes are minted in uppercase over an ambiguity-free alphabet, but
// players read them off a TV or type them by hand, so an inbound code is
// trimmed and upper-cased rather than 404ing on a lowercase or
// space-padded entry.
func normalizeJoinCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// allocateJoinCode returns a join code not currently in use. It probes a
// freshly generated code against the store and regenerates on collision,
// up to the configured attempt budget. The probe-then-insert pattern has
// an inherent race (two creates can pass the probe with the same code);
// the store's UNIQUE constraint is the real arbiter and surfaces the loser
// as [ErrJoinCodeUnavailable] at insert time, which the handler treats as
// a retryable 500.
func (s *Service) allocateJoinCode(ctx context.Context) (string, error) {
	for range s.codeTries {
		code := s.newCode()
		exists, err := s.store.JoinCodeExists(ctx, code)
		if err != nil {
			return "", fmt.Errorf("failed to probe join code: %w", err)
		}
		if !exists {
			return code, nil
		}
	}

	return "", ErrJoinCodeUnavailable
}

// addPlayerWithPetnameFallback adds the player under the requested display
// name, falling back to petnames on a per-session display-name collision
// (mirroring the anonymous-join petname retry in EnsurePlayer) until one is
// free or the attempt budget is exhausted.
func (s *Service) addPlayerWithPetnameFallback(
	ctx context.Context, sessionID string, playerID int64, displayName string, petname func() string,
) (*Player, error) {
	player, err := s.store.AddPlayer(ctx, sessionID, playerID, displayName)
	if err == nil {
		return player, nil
	}
	if !errors.Is(err, ErrDisplayNameTaken) {
		return nil, fmt.Errorf("failed to add session player: %w", err)
	}

	for range joinCodeAttempts {
		player, err = s.store.AddPlayer(ctx, sessionID, playerID, petname())
		if err == nil {
			return player, nil
		}
		if !errors.Is(err, ErrDisplayNameTaken) {
			return nil, fmt.Errorf("failed to add session player with petname: %w", err)
		}
	}

	return nil, fmt.Errorf("failed to add session player after petname retries: %w", err)
}

// publish fans out a session tick if a publisher is wired. The single
// choke point through which the lobby mutations (Join, SetReady) signal
// "state moved; re-GET /state" - keeping the bump-and-fan-out in one place
// so a later mutation (MP-5+) cannot forget to publish.
func (s *Service) publish(code string, phase Phase) {
	if s.publisher == nil {
		return
	}
	s.publisher.Publish(code, phase)
}
