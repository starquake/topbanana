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

	// ErrLobbyClosed is returned by [Service.Join] when the session has
	// already left the lobby: the lobby closes at start and v1 has no
	// late join. Handlers map it to 409.
	ErrLobbyClosed = errors.New("session lobby has closed")

	// ErrNotInLobby is returned by [Service.ArmStart] / [Service.CancelStart]
	// when the session has already left the lobby, so the last-call countdown
	// can only be armed or cancelled while the game has not begun. Handlers
	// treat it as an idempotent no-op (the host clicked after the game already
	// started). Distinct from [ErrLobbyClosed], which is the player-join gate.
	ErrNotInLobby = errors.New("session is no longer in the lobby")

	// ErrNotIntermission is returned by [Service.StartNextQuiz] when the room is
	// not sitting in the between-games intermission phase, so the host can only
	// arm the next quiz once the current game has ended (not mid-game). Handlers
	// map it to 409.
	ErrNotIntermission = errors.New("room is not in intermission")
)

// Phase is the server-authoritative state-machine label for a session.
// The runner (MP-5 / #682, MP-6 / #683) advances a session through the
// gameplay phases. The DB CHECK on sessions.phase enforces the same set.
type Phase string

// Session phases. The runner advances lobby -> round_intro -> question ->
// reveal (repeating per question) -> round_results (after the last question
// of a round) -> the next round's round_intro. A game ends at intermission
// (#836): the between-games screen where the room stays alive and the host can
// arm the next quiz, which re-arms back to lobby for the next game. A room
// reaches the terminal finished only when it is actually closed (host gone past
// the abandon timeout, or an explicit end), at which point the runner evicts it.
const (
	PhaseLobby        Phase = "lobby"
	PhaseRoundIntro   Phase = "round_intro"
	PhaseQuestion     Phase = "question"
	PhaseReveal       Phase = "reveal"
	PhaseRoundResults Phase = "round_results"
	PhaseIntermission Phase = "intermission"
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
	// DefaultStartCountdown is how long the host's armed last-call countdown
	// runs before the runner starts the game (#735). The host arms it with
	// "Start in 60s"; the service stamps the absolute deadline at now + this.
	// The e2e suite shrinks it via SESSION_START_COUNTDOWN so a spec does not
	// pay the production dwell time.
	DefaultStartCountdown = 60 * time.Second
	// AbandonTimeout is how long a started (mid-game, not finished) session may
	// go without a host heartbeat before the runner finishes it as abandoned.
	// The host beats every HeartbeatInterval (10s) over its held SSE
	// connection; 3 minutes is far longer than ActiveWindow (a player drop) on
	// purpose, since finishing the room ends the game for everyone, so it
	// tolerates a long host reconnect (laptop sleep, tab reload, network blip)
	// before giving up. Lobbies are out of scope - only a started session is
	// swept.
	AbandonTimeout = 3 * time.Minute
)

// beginTimeout bounds the detached first-round transition [Service.Start]
// hands to the runner. The transition runs on a context derived from
// [context.WithoutCancel] so a host disconnect ending the HTTP request cannot
// abandon it mid-flight and strand the session started-but-still-in-lobby
// (#781); this caps the detached work so it cannot run unbounded.
const beginTimeout = 10 * time.Second

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
	// GameSeq counts which game in the room is being played, starting at 1
	// (#836). A re-arm points the room at a new quiz and bumps it, so every
	// per-game answer read scopes to it and a re-run of the same quiz is scored
	// independently of the previous game.
	GameSeq int64
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
	// StartAt is the absolute server deadline of an armed last-call countdown
	// (#735): nil when no countdown is armed. The runner starts the game on the
	// first lobby tick at or after StartAt; the state read surfaces it so every
	// surface renders the same server-clock countdown.
	StartAt *time.Time
	// HostLastSeenAt is when the host last beat its held SSE connection; nil
	// when the host has never beat. The runner's abandon sweep ages a started
	// session from COALESCE(HostLastSeenAt, StartedAt) and finishes it once
	// host presence is older than AbandonTimeout (MP-10).
	HostLastSeenAt *time.Time
	Players        []*Player
}

