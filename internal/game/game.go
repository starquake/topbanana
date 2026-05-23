// Package game contains the game domain logic.
package game

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/starquake/topbanana/internal/quiz"
)

const (
	defaultExpiration = 10 * time.Second
	// defaultRevealDelay is the wall-clock gap between issuing a
	// question and revealing the answer options. The player sees the
	// question text immediately and gets the delay "for free" to read
	// it before the per-question countdown starts — see #247. The
	// server shifts StartedAt into the future by this amount so the
	// answer window (StartedAt → ExpiredAt) starts AFTER the reveal,
	// not from the moment the question was issued.
	defaultRevealDelay      = 3 * time.Second
	maxPoints               = 1000
	defaultLeaderboardLimit = 10
)

var (
	// ErrGameNotFound is returned when a game lookup finds no matching row.
	ErrGameNotFound = errors.New("game not found")

	// ErrGameAlreadyExists is returned by [Service.CreateGame] when the
	// player already has a game (in-progress or completed) for the quiz.
	// Callers that need to render a "resume" affordance should call
	// [Service.GetGameForPlayerOnQuiz] first.
	ErrGameAlreadyExists = errors.New("game already exists for this player and quiz")

	// ErrAnswerAlreadyRecorded is returned by [GameStore.CreateAnswer]
	// when a second answer for the same (game, player, game_question)
	// trips the UNIQUE constraint. Handlers treat this as an idempotent
	// retry rather than a 500 — see [Service.SubmitAnswer] and
	// HandleAnswerPost for the recovery path (#353).
	ErrAnswerAlreadyRecorded = errors.New("answer already recorded for this question")

	// ErrNoMoreQuestions is returned by [Service.GetNextQuestion] when
	// every quiz question has already been issued for the game.
	ErrNoMoreQuestions = errors.New("no more questions")

	// ErrQuestionNotInGame is returned by [Service.SubmitAnswer] when the
	// question being answered does not belong to the supplied game.
	ErrQuestionNotInGame = errors.New("question not in game")

	// ErrOptionNotInQuestion is returned by [Service.SubmitAnswer] when
	// the option being submitted does not belong to the supplied question.
	ErrOptionNotInQuestion = errors.New("option does not belong to question")

	// ErrStartingGameNoRowsAffected is returned by [GameStore.StartGame]
	// when the UPDATE matched no rows — i.e. the game does not exist.
	ErrStartingGameNoRowsAffected = errors.New("no rows affected when starting game")
)

// Game represents a game. It is an instance of a quiz being played by a player.
type Game struct {
	ID           string
	QuizID       int64
	Quiz         *quiz.Quiz
	CreatedAt    time.Time
	StartedAt    *time.Time
	Questions    []*Question
	Participants []*Participant
}

// Player represents a player.
type Player struct {
	ID        int64
	Username  string
	Email     string
	CreatedAt time.Time
}

// Participant represents a player participating in a game. QuizID is
// denormalised from the parent game so the UNIQUE INDEX on
// game_participants (player_id, quiz_id) can enforce the
// one-attempt-per-(player, quiz) rule at the DB level (#273); callers
// populate it from the game they just created.
type Participant struct {
	ID       int64
	GameID   string
	PlayerID int64
	QuizID   int64
	JoinedAt time.Time
}

// Question represents a question in a game. It references a quiz question.
type Question struct {
	ID           int64
	GameID       string
	QuestionID   int64
	QuizQuestion *quiz.Question
	StartedAt    time.Time
	// TODO: change this to time duration like 10s instead of timestamp?
	ExpiredAt time.Time
	Answers   []*Answer
	// Position is the 1-indexed ordinal of this question in the
	// game's issued sequence ("Q 3 of 4"). Populated by
	// [Service.GetNextQuestion]; zero on Questions loaded from the
	// store for other purposes (resume probe, leaderboard pipe).
	Position int
	// Total is the count of questions in the quiz that owns this
	// game. Populated alongside Position by [Service.GetNextQuestion];
	// zero on store-loaded Questions for the same reason as above.
	Total int
}

// Answer represents an answer for a question. Answers are recorded for a specific game and player.
type Answer struct {
	ID         int64
	GameID     string
	PlayerID   int64
	QuestionID int64
	Question   *Question
	OptionID   int64
	Option     *quiz.Option
	AnsweredAt time.Time
}

// Results represents the accumulated score for each player in a game.
type Results struct {
	GameID string

	// Winner is the PlayerID with the highest score, or 0 if there is a tie or no players.
	Winner int64

	// PlayerScores maps a player's ID to their accumulated CalculateScore in the game.
	PlayerScores map[int64]int
}

