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

// Participant represents a player participating in a game.
type Participant struct {
	ID       int64
	GameID   string
	PlayerID int64
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
// leaderboard row. The store filters these rows to completed games only
// (every quiz question issued), and the one-attempt-per-(player, quiz)
// constraint enforced by [Service.CreateGame] and the admin reset flow
// keeps a player from showing up more than once.
type LeaderboardAnswer struct {
	PlayerID          int64
	Username          string
	QuestionStartedAt time.Time
	QuestionExpiredAt time.Time
	AnsweredAt        time.Time
	Correct           bool
}

// LeaderboardEntry is a single row of a per-quiz leaderboard: the player's
// total score for that quiz. Rank is 1-indexed and computed before
// truncation, so the value remains meaningful for a CurrentPlayer entry
// returned outside the truncated top-N. IsCurrentPlayer is true when the
// entry belongs to the player making the request, which lets the client
// highlight the row.
type LeaderboardEntry struct {
	PlayerID        int64
	Username        string
	Score           int
	Rank            int
	IsCurrentPlayer bool
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
	StartGame(ctx context.Context, id string) error
	CreateParticipant(ctx context.Context, p *Participant) error
	CreateQuestion(ctx context.Context, gq *Question) error
	CreateAnswer(ctx context.Context, a *Answer) error
	// ListAnswersForQuizLeaderboard returns one row per game_answer for
	// completed games of the given quiz, joined with the fields the
	// Service needs to score each answer. A game counts as completed when
	// every quiz question has been issued (answered or timed out); partial
	// games are filtered out at the store layer.
	ListAnswersForQuizLeaderboard(ctx context.Context, quizID int64) ([]*LeaderboardAnswer, error)
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
}

// NewService initializes and returns a new instance of Service with the provided game and quiz stores.
func NewService(gameStore Store, quizStore quiz.Store, logger *slog.Logger) *Service {
	return &Service{
		store:     gameStore,
		quizStore: quizStore,
		logger:    logger,
	}
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

// CreateGame creates a new game with the specified quiz and player, linking the player and starting the game immediately.
// Returns the newly created game or an error if the operation fails.
//
// Returns [ErrGameAlreadyExists] when the player already has a game for the
// quiz (in-progress or completed). Callers that need to render a "resume"
// affordance should call [Service.GetGameForPlayerOnQuiz] first; the
// AlreadyExists error here is a defensive backstop, not the primary signal.
func (s *Service) CreateGame(ctx context.Context, quizID, playerID int64) (*Game, error) {
	var err error
	// verify that the quiz exists
	qz, err := s.quizStore.GetQuiz(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to get quiz: %w", err)
	}

	// One-attempt-per-(player, quiz) enforcement. Checked here rather than
	// at the DB level because the schema doesn't carry the constraint —
	// see #145 for the design discussion.
	existing, err := s.store.GetGameByPlayerAndQuiz(ctx, playerID, qz.ID)
	if err != nil && !errors.Is(err, ErrGameNotFound) {
		return nil, fmt.Errorf("failed to check existing game: %w", err)
	}
	if existing != nil {
		return nil, ErrGameAlreadyExists
	}

	// Create the game record
	g := &Game{QuizID: qz.ID}
	if err = s.store.CreateGame(ctx, g); err != nil {
		return nil, fmt.Errorf("failed to create game: %w", err)
	}

	g.Quiz = qz

	// Add the player to the game
	pa := &Participant{GameID: g.ID, PlayerID: playerID}
	if err = s.store.CreateParticipant(ctx, pa); err != nil {
		return nil, fmt.Errorf("failed to create participant: %w", err)
	}

	// Start the game (Single player game starts immediately)
	if err = s.store.StartGame(ctx, g.ID); err != nil {
		return nil, fmt.Errorf("failed to start game: %w", err)
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

// GetNextQuestion retrieves the next unanswered question for the specified game or returns nil if all are answered.
func (s *Service) GetNextQuestion(ctx context.Context, gameID string) (*Question, error) {
	// Get the game
	g, err := s.store.GetGame(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("failed to get game: %w", err)
	}

	// Get the quiz
	qz, err := s.quizStore.GetQuiz(ctx, g.QuizID)
	if err != nil {
		return nil, fmt.Errorf("failed to get quiz: %w", err)
	}

	g.Quiz = qz

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

	var gq *Question
	// If we found a quiz question, register it as a GameQuestion. The
	// answer window (StartedAt → ExpiredAt) is anchored at
	// now + defaultRevealDelay, not "now" — the reveal delay gives the
	// player a brief beat to read the question before the option
	// buttons appear (#247). Submissions before StartedAt are scored
	// as if they arrived AT StartedAt (see CalculateScore's clamp).
	if nextQuestion != nil {
		revealAt := time.Now().Add(defaultRevealDelay)
		gq = &Question{
			GameID:       gameID,
			QuestionID:   nextQuestion.ID,
			QuizQuestion: nextQuestion,
			StartedAt:    revealAt,
			ExpiredAt:    revealAt.Add(defaultExpiration),
			// Position counts the newly-issued question itself, so
			// it's the prior asked count + 1 (the player just
			// received this question; previous answers were the N-1
			// before it).
			Position: len(g.Questions) + 1,
			Total:    len(qz.Questions),
		}
		if err = s.store.CreateQuestion(ctx, gq); err != nil {
			return nil, fmt.Errorf("failed to record game question: %w", err)
		}
	}

	if nextQuestion == nil {
		return nil, ErrNoMoreQuestions
	}

	return gq, nil
}

// SubmitAnswer records an answer from a player for a specific question in a game.
// It validates that the game exists and the question belongs to the game before saving the answer.
// Returns the saved answer or nil if the question was not found in the game.
// Returns an error if the operation fails.
func (s *Service) SubmitAnswer(
	ctx context.Context,
	gameID string,
	playerID, questionID, optionID int64,
) (*Answer, error) {
	var err error

	g, err := s.store.GetGame(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("failed to get game: %w", err)
	}

	var question *Question
	for _, qs := range g.Questions {
		if qs.QuestionID == questionID {
			question = qs

			break
		}
	}

	if question == nil {
		return nil, fmt.Errorf("question %d not found in game %s: %w", questionID, gameID, ErrQuestionNotInGame)
	}

	// Load the quiz question with its full option set in one shot so
	// callers can read both the selected option and the correct ones
	// (so the player client can light up the right answer after a wrong
	// pick — #233) without a second store round-trip.
	quizQuestion, err := s.quizStore.GetQuestion(ctx, question.QuestionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get question: %w", err)
	}
	question.QuizQuestion = quizQuestion

	var option *quiz.Option
	for _, o := range quizQuestion.Options {
		if o.ID == optionID {
			option = o

			break
		}
	}
	if option == nil {
		return nil, fmt.Errorf(
			"option %d not in question %d: %w",
			optionID,
			question.QuestionID,
			ErrOptionNotInQuestion,
		)
	}

	if option.QuestionID != question.QuestionID {
		return nil, ErrOptionNotInQuestion
	}

	a := &Answer{
		GameID:     gameID,
		PlayerID:   playerID,
		QuestionID: question.ID,
		Question:   question,
		OptionID:   optionID,
		Option:     option,
	}

	if err = s.store.CreateAnswer(ctx, a); err != nil {
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

// GetResults calculates the accumulated score for each player in a game and returns the results.
func (s *Service) GetResults(ctx context.Context, gameID string) (*Results, error) {
	g, err := s.store.GetGame(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("failed to get game: %w", err)
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
// Only completed games (every quiz question issued, answered or timed
// out) are counted; partial games are filtered out by the store so a
// player who walked away mid-quiz does not take a slot. Combined with
// the one-attempt-per-(player, quiz) constraint enforced by
// [Service.CreateGame] and the admin reset flow, that means each player
// either appears with their final score for the quiz or not at all.
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

	rows, err := s.store.ListAnswersForQuizLeaderboard(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to list leaderboard answers: %w", err)
	}

	playerTotals := make(map[int64]int)
	usernames := make(map[int64]string)

	for _, r := range rows {
		usernames[r.PlayerID] = r.Username

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

	entries := make([]LeaderboardEntry, 0, len(playerTotals))
	for playerID, score := range playerTotals {
		entries = append(entries, LeaderboardEntry{
			PlayerID:        playerID,
			Username:        usernames[playerID],
			Score:           score,
			IsCurrentPlayer: playerID == currentPlayerID,
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