// Player is one roster row: a player who joined a session, with the ready
// flag the lobby shows. DisplayName is the player's CURRENT
// players.display_name (#716), fanned out by the roster join rather than a
// per-session snapshot, so a rename shows everywhere. It is empty on the bare
// Player the AddPlayer upsert returns; only the lobby/state read populates it.
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
	// alongside the running total) and on the end-of-game screen - intermission
	// (the between-games screen, #836) and the terminal finished phase - as the
	// final standings, where RoundScore carries the last round's score so the bar
	// graph can animate that final contribution. Nil in every other phase.
	// Ordered best-first, rank stamped 1-indexed.
	Standings []*Standing
	// CurrentRound describes the round the session is about to play: its title,
	// summary, and position so the between-rounds screen names the round and can
	// word its heading correctly on the first round (#748). Populated only in the
	// round_intro phase; nil in every other phase.
	CurrentRound *RoundInfo
}

// RoundInfo describes the round shown on the round_intro screen (#748): its
// title and summary plus where it sits in the quiz, so a surface can show what
// the round is about and tell the first round (which has no previous round)
// apart from a later one. Number is 1-indexed; Total is the round count, so a
// surface knows Number == 1 means the first round.
type RoundInfo struct {
	Title   string
	Summary string
	Number  int
	Total   int
}

// Standing is one player's place in the session ranking shown between rounds
// (round_results) and at the end (finished). RoundScore is the points the
// player earned in the round that just finished; in the finished phase it
// carries the last round's score so the bar graph can animate that final
// contribution (0 for a player absent from the last round). TotalScore is their
// cumulative session score. Rank is 1-indexed over the full roster, ties broken
// by display name so the ordering is stable across reads.
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
	// AddPlayer adds (or revives) a roster row for the player. The display
	// name is no longer stored per session (#716): the roster/standings reads
	// join players and select the current players.display_name, so a rename
	// propagates everywhere. The returned Player carries no name.
	AddPlayer(ctx context.Context, sessionID string, playerID int64) (*Player, error)
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
	// started (false). Used by the host Start and the armed-countdown firing
	// so only one of them issues the first round.
	MarkStarted(ctx context.Context, sessionID string) (bool, error)
	// ArmStart stamps start_at (the absolute last-call countdown deadline) on a
	// session still in the lobby. Returns [ErrNotInLobby] when the session has
	// already left the lobby. Re-arming in the lobby overwrites the deadline.
	ArmStart(ctx context.Context, sessionID string, startAt time.Time) error
	// CancelStart clears start_at on a session still in the lobby, stopping an
	// armed countdown. Returns [ErrNotInLobby] when the session has already
	// left the lobby.
	CancelStart(ctx context.Context, sessionID string) error
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
	// Finish ends the session terminally: marks it finished and clears the
	// per-question runner columns. Used when the room is actually closed (host
	// gone past the abandon timeout, or an explicit end).
	Finish(ctx context.Context, sessionID string) error
	// Intermission ends a game without closing the room (#836): marks it
	// intermission (the between-games screen) and clears the per-question runner
	// columns, leaving the room alive so the host can arm the next quiz.
	Intermission(ctx context.Context, sessionID string) error
	// RearmSession starts the next game in a room (#836): points it at the new
	// quiz, bumps game_seq, resets to the lobby, clears the per-game runner
	// columns, and clears every roster player's ready flag. Returns
	// [ErrNotIntermission] when the room is not in the between-games intermission
	// phase (re-arming a live game or a terminally finished room is rejected).
	RearmSession(ctx context.Context, sessionID string, quizID int64) error
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
	// TouchHostLastSeen refreshes the host's host_last_seen_at, the
	// host-presence heartbeat, for the session identified by join code. Returns
	// [ErrSessionNotFound] when no session uses the code. Keyed on join code so
	// the SSE handler need only carry the code it already gates on.
	TouchHostLastSeen(ctx context.Context, joinCode string) error
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
	// finished state. RoundScore on each returned Standing is 0; the service
	// overlays the last round's score for the finished bar graph animation.
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