// LeaderboardAnswer is a flat row used to compute a global per-quiz
// leaderboard. It carries every field [Service.CalculateScore] needs (the
// option's correctness, the question's start/expiry timestamps, and the
// answer's submission time) plus the player's username and ID for the
// leaderboard row. The store returns rows for both finished and
// in-progress games (#244).
//
// IsCompleted is no longer read by [Service.GetQuizLeaderboard] (#335
// moved the per-player Completed flag onto [LeaderboardParticipant]
// since the participants list is now the canonical entry source); the
// column stays on the wire so the store-level test can pin the
// per-game completion predicate without an end-to-end fixture.
type LeaderboardAnswer struct {
	PlayerID          int64
	Username          string
	QuestionStartedAt time.Time
	QuestionExpiredAt time.Time
	AnsweredAt        time.Time
	Correct           bool
	IsCompleted       bool
}

// LeaderboardParticipant is the minimum needed to surface a player on
// the live leaderboard before their first answer commits (#335):
// player_id and username for the row, and the same is_completed flag
// the answer rows carry so the entry can be marked in-progress. The
// store returns one of these per participant; [Service.GetQuizLeaderboard]
// uses the list as the canonical set of leaderboard entries and folds
// in the per-answer scoring inputs from
// [Store.ListAnswersForQuizLeaderboard].
type LeaderboardParticipant struct {
	PlayerID    int64
	Username    string
	IsCompleted bool
}

// LeaderboardEntry is a single row of a per-quiz leaderboard: the player's
// total score for that quiz. Rank is 1-indexed and computed before
// truncation, so the value remains meaningful for a CurrentPlayer entry
// returned outside the truncated top-N. IsCurrentPlayer is true when the
// entry belongs to the player making the request, which lets the client
// highlight the row.
//
// Completed is false when the player is still mid-quiz: the Score may
// be a partial running total (#244) or zero if the player has clicked
// Start but not yet submitted their first answer (#335). The client
// surfaces these rows as in-progress on the wire (`inProgress: true`).
type LeaderboardEntry struct {
	PlayerID        int64
	Username        string
	Score           int
	Rank            int
	IsCurrentPlayer bool
	Completed       bool
}

// LeaderboardResult bundles the truncated top-N entries with the requesting
// player's full standing, so a player who finished outside the visible
// leaderboard can still see their own score and rank. CurrentPlayer is nil
// when the player has no completed-game row for the quiz; when populated
// it carries Rank from the full (pre-truncation) ordering, even if the
// same player also appears in Entries.
type LeaderboardResult struct {
	Entries       []LeaderboardEntry
	CurrentPlayer *LeaderboardEntry
}

// Store represents a game store.
type Store interface {
	// Ping returns the status of the database connection.
	Ping(ctx context.Context) error
	GetGame(ctx context.Context, id string) (*Game, error)
	// GetGameByPlayerAndQuiz returns the most-recent game played by the
	// given player on the given quiz, with [Game.Questions] populated so
	// callers can call [Game.IsCompleted]. Returns [ErrGameNotFound] if
	// the player has no game for the quiz.
	GetGameByPlayerAndQuiz(ctx context.Context, playerID, quizID int64) (*Game, error)
	// CreateGame creates a new game.
	CreateGame(ctx context.Context, g *Game) error
	// CreateGameAndParticipant inserts a games row + matching
	// game_participants row + stamps started_at inside a single
	// transaction so a crash mid-flow can't leave an orphan game
	// (#351). On the UNIQUE(player_id, quiz_id) loser this returns
	// [ErrGameAlreadyExists] from within the txn. Preferred over
	// manually pairing CreateGame + CreateParticipant + StartGame
	// for the new-game flow.
	CreateGameAndParticipant(ctx context.Context, g *Game, p *Participant) error
	StartGame(ctx context.Context, id string) error
	CreateParticipant(ctx context.Context, p *Participant) error
	CreateQuestion(ctx context.Context, gq *Question) error
	CreateAnswer(ctx context.Context, a *Answer) error
	// ListAnswersForQuizLeaderboard returns one row per game_answer for
	// every game (finished or in-progress) of the given quiz, joined with
	// the fields the Service needs to score each answer. The
	// LeaderboardAnswer.IsCompleted flag tells the caller whether the
	// row belongs to a game that has issued every quiz question (#244).
	ListAnswersForQuizLeaderboard(ctx context.Context, quizID int64) ([]*LeaderboardAnswer, error)
	// ListParticipantsForQuizLeaderboard returns one row per player who
	// joined a game for the given quiz, with the same is_completed flag
	// that ListAnswersForQuizLeaderboard carries. The Service uses this
	// list as the canonical set of leaderboard entries (#335) so a
	// player who has clicked Start but not yet submitted an answer
	// still appears with a 0 score and the in-progress dot.
	ListParticipantsForQuizLeaderboard(ctx context.Context, quizID int64) ([]*LeaderboardParticipant, error)
	// DeleteGamesForPlayerOnQuiz hard-deletes every game (and dependent
	// rows) that belongs to the given player on the given quiz. No error
	// when the player has no games for the quiz: the admin reset flow is
	// idempotent.
	DeleteGamesForPlayerOnQuiz(ctx context.Context, playerID, quizID int64) error
	// ListQuizIDsForPlayer returns the distinct quiz IDs where the player
	// has at least one recorded answer. Used by the claim-name flow to
	// fan out a leaderboard republish on every quiz the player appears
	// on.
	ListQuizIDsForPlayer(ctx context.Context, playerID int64) ([]int64, error)
}

