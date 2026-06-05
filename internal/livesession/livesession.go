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
)

// Phase is the server-authoritative state-machine label for a session.
// Only [PhaseLobby] exists in MP-1; the later gameplay phases (round
// intro, question, reveal, results, finished) land in MP-5. The DB CHECK
// on sessions.phase enforces the same (currently single-value) set.
type Phase string

// Session phases. Only PhaseLobby exists today (MP-1 / #678).
const (
	PhaseLobby Phase = "lobby"
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
	CreatedAt    time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
	Players      []*Player
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
type LobbyState struct {
	Session *Session
	Quiz    *quiz.Quiz
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
	// AddPlayer adds (or revives) a roster row for the player under the
	// requested display name. Returns [ErrDisplayNameTaken] on a
	// per-session display-name collision so the caller can fall back to a
	// petname.
	AddPlayer(ctx context.Context, sessionID string, playerID int64, displayName string) (*Player, error)
	// SetReady toggles a participant's ready flag. Returns
	// [ErrNotParticipant] when the player has no roster row in the
	// session.
	SetReady(ctx context.Context, sessionID string, playerID int64, ready bool) error
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

// Service orchestrates the live-session use cases over the store layer and
// the quiz reader.
type Service struct {
	store     Store
	quizzes   QuizReader
	logger    *slog.Logger
	publisher Publisher
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

// Join adds the player to the session identified by join code under the
// requested display name. The display name is required; a per-session
// collision is recovered transparently by retrying with a petname, so the
// caller always lands in the lobby (decision 4 / claim-name parity). The
// chosen display name is carried on the returned [Player]. Returns
// [ErrSessionNotFound] when the code resolves to no session.
func (s *Service) Join(
	ctx context.Context, joinCode string, playerID int64, displayName string, petname func() string,
) (*Player, error) {
	sess, err := s.store.GetSessionByJoinCode(ctx, normalizeJoinCode(joinCode))
	if err != nil {
		return nil, fmt.Errorf(errGetSessionByCodeFmt, err)
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

	if !s.canView(sess, playerID) {
		return nil, ErrNotParticipant
	}

	qz, err := s.quizzes.GetQuiz(ctx, sess.QuizID)
	if err != nil {
		return nil, fmt.Errorf("failed to get quiz for lobby state: %w", err)
	}

	return &LobbyState{Session: sess, Quiz: qz}, nil
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

	if !s.canView(sess, playerID) {
		return "", "", ErrNotParticipant
	}

	return sess.JoinCode, sess.Phase, nil
}

// canView reports whether playerID may read the session's lobby: the host
// always can, and any roster player can.
func (*Service) canView(sess *Session, playerID int64) bool {
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