// QuizReader is the slice of the quiz store the service needs: load the
// full quiz (for mode + lobby metadata) and its rounds (for the round_intro
// title/summary, which GetQuiz does not load). Kept narrow so the service
// does not depend on the whole quiz.Store surface.
type QuizReader interface {
	GetQuiz(ctx context.Context, id int64) (*quiz.Quiz, error)
	ListRoundsByQuiz(ctx context.Context, quizID int64) ([]*quiz.Round, error)
}

// Publisher is the tiny seam the service uses to signal that a session's
// state has moved (MP-2 / #679). Implemented by *Hub in production;
// nil-by-default so tests that don't care about the event channel don't
// have to wire anything up. The service calls Publish after every
// successful lobby mutation (join, ready) so subscribers re-GET
// /api/sessions/{code}/state. The returned Tick is ignored by the service;
// the SSE handler is the consumer.
//
// Forget releases a session's version bookkeeping once it is terminal; the
// runner calls it after the final Publish so a finished session does not pin
// its version entry for the process lifetime.
type Publisher interface {
	Publish(code string, phase Phase) Tick
	Forget(code string)
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
	// Rearm drives the runner into the next game of a room (#836) after the
	// service has re-armed it: it drops the room's stale per-game state and
	// enters the new game's first round. Safe to call more than once; a room not
	// in the lobby is a no-op.
	Rearm(ctx context.Context, sessionID string)
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
	// startCountdown is how far in the future ArmStart stamps the last-call
	// deadline. Zero falls back to DefaultStartCountdown so a service built
	// without SetStartCountdown still arms a sane 60s countdown.
	startCountdown time.Duration
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

// SetStartCountdown overrides how far in the future [Service.ArmStart] stamps
// the last-call deadline (#735). Zero or negative leaves the default
// (DefaultStartCountdown). Same startup-only contract as [Service.SetPublisher].
func (s *Service) SetStartCountdown(d time.Duration) {
	if d <= 0 {
		return
	}
	s.startCountdown = d
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

// Join adds the player to the session identified by join code. The player is
// already named on their players row before joining (#716): an anonymous or
// unnamed player claims players.display_name through the shared claim flow
// first, and a logged-in named player keeps their account name. Join just adds
// the roster row; the displayed name comes from the players join on the
// roster/standings reads, so a rename propagates everywhere. Returns
// [ErrSessionNotFound] when the code resolves to no session and
// [ErrLobbyClosed] when the session has already left the lobby (v1 has no
// late join).
func (s *Service) Join(ctx context.Context, joinCode string, playerID int64) (*Player, error) {
	sess, err := s.store.GetSessionByJoinCode(ctx, normalizeJoinCode(joinCode))
	if err != nil {
		return nil, fmt.Errorf(errGetSessionByCodeFmt, err)
	}

	// A room accepts joins in every phase except the terminal finished state
	// (#836): a latecomer joins mid-game and simply misses the questions already
	// played, players drift in during the between-games intermission, and a
	// prior participant re-Joining at any live phase is a reconnect/resume
	// (AddPlayer revives their row, clearing left_at). Only a finished, closed
	// room rejects joins.
	if sess.Phase == PhaseFinished {
		return nil, ErrLobbyClosed
	}

	player, err := s.store.AddPlayer(ctx, sess.ID, playerID)
	if err != nil {
		return nil, fmt.Errorf("failed to add session player: %w", err)
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

// Start begins the game immediately (the host "Start now" control). Only the
// host may call it. Marks the session started and hands it to the runner to
// enter the first round at once, so it also skips (overrides) any armed
// last-call countdown. Returns [ErrSessionNotFound] for an unknown code,
// [ErrNotHost] when the caller is not the host, and [ErrSessionAlreadyStarted]
// when the session has already left the lobby (treated as an idempotent no-op
// by the handler).
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
		// Detach from the request context so a host disconnect cannot cancel
		// the first-round transition and leave the session started-but-still-in-
		// lobby. The runner's next tick is the backstop if this still fails.
		beginCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), beginTimeout)
		defer cancel()
		s.advancer.Begin(beginCtx, sess.ID)
	}

	return nil
}