// LeaderboardPublisher is the tiny seam Service uses to signal that a
// quiz's leaderboard has moved. Implemented by *leaderboard.Hub in
// production; nil-by-default so tests that don't care about streaming
// don't have to wire anything up.
type LeaderboardPublisher interface {
	Publish(quizID int64)
}

// Service exposes the quiz-gameplay use cases on top of the store layer
// (game + quiz). Holds a logger and an optional LeaderboardPublisher.
type Service struct {
	store                Store
	quizStore            quiz.Store
	logger               *slog.Logger
	leaderboardPublisher LeaderboardPublisher
	revealDelay          time.Duration
}

// NewService initializes and returns a new instance of Service with the provided game and quiz stores.
func NewService(gameStore Store, quizStore quiz.Store, logger *slog.Logger) *Service {
	return &Service{
		store:       gameStore,
		quizStore:   quizStore,
		logger:      logger,
		revealDelay: defaultRevealDelay,
	}
}

// GetQuiz proxies to the wrapped quiz store. Exposed so clientapi
// handlers can apply the #103 visibility gate without taking a separate
// quiz.Store parameter (every leaderboard / my-game / create-game
// handler already needs the *Service).
func (s *Service) GetQuiz(ctx context.Context, id int64) (*quiz.Quiz, error) {
	qz, err := s.quizStore.GetQuiz(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get quiz %d: %w", id, err)
	}

	return qz, nil
}

// SetRevealDelay overrides the per-question reveal beat (#247). The default
// is 3 s — long enough to read the prompt before the option buttons appear.
// E2E and load-test deployments shrink this to a few hundred ms to speed up
// runs without losing the visual reveal phase.
//
// Not safe for concurrent use: must be called during startup wiring, before
// the service is handed to any HTTP handler that may invoke GetNextQuestion.
func (s *Service) SetRevealDelay(d time.Duration) {
	s.revealDelay = d
}

// SetLeaderboardPublisher wires a publisher invoked on every successful
// SubmitAnswer so SSE subscribers (or any other listener) learn about
// score changes. Optional — Service works fine without one.
//
// Not safe for concurrent use: must be called during startup wiring,
// before the service is handed to any HTTP handler that may invoke
// SubmitAnswer. There is no in-flight reconfiguration use case for
// this field; if one ever appears, swap the bare field for an
// atomic.Pointer.
func (s *Service) SetLeaderboardPublisher(p LeaderboardPublisher) {
	s.leaderboardPublisher = p
}

// PublishLeaderboardForPlayer fans out a leaderboard tick on every
// quiz where the given player has at least one answer. The claim-name
// flow calls this after a successful rename so all SSE subscribers see
// the new display name on the player's existing row without waiting
// for the next answer-submit publish.
//
// The store lookup error is returned to the caller; per-publish steps
// are best-effort (the publisher is nil-tolerant and the Publish call
// itself never returns).
func (s *Service) PublishLeaderboardForPlayer(ctx context.Context, playerID int64) error {
	if s.leaderboardPublisher == nil {
		return nil
	}
	quizIDs, err := s.store.ListQuizIDsForPlayer(ctx, playerID)
	if err != nil {
		return fmt.Errorf("list quiz IDs for player %d: %w", playerID, err)
	}
	for _, quizID := range quizIDs {
		s.leaderboardPublisher.Publish(quizID)
	}

	return nil
}

// hasParticipant reports whether playerID is one of the game's
// participants. Used by the service entry points to gate gameID-keyed
// reads and writes on participant membership (#272) so a stranger who
// somehow obtains another player's gameID cannot probe or mutate that
// game.
func hasParticipant(g *Game, playerID int64) bool {
	for _, p := range g.Participants {
		if p.PlayerID == playerID {
			return true
		}
	}

	return false
}

// IsCompleted reports whether the game has had every quiz question issued.
// A question that was issued but never answered still counts as "asked"
// because it has a [Question] row, matching [Service.GetNextQuestion]'s
// existing semantics. Requires [Game.Quiz] to be populated; an unpopulated
// Quiz returns false.
func (g *Game) IsCompleted() bool {
	if g.Quiz == nil {
		return false
	}

	return len(g.Questions) >= len(g.Quiz.Questions) && len(g.Quiz.Questions) > 0
}

// HasOpenQuestion reports whether the most recently issued question for
// this game is still resumable: unanswered, with the answer window not
// yet closed. The HTTP resume probe (/my-game, #310) treats a game with
// an open question as "not completed" even when every quiz question
// has already been issued, so a reload on the final question lands
// back on the question rather than the post-game leaderboard.
func (g *Game) HasOpenQuestion() bool {
	if len(g.Questions) == 0 {
		return false
	}
	latest := g.Questions[len(g.Questions)-1]

	return len(latest.Answers) == 0 && time.Now().Before(latest.ExpiredAt)
}

// CreateGame creates a new game with the specified quiz and player, linking
// the player and starting the game immediately. Returns the newly created
// game or an error if the operation fails.
//
// Returns [ErrGameAlreadyExists] when the player already has a game for the
// quiz. The fast-path check via GetGameByPlayerAndQuiz keeps the error
// friendly for sequential callers; the authoritative enforcement lives on
// the UNIQUE INDEX on game_participants (player_id, quiz_id) introduced by
// the 20260520180000 migration (#273). A second concurrent call that races
// past the check surfaces as a UNIQUE constraint failure on
// CreateParticipant, which the store translates back into
// ErrGameAlreadyExists — same return shape from either path.
func (s *Service) CreateGame(ctx context.Context, quizID, playerID int64) (*Game, error) {
	qz, err := s.quizStore.GetQuiz(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to get quiz: %w", err)
	}

	existing, err := s.store.GetGameByPlayerAndQuiz(ctx, playerID, qz.ID)
	if err != nil && !errors.Is(err, ErrGameNotFound) {
		return nil, fmt.Errorf("failed to check existing game: %w", err)
	}
	if existing != nil {
		return nil, ErrGameAlreadyExists
	}

	// CreateGame + CreateParticipant + StartGame run in a single
	// transaction (#351) so a crash mid-flow can't leave an orphan
	// games row. The UNIQUE(player_id, quiz_id) loser surfaces as
	// ErrGameAlreadyExists from inside the txn.
	g := &Game{QuizID: qz.ID}
	pa := &Participant{PlayerID: playerID, QuizID: qz.ID}
	if err = s.store.CreateGameAndParticipant(ctx, g, pa); err != nil {
		if errors.Is(err, ErrGameAlreadyExists) {
			return nil, ErrGameAlreadyExists
		}

		return nil, fmt.Errorf("failed to create game and participant: %w", err)
	}
	g.Quiz = qz

	// Repaint subscribers so the new participant appears on the live
	// leaderboard at score 0 / in-progress immediately (#335). Without
	// this fire, existing subscribers (hosts watching the start screen,
	// other players on the same quiz) would only see the row once the
	// player committed their first answer. Nil-guarded to match
	// PublishLeaderboardForPlayer / SubmitAnswer — tests can construct
	// a Service without wiring a publisher.
	if s.leaderboardPublisher != nil {
		s.leaderboardPublisher.Publish(qz.ID)
	}

	return g, nil
}

// GetGameForPlayerOnQuiz returns the player's most-recent game for the given
// quiz with [Game.Quiz] populated so callers can call [Game.IsCompleted].
//
// Returns [ErrGameNotFound] when the player has no game for the quiz, and
// [quiz.ErrQuizNotFound] when the quiz itself does not exist.
func (s *Service) GetGameForPlayerOnQuiz(ctx context.Context, playerID, quizID int64) (*Game, error) {
	// Verify the quiz exists first so callers can map ErrQuizNotFound to
	// a 404 distinct from "no game yet".
	qz, err := s.quizStore.GetQuiz(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to load quiz for player resume: %w", err)
	}

	g, err := s.store.GetGameByPlayerAndQuiz(ctx, playerID, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to load game for player resume: %w", err)
	}

	g.Quiz = qz

	return g, nil
}