// StartNextQuiz arms the room's next game (#836): the host picks a new live quiz
// and the room - currently sitting in the between-games intermission - re-arms
// onto it and begins immediately, the same start-now semantics as [Service.Start].
// Only the host may call it, and only from intermission (a mid-game call is
// rejected), so the next game cannot be armed while one is in flight. The quiz
// must exist and be mode='live' (the same gate [Service.CreateSession] applies).
// Re-arming bumps game_seq so the new game is scored independently and resets
// the roster's ready flags. Returns [ErrSessionNotFound] for an unknown code,
// [ErrNotHost] when the caller is not the host, [quiz.ErrQuizNotFound] /
// [ErrNotLiveQuiz] for an unhostable quiz, and [ErrNotIntermission] when the
// room is not between games.
func (s *Service) StartNextQuiz(ctx context.Context, joinCode string, hostPlayerID, quizID int64) error {
	sess, err := s.store.GetSessionByJoinCode(ctx, normalizeJoinCode(joinCode))
	if err != nil {
		return fmt.Errorf(errGetSessionByCodeFmt, err)
	}
	if sess.HostPlayerID != hostPlayerID {
		return ErrNotHost
	}
	if sess.Phase != PhaseIntermission {
		return ErrNotIntermission
	}

	qz, err := s.quizzes.GetQuiz(ctx, quizID)
	if err != nil {
		return fmt.Errorf("failed to get quiz for next game: %w", err)
	}
	if qz.Mode != quiz.ModeLive {
		return ErrNotLiveQuiz
	}

	// RearmSession is scoped to the intermission phase, so it is the real arbiter
	// if the room left intermission between the read above and this write (a
	// concurrent re-arm); it returns ErrNotIntermission to the loser.
	if err = s.store.RearmSession(ctx, sess.ID, qz.ID); err != nil {
		return fmt.Errorf("failed to rearm session: %w", err)
	}

	// Mark the re-armed lobby started so the new game begins now (start-now
	// semantics) and the abandon sweep ages it from this game's start.
	if _, err = s.store.MarkStarted(ctx, sess.ID); err != nil {
		return fmt.Errorf("failed to mark next game started: %w", err)
	}

	// A re-arm changes what every surface shows (new quiz, back to lobby), so
	// signal subscribers to re-GET the state before the runner drives the game.
	s.publish(sess.JoinCode, PhaseLobby)

	if s.advancer != nil {
		// Detach from the request context (same reason as Start) so a host
		// disconnect cannot strand the re-armed game in the lobby; the runner's
		// next tick is the backstop.
		rearmCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), beginTimeout)
		defer cancel()
		s.advancer.Rearm(rearmCtx, sess.ID)
	}

	return nil
}

// ArmStart arms the host's last-call countdown (the "Start in 60s" control):
// it stamps the absolute deadline at now + the configured countdown, so every
// surface renders the same server-clock countdown and the runner starts the
// game once it elapses. Host-gated and lobby-phase only. Joins during the
// countdown do not reset it (the deadline is absolute). Re-arming while in the
// lobby overwrites the deadline. Returns [ErrSessionNotFound] for an unknown
// code, [ErrNotHost] when the caller is not the host, and [ErrNotInLobby] when
// the session has already left the lobby.
func (s *Service) ArmStart(ctx context.Context, joinCode string, hostPlayerID int64, now time.Time) error {
	sess, err := s.store.GetSessionByJoinCode(ctx, normalizeJoinCode(joinCode))
	if err != nil {
		return fmt.Errorf(errGetSessionByCodeFmt, err)
	}
	if sess.HostPlayerID != hostPlayerID {
		return ErrNotHost
	}

	countdown := s.startCountdown
	if countdown <= 0 {
		countdown = DefaultStartCountdown
	}
	if err = s.store.ArmStart(ctx, sess.ID, now.Add(countdown)); err != nil {
		return fmt.Errorf("failed to arm session start: %w", err)
	}

	// An armed countdown changes what every surface shows, so signal
	// subscribers to re-GET the state (which now carries the deadline).
	s.publish(sess.JoinCode, sess.Phase)

	return nil
}