// ResetGamesForPlayerOnQuiz hard-deletes every game (and dependent rows) the
// given player has for the given quiz. The reset is idempotent: running it
// against a (player, quiz) with no games is a no-op success so the admin
// reset button can be pressed safely from any state.
//
// Returns [quiz.ErrQuizNotFound] when the quiz does not exist so the admin
// route can map it to a 404.
func (s *Service) ResetGamesForPlayerOnQuiz(ctx context.Context, playerID, quizID int64) error {
	// Existence-only check: we don't need the quiz's questions or options,
	// so use QuizExists to skip the per-question/per-option fan-out reads
	// GetQuiz performs.
	exists, err := s.quizStore.QuizExists(ctx, quizID)
	if err != nil {
		return fmt.Errorf("failed to check quiz exists for reset: %w", err)
	}
	if !exists {
		return quiz.ErrQuizNotFound
	}

	if err := s.store.DeleteGamesForPlayerOnQuiz(ctx, playerID, quizID); err != nil {
		return fmt.Errorf("failed to delete games for player %d on quiz %d: %w", playerID, quizID, err)
	}

	return nil
}

// GetNextQuestion retrieves the next unanswered question for the specified
// game or returns nil if all are answered. Requires playerID to identify
// the caller — the participant gate (#272) makes the method 404 for any
// player who is not a `game_participants` row on the supplied gameID, so
// the gameID alone is no longer a capability.
//
// Idempotent while the answer window is still open: if the most-recently
// issued question has no answer and ExpiredAt is still in the future,
// the same question is returned without inserting a new game_questions
// row. A reload (e.g. mobile pull-to-refresh) resumes on the same
// question with the original StartedAt/ExpiredAt anchor, so the timer
// keeps ticking from where it left off. Once ExpiredAt passes the
// advance path runs as usual, so a client-side timeout call still
// moves the game forward.
func (s *Service) GetNextQuestion(ctx context.Context, gameID string, playerID int64) (*Question, error) {
	// Get the game
	g, err := s.store.GetGame(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("failed to get game: %w", err)
	}

	// Participant gate (#272): non-participants get ErrGameNotFound so
	// the error path is indistinguishable from a genuinely missing
	// game — the gameID stays opaque to outsiders.
	if !hasParticipant(g, playerID) {
		return nil, ErrGameNotFound
	}

	// Get the quiz
	qz, err := s.quizStore.GetQuiz(ctx, g.QuizID)
	if err != nil {
		return nil, fmt.Errorf("failed to get quiz: %w", err)
	}

	g.Quiz = qz

	// Resume path: when the latest issued game_question is unanswered
	// and the answer window is still open, hand back the same row so a
	// reload doesn't skip the question.
	if gq := resumeCandidate(g, qz); gq != nil {
		return gq, nil
	}

	// Create a lookup map for questions already asked in this game
	askedQuestions := make(map[int64]bool)
	for _, gqs := range g.Questions {
		askedQuestions[gqs.QuestionID] = true
	}

	var nextQuestion *quiz.Question
	// Find the first question in the quiz that hasn't been asked yet
	for _, q := range qz.Questions {
		if !askedQuestions[q.ID] {
			nextQuestion = q

			break
		}
	}

	if nextQuestion == nil {
		return nil, ErrNoMoreQuestions
	}

	// Register the chosen quiz question as a GameQuestion. The answer
	// window (StartedAt → ExpiredAt) is anchored at now + revealDelay,
	// not "now" — the reveal delay gives the player a brief beat to
	// read the question before the option buttons appear (#247).
	// Submissions before StartedAt are scored as if they arrived AT
	// StartedAt (see CalculateScore's clamp).
	revealAt := time.Now().Add(s.revealDelay)
	gq := &Question{
		GameID:       gameID,
		QuestionID:   nextQuestion.ID,
		QuizQuestion: nextQuestion,
		StartedAt:    revealAt,
		ExpiredAt:    revealAt.Add(resolveAnswerWindow(nextQuestion, qz)),
		// Position counts the newly-issued question itself, so it's
		// the prior asked count + 1 (the player just received this
		// question; previous answers were the N-1 before it).
		Position: len(g.Questions) + 1,
		Total:    len(qz.Questions),
	}
	if err = s.store.CreateQuestion(ctx, gq); err != nil {
		return nil, fmt.Errorf("failed to record game question: %w", err)
	}

	return gq, nil
}

// resumeCandidate returns the most recently issued game_question for
// the game when it can be handed back as-is (unanswered, answer window
// still open, quiz question still on the quiz). Returns nil when the
// caller should advance to the next question instead — including the
// defensive case where the latest row points at a quiz question that
// no longer exists (admin edited the quiz mid-game), in which case
// the advance branch will issue the next valid question.
//
// The returned Question is a shallow copy of the store-loaded row;
// callers that iterate g.Questions afterwards keep seeing the
// untouched store values (Position/Total zero, QuizQuestion nil), so
// the invariant documented on those fields stays honest.
func resumeCandidate(g *Game, qz *quiz.Quiz) *Question {
	if !g.HasOpenQuestion() {
		return nil
	}
	latest := g.Questions[len(g.Questions)-1]
	qq := findQuizQuestion(qz, latest.QuestionID)
	if qq == nil {
		return nil
	}
	resumed := *latest
	resumed.QuizQuestion = qq
	resumed.Position = len(g.Questions)
	resumed.Total = len(qz.Questions)

	return &resumed
}

// findQuizQuestion returns the quiz question with the given ID, or nil
// if no such question exists on the quiz.
func findQuizQuestion(qz *quiz.Quiz, questionID int64) *quiz.Question {
	for _, q := range qz.Questions {
		if q.ID == questionID {
			return q
		}
	}

	return nil
}

// SubmitAnswer records an answer from a player for a specific question in a game.
// It validates that the game exists and the question belongs to the game before saving the answer.
// Returns the saved answer or nil if the question was not found in the game.
// Returns an error if the operation fails.
//
// tappedAt is what the player's client claims as the moment of the tap.
// We clamp it to [question.StartedAt, serverNow] so an honest player on
// a slow link gets the network latency refunded (their tappedAt pulls
// the recorded AnsweredAt earlier than the receive time), while a
// dishonest or clock-skewed client can't claim a moment outside the
// window: anything before StartedAt or after serverNow falls back to
// serverNow. The asymmetry is intentional — claims can only ever make
// the recorded time earlier, never later (#237).
func (s *Service) SubmitAnswer(
	ctx context.Context,
	gameID string,
	playerID, questionID, optionID int64,
	tappedAt time.Time,
) (*Answer, error) {
	g, err := s.store.GetGame(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("failed to get game: %w", err)
	}

	// Participant gate (#272): non-participants get ErrGameNotFound. The
	// answer-post path previously trusted the (gameID, playerID) pair the
	// caller supplied, so a third party could land an answer row in
	// someone else's game.
	if !hasParticipant(g, playerID) {
		return nil, ErrGameNotFound
	}

	question, option, err := s.resolveAnswerTarget(ctx, g, gameID, questionID, optionID)
	if err != nil {
		return nil, err
	}

	a := &Answer{
		GameID:     gameID,
		PlayerID:   playerID,
		QuestionID: question.ID,
		Question:   question,
		OptionID:   optionID,
		Option:     option,
		AnsweredAt: clampTappedAt(tappedAt, question.StartedAt, time.Now()),
	}

	if err = s.store.CreateAnswer(ctx, a); err != nil {
		// Pass ErrAnswerAlreadyRecorded through unwrapped so the
		// handler can map it to 409 instead of 500 — a double-tap is
		// a retry, not a server fault (#353).
		if errors.Is(err, ErrAnswerAlreadyRecorded) {
			return nil, ErrAnswerAlreadyRecorded
		}

		return nil, fmt.Errorf("failed to create answer: %w", err)
	}

	// Signal SSE subscribers that the leaderboard has moved. Non-blocking
	// (the hub buffers one event per subscriber and drops on backpressure),
	// so this never delays the answer-submit response.
	if s.leaderboardPublisher != nil {
		s.leaderboardPublisher.Publish(g.QuizID)
	}

	return a, nil
}

// resolveAnswerWindow picks the per-question answer window from #99's
// priority chain: the question's own time_limit_seconds wins; falling
// back to the quiz default; falling back to defaultExpiration when both
// are unset or zero. Returning a [time.Duration] keeps the call site
// arithmetic identical to the prior hard-coded path.
func resolveAnswerWindow(q *quiz.Question, qz *quiz.Quiz) time.Duration {
	if q != nil && q.TimeLimitSeconds != nil && *q.TimeLimitSeconds > 0 {
		return time.Duration(*q.TimeLimitSeconds) * time.Second
	}
	if qz != nil && qz.TimeLimitSeconds > 0 {
		return time.Duration(qz.TimeLimitSeconds) * time.Second
	}

	return defaultExpiration
}