// CancelStart cancels an armed last-call countdown (the host "Cancel"
// control), clearing the deadline so the lobby returns to waiting on the host.
// Host-gated and lobby-phase only. Returns [ErrSessionNotFound] for an unknown
// code, [ErrNotHost] when the caller is not the host, and [ErrNotInLobby] when
// the session has already left the lobby.
func (s *Service) CancelStart(ctx context.Context, joinCode string, hostPlayerID int64) error {
	sess, err := s.store.GetSessionByJoinCode(ctx, normalizeJoinCode(joinCode))
	if err != nil {
		return fmt.Errorf(errGetSessionByCodeFmt, err)
	}
	if sess.HostPlayerID != hostPlayerID {
		return ErrNotHost
	}

	if err = s.store.CancelStart(ctx, sess.ID); err != nil {
		return fmt.Errorf("failed to cancel session start: %w", err)
	}

	// A cleared countdown changes what every surface shows, so signal
	// subscribers to re-GET the state.
	s.publish(sess.JoinCode, sess.Phase)

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
	if sess.Phase != PhaseQuestion ||
		sess.CurrentQuestionID == nil ||
		sess.QuestionStartedAt == nil ||
		sess.QuestionExpiresAt == nil {
		return ErrQuestionNotOpen
	}
	// The window opens at StartedAt (after the read beat) and closes at
	// ExpiresAt; a pick outside [StartedAt, ExpiresAt] is rejected, so a client
	// cannot pre-submit during the read beat.
	if answeredAt.Before(*sess.QuestionStartedAt) || answeredAt.After(*sess.QuestionExpiresAt) {
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
	if err = s.populateRoundIntro(ctx, state); err != nil {
		return nil, err
	}

	return state, nil
}

// ViewAuthorization is the result of [Service.AuthorizeView]: the canonical
// join code to subscribe under, the current phase that seeds the stream's
// initial tick, and whether the caller is the session host. The handler beats
// the host-presence heartbeat for the host and the participant heartbeat for a
// roster player, so it needs IsHost to pick which (MP-10).
type ViewAuthorization struct {
	Code   string
	Phase  Phase
	IsHost bool
}

// AuthorizeView resolves a join code to its canonical code, current phase, and
// host flag, gated to participants exactly like [GetLobbyState]: only the host
// or a roster player passes. The SSE event handler (MP-2 / #679) calls this
// before subscribing so a stranger who knows or guesses the code cannot
// open an event stream and learn the session exists - it returns
// [ErrNotParticipant] (which the handler maps to 404, same as an unknown
// code) for a non-participant.
func (s *Service) AuthorizeView(ctx context.Context, joinCode string, playerID int64) (ViewAuthorization, error) {
	sess, err := s.store.GetSessionByJoinCode(ctx, normalizeJoinCode(joinCode))
	if err != nil {
		return ViewAuthorization{}, fmt.Errorf(errGetSessionByCodeFmt, err)
	}

	if !s.isParticipant(sess, playerID) {
		return ViewAuthorization{}, ErrNotParticipant
	}

	return ViewAuthorization{
		Code:   sess.JoinCode,
		Phase:  sess.Phase,
		IsHost: sess.HostPlayerID == playerID,
	}, nil
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

// TouchHostLastSeen refreshes the host-presence heartbeat for the session
// identified by join code. The SSE events handler calls it (in place of
// TouchLastSeen) while the host's connection is held, so a host who
// disconnects mid-game goes stale and the runner's abandon sweep finishes the
// lingering session. Returns [ErrSessionNotFound] for an unknown code.
func (s *Service) TouchHostLastSeen(ctx context.Context, joinCode string) error {
	if err := s.store.TouchHostLastSeen(ctx, normalizeJoinCode(joinCode)); err != nil {
		return fmt.Errorf("failed to touch session host last seen: %w", err)
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
// on the round that just finished), and the cumulative final standings on the
// end-of-game screen. A game now ends in intermission (#836) - the between-games
// screen where the room waits for the host to arm the next quiz - so the final
// standings are shown there; the terminal finished phase (a closed room) shows
// them too. The final standings carry each player's score in the last round as
// RoundScore so the bar graph can animate the last round's contribution
// (preTotal = TotalScore - RoundScore), matching the between-rounds screen;
// players absent from the last round keep RoundScore 0 and so do not grow.
// Ranking stays by cumulative total in every phase. Leaves Standings nil in
// every other phase, which has no ranking to show. Ranks are stamped 1-indexed
// over the store's best-first ordering.
func (s *Service) populateStandings(ctx context.Context, state *LobbyState) error {
	sess := state.Session
	switch {
	case sess.Phase == PhaseRoundResults && sess.CurrentRoundID != nil:
		standings, err := s.store.ListRoundStandings(ctx, sess.ID, *sess.CurrentRoundID)
		if err != nil {
			return fmt.Errorf("failed to list round standings for state: %w", err)
		}
		state.Standings = rankStandings(standings)
	case sess.Phase == PhaseIntermission, sess.Phase == PhaseFinished:
		standings, err := s.finishedStandings(ctx, sess)
		if err != nil {
			return err
		}
		state.Standings = rankStandings(standings)
	default:
		// Every other phase carries no standings.
	}

	return nil
}

// finishedStandings returns the final standings ordered best-first by
// cumulative total, with each player's last-round score overlaid onto
// RoundScore so the finished bar graph can animate the last round's
// contribution. When the quiz has no rounds the final standings are returned
// unchanged (RoundScore 0).
func (s *Service) finishedStandings(ctx context.Context, sess *Session) ([]*Standing, error) {
	standings, err := s.store.ListFinalStandings(ctx, sess.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list final standings for state: %w", err)
	}

	rounds, err := s.quizzes.ListRoundsByQuiz(ctx, sess.QuizID)
	if err != nil {
		return nil, fmt.Errorf("failed to list rounds for final standings: %w", err)
	}
	if len(rounds) == 0 {
		return standings, nil
	}

	lastRoundID := rounds[len(rounds)-1].ID
	lastRound, err := s.store.ListRoundStandings(ctx, sess.ID, lastRoundID)
	if err != nil {
		return nil, fmt.Errorf("failed to list last round standings for state: %w", err)
	}

	lastRoundScore := make(map[int64]int, len(lastRound))
	for _, st := range lastRound {
		lastRoundScore[st.PlayerID] = st.RoundScore
	}
	for _, st := range standings {
		st.RoundScore = lastRoundScore[st.PlayerID]
	}

	return standings, nil
}

// populateRoundIntro fills CurrentRound with the round the session is about to
// play (its title, summary, and 1-indexed position), so the round_intro screen
// names the round and words its heading correctly on the first round (#748).
// Leaves CurrentRound nil in every other phase, and when the current round id
// resolves to no round (a deleted round mid-game), so the surface falls back to
// its generic copy rather than naming a stale round.
func (s *Service) populateRoundIntro(ctx context.Context, state *LobbyState) error {
	sess := state.Session
	if sess.Phase != PhaseRoundIntro || sess.CurrentRoundID == nil {
		return nil
	}

	rounds, err := s.quizzes.ListRoundsByQuiz(ctx, sess.QuizID)
	if err != nil {
		return fmt.Errorf("failed to list rounds for round intro: %w", err)
	}

	for i, r := range rounds {
		if r.ID == *sess.CurrentRoundID {
			state.CurrentRound = &RoundInfo{
				Title:   r.Title,
				Summary: r.Summary,
				Number:  i + 1,
				Total:   len(rounds),
			}

			break
		}
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