// clampTappedAt applies the #237 trust window: the recorded answer time
// is the client-supplied tappedAt when it falls inside [startedAt,
// serverNow], otherwise it's serverNow. The fallback is intentionally
// the upper bound — an out-of-range claim should never give the player
// a faster score than they earned in real time.
func clampTappedAt(tappedAt, startedAt, serverNow time.Time) time.Time {
	if tappedAt.IsZero() || tappedAt.Before(startedAt) || tappedAt.After(serverNow) {
		return serverNow
	}

	return tappedAt
}

// GetResults calculates the accumulated score for each player in a game and
// returns the results. Requires playerID for the participant gate (#272);
// non-participants get ErrGameNotFound so the gameID itself can't be used
// to read the score map of a game the caller is not in.
func (s *Service) GetResults(ctx context.Context, gameID string, playerID int64) (*Results, error) {
	g, err := s.store.GetGame(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("failed to get game: %w", err)
	}

	if !hasParticipant(g, playerID) {
		return nil, ErrGameNotFound
	}

	// Collect all option IDs needed across all answers in one pass.
	var optionIDs []int64
	for _, gqs := range g.Questions {
		for _, ga := range gqs.Answers {
			optionIDs = append(optionIDs, ga.OptionID)
		}
	}

	options, err := s.quizStore.GetOptionsByIDs(ctx, optionIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to get options: %w", err)
	}

	optionsByID := make(map[int64]*quiz.Option, len(options))
	for _, o := range options {
		optionsByID[o.ID] = o
	}

	plsMap := make(map[int64]int, len(g.Participants))
	for _, gqs := range g.Questions {
		for _, ga := range gqs.Answers {
			ga.Question = gqs
			ga.Option = optionsByID[ga.OptionID]
			plsMap[ga.PlayerID] += s.CalculateScore(ctx, ga)
		}
	}

	var winner int64
	topScore := -1
	for playerID, score := range plsMap {
		if score > topScore {
			topScore = score
			winner = playerID
		} else if score == topScore {
			winner = 0
		}
	}

	return &Results{GameID: g.ID, Winner: winner, PlayerScores: plsMap}, nil
}

// GetQuizLeaderboard returns the top scoring players for the given quiz.
// Scoring reuses [Service.CalculateScore] so values stay consistent with
// [Service.GetResults].
//
// Every player who has joined a game for the quiz appears on the
// leaderboard, including those still mid-quiz (running partial score,
// [LeaderboardEntry.Completed] = false) and those who have clicked
// Start but not yet submitted their first answer (score 0,
// [LeaderboardEntry.Completed] = false). The
// one-attempt-per-(player, quiz) constraint enforced by
// [Service.CreateGame] and the admin reset flow keeps each player to
// at most one row.
//
// Ordering: descending by score; ties are broken by ascending username for
// determinism so a tied scoreboard is stable across requests.
//
// currentPlayerID flags the entry that belongs to the requesting player so
// the client can highlight it; pass 0 to flag nothing. The same id drives
// the returned CurrentPlayer standing so a player who landed outside the
// truncated top-N can still see their own score and rank — see #181.
//
// If limit <= 0 it defaults to 10. The quiz must exist; missing quizzes
// surface as [quiz.ErrQuizNotFound].
func (s *Service) GetQuizLeaderboard(
	ctx context.Context, quizID, currentPlayerID int64, limit int,
) (*LeaderboardResult, error) {
	if limit <= 0 {
		limit = defaultLeaderboardLimit
	}

	// Verify the quiz exists so callers can map ErrQuizNotFound to a 404.
	// Cheap existence check — leaderboard rendering does not need the
	// quiz's questions or options.
	exists, err := s.quizStore.QuizExists(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to check quiz exists for leaderboard: %w", err)
	}
	if !exists {
		return nil, quiz.ErrQuizNotFound
	}

	// Participants is the canonical set of leaderboard entries (#335):
	// every player who joined a game for this quiz, including those who
	// have not submitted an answer yet. The answers query below only
	// contributes per-row scoring inputs that roll up into each entry's
	// running total.
	participants, err := s.store.ListParticipantsForQuizLeaderboard(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to list leaderboard participants: %w", err)
	}

	rows, err := s.store.ListAnswersForQuizLeaderboard(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to list leaderboard answers: %w", err)
	}

	playerTotals := make(map[int64]int)
	for _, r := range rows {
		// Synthesise just enough of an *Answer / *Question / *quiz.Option
		// for CalculateScore. The formula touches only Option.Correct,
		// Question.StartedAt, Question.ExpiredAt, and Answer.AnsweredAt.
		a := &Answer{
			AnsweredAt: r.AnsweredAt,
			Question: &Question{
				StartedAt: r.QuestionStartedAt,
				ExpiredAt: r.QuestionExpiredAt,
			},
			Option: &quiz.Option{Correct: r.Correct},
		}
		playerTotals[r.PlayerID] += s.CalculateScore(ctx, a)
	}

	entries := make([]LeaderboardEntry, 0, len(participants))
	for _, p := range participants {
		entries = append(entries, LeaderboardEntry{
			PlayerID:        p.PlayerID,
			Username:        p.Username,
			Score:           playerTotals[p.PlayerID],
			IsCurrentPlayer: p.PlayerID == currentPlayerID,
			Completed:       p.IsCompleted,
		})
	}

	slices.SortFunc(entries, func(a, b LeaderboardEntry) int {
		// Higher scores first; ties broken by ascending username.
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}

		return strings.Compare(a.Username, b.Username)
	})

	return finalizeLeaderboardInPlace(entries, currentPlayerID, limit), nil
}

// finalizeLeaderboardInPlace stamps 1-indexed rank on every entry, extracts the
// current player's standing from the full ordering (so a player outside
// the visible top-N still gets a Rank that matches their global position),
// and then truncates entries to the requested limit. Split out of
// GetQuizLeaderboard to keep that function under the project's per-function
// length budget; the steps need to run in this order — ranks must be stamped
// before the CurrentPlayer copy or it gets a zero rank, and the truncation
// must come after both or the off-leaderboard player vanishes.
//
// The entries slice is mutated in place (rank field writes + sub-slicing);
// callers must not retain the original slice after invocation.
func finalizeLeaderboardInPlace(entries []LeaderboardEntry, currentPlayerID int64, limit int) *LeaderboardResult {
	for i := range entries {
		entries[i].Rank = i + 1
	}

	var currentPlayer *LeaderboardEntry
	if currentPlayerID != 0 {
		for i := range entries {
			if entries[i].PlayerID == currentPlayerID {
				cp := entries[i]
				currentPlayer = &cp

				break
			}
		}
	}

	if len(entries) > limit {
		entries = entries[:limit]
	}

	return &LeaderboardResult{Entries: entries, CurrentPlayer: currentPlayer}
}

// CalculateScore calculates the score for a given answer.
func (s *Service) CalculateScore(ctx context.Context, a *Answer) int {
	// TODO: Should this be the points for answering immediately? Or within one second?

	if !a.Option.Correct {
		return 0
	}

	if a.AnsweredAt.After(a.Question.ExpiredAt) {
		s.logger.InfoContext(ctx, "score=0, a.AnsweredAt > question.ExpiredAt, answered too late!")

		return 0
	}

	answerWindow := a.Question.ExpiredAt.Sub(a.Question.StartedAt)
	duration := max(
		// Defensive clamp: a hand-crafted client could POST an answer
		// before StartedAt (which sits in the future due to the reveal
		// delay — #247). Without clamping, a negative duration would
		// score above maxPoints. Treat early arrivals as if they landed
		// at StartedAt.
		a.AnsweredAt.Sub(a.Question.StartedAt), 0)

	score := int(float64(maxPoints) - (duration.Seconds() / answerWindow.Seconds() * float64(maxPoints)))

	return score
}

// resolveAnswerTarget finds the issued game_question for the supplied
// questionID and the option for the supplied optionID, loading the
// option set in one round-trip so [Service.SubmitAnswer] can also
// surface the correct options on a wrong-pick reveal (#233). Returns
// [ErrQuestionNotInGame] or [ErrOptionNotInQuestion] when the lookup
// misses; pulled out of SubmitAnswer to keep it under revive's
// function-length cap.
func (s *Service) resolveAnswerTarget(
	ctx context.Context, g *Game, gameID string, questionID, optionID int64,
) (*Question, *quiz.Option, error) {
	var question *Question
	for _, qs := range g.Questions {
		if qs.QuestionID == questionID {
			question = qs

			break
		}
	}
	if question == nil {
		return nil, nil, fmt.Errorf(
			"question %d not found in game %s: %w", questionID, gameID, ErrQuestionNotInGame,
		)
	}

	quizQuestion, err := s.quizStore.GetQuestion(ctx, question.QuestionID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get question: %w", err)
	}
	question.QuizQuestion = quizQuestion

	for _, o := range quizQuestion.Options {
		if o.ID == optionID {
			return question, o, nil
		}
	}

	return nil, nil, fmt.Errorf(
		"option %d not in question %d: %w",
		optionID,
		question.QuestionID,
		ErrOptionNotInQuestion,
	)
}
