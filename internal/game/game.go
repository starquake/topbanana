// Package game contains the game domain logic.
package game

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/starquake/topbanana/internal/quiz"
)

const defaultLeaderboardLimit = 10

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
	// retry rather than a 500 - see [Service.SubmitAnswer] and
	// HandleAnswerPost for the recovery path (#353).
	ErrAnswerAlreadyRecorded = errors.New("answer already recorded for this question")

	// ErrQuestionAlreadyIssued is returned by [GameStore.CreateQuestion]
	// when a concurrent /next call already inserted the same
	// (game_id, question_id) row. The store populates the Question with
	// the winning row's ID and timestamps before returning so the
	// service can hand it back as a resume rather than a duplicate.
	ErrQuestionAlreadyIssued = errors.New("question already issued for this game")

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
	// when the UPDATE matched no rows - i.e. the game does not exist.
	ErrStartingGameNoRowsAffected = errors.New("no rows affected when starting game")

	// ErrInvalidRoundPhase is returned by [Service.MarkRoundSeen] when
	// the phase is not one of the recognised round boundary phases
	// (#548). Handlers map it to 400.
	ErrInvalidRoundPhase = errors.New("invalid round phase")
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
	ID          int64
	DisplayName string
	Email       string
	CreatedAt   time.Time
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

// ItemType discriminates the variants of [Item] returned by
// [Service.GetNext]. The round-walking iterator emits one of these per
// call so the player API can serve a question or a round-boundary round
// summary through the same endpoint (#444).
type ItemType string

// Item kinds emitted by [Service.GetNext].
const (
	ItemTypeQuestion      ItemType = "question"
	ItemTypeRoundBoundary ItemType = "round_boundary"
)

// RoundPhase discriminates the two halves of a round boundary (#548).
// Intro is emitted before a round's first not-yet-asked question and
// carries the round title + summary; Results is emitted after all the
// round's questions have been asked and carries the player's own recap
// for the round. Both phases are gated on a non-empty round summary.
type RoundPhase string

// Round boundary phases emitted by [Service.GetNext].
const (
	RoundPhaseIntro   RoundPhase = "intro"
	RoundPhaseResults RoundPhase = "results"
)

// Valid reports whether p is one of the recognised round boundary
// phases. Used by the seen endpoint to reject unknown phase path values
// before they reach the store CHECK constraint.
func (p RoundPhase) Valid() bool {
	return p == RoundPhaseIntro || p == RoundPhaseResults
}

// Item is the union returned by [Service.GetNext]. Exactly one of
// Question or Round is set, matched by Type.
//
// Score is populated for results-phase round-boundary items so the
// recap screen can show the player's running total; it is left zero on
// intro-phase boundaries (the intro wire shape omits it) and on question
// items because the HUD chip doesn't carry a score there (#444, #548).
//
// Total is populated for round-boundary items as well so the player UI
// can keep rendering the "Q n / total" chip across a round summary
// without a second round-trip. For question items, the total lives on
// [Question.Total] (populated at issue time by [Service.GetNext]).
type Item struct {
	Type     ItemType
	Question *Question
	Round    *quiz.Round
	Score    int
	Total    int

	// Phase is set on round-boundary items to the half of the boundary
	// being shown (#548). Zero on question items.
	Phase RoundPhase

	// StartedAt and ExpiredAt bound the auto-advance countdown for a
	// round-boundary item (#548): the card auto-advances when the window
	// expires (the client also keeps a Continue button to skip). The
	// window is one quiz-default answer duration (Quiz.TimeLimitSeconds)
	// long. Both phases carry it. Zero on question items, which carry
	// their own window on [Question.StartedAt]/[Question.ExpiredAt].
	StartedAt time.Time
	ExpiredAt time.Time

	// RoundScore, RoundCorrect, and RoundQuestions carry the player's
	// own recap for the round, populated only on a results-phase
	// round-boundary item. RoundScore is the points earned for this
	// round's questions; RoundCorrect is how many of the round's
	// questions the player answered correctly; RoundQuestions is the
	// number of questions in the round (the denominator). All zero on
	// intro-phase and question items.
	RoundScore     int
	RoundCorrect   int
	RoundQuestions int
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
	// RoundNumber, RoundTotal, RoundPosition, and RoundQuestions place
	// this question within the quiz's rounds for the gameplay header's
	// "Round N of M" heading and per-round "Q n / m" chip. Populated
	// alongside Position by [Service.GetNextQuestion]; zero on
	// store-loaded Questions for the same reason as above.
	RoundNumber    int
	RoundTotal     int
	RoundPosition  int
	RoundQuestions int
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

// LeaderboardAnswer is a flat row for the per-quiz leaderboard. It
// carries every field [Service.CalculateScore] needs plus the player's
// displayName and ID, for both finished and in-progress games.
// IsCompleted is kept on the wire even though GetQuizLeaderboard no
// longer reads it - the store-level test pins the completion
// predicate on it.
type LeaderboardAnswer struct {
	PlayerID          int64
	DisplayName       string
	QuestionStartedAt time.Time
	QuestionExpiredAt time.Time
	AnsweredAt        time.Time
	Correct           bool
	IsCompleted       bool
}

// LeaderboardParticipant is the minimum needed to surface a player on
// the live leaderboard before their first answer commits (#335):
// player_id and displayName for the row, and the same is_completed flag
// the answer rows carry so the entry can be marked in-progress. The
// store returns one of these per participant; [Service.GetQuizLeaderboard]
// uses the list as the canonical set of leaderboard entries and folds
// in the per-answer scoring inputs from
// [Store.ListAnswersForQuizLeaderboard].
type LeaderboardParticipant struct {
	PlayerID    int64
	DisplayName string
	// IsCompleted: every quiz question has been issued to this game.
	IsCompleted bool
	// IsStale: latest game_question is unanswered and expired before
	// the store's stale_before threshold (#336).
	IsStale bool
}

// LeaderboardEntry is a single row of a per-quiz leaderboard: the player's
// total score for that quiz. Rank is 1-indexed and computed before
// truncation, so the value remains meaningful for a CurrentPlayer entry
// returned outside the truncated top-N. IsCurrentPlayer is true when the
// entry belongs to the player making the request, which lets the client
// highlight the row.
//
// Completed is true once every quiz question has been issued.
// InProgress is true when the player is actively mid-quiz: not
// completed AND not stale (#336). Wire renders the live dot from
// InProgress; admin "Played by" filters on Completed.
type LeaderboardEntry struct {
	PlayerID        int64
	DisplayName     string
	Score           int
	Rank            int
	IsCurrentPlayer bool
	Completed       bool
	InProgress      bool
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
	// CreateQuestion records the issuance of a quiz question to a game.
	// When completesGame is true, the same transaction bumps
	// quizzes.play_count for the quiz that owns this game (#891), so the
	// durable hit counter cannot drift from the games-become-completed
	// transition that fires alongside the final question.
	CreateQuestion(ctx context.Context, gq *Question, completesGame bool) error
	CreateAnswer(ctx context.Context, a *Answer) error
	// ListAnswersForQuizLeaderboard returns one row per game_answer for
	// every game (finished or in-progress) of the given quiz, joined with
	// the fields the Service needs to score each answer. The
	// LeaderboardAnswer.IsCompleted flag tells the caller whether the
	// row belongs to a game that has issued every quiz question (#244).
	ListAnswersForQuizLeaderboard(ctx context.Context, quizID int64) ([]*LeaderboardAnswer, error)
	// ListParticipantsForQuizLeaderboard returns one row per player
	// joined to the quiz, flagged with IsCompleted and IsStale (#336).
	// Canonical entry set per #335 so a joined-but-unanswered player
	// still appears at 0.
	ListParticipantsForQuizLeaderboard(
		ctx context.Context,
		quizID int64,
		staleBefore time.Time,
	) ([]*LeaderboardParticipant, error)
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
	// MarkRoundSeen records that the player has acknowledged the given
	// phase of the round boundary in the given game (#548). Idempotent:
	// a second call with the same (gameID, roundID, phase) is a no-op.
	MarkRoundSeen(ctx context.Context, gameID string, roundID int64, phase RoundPhase) error
	// ListSeenRoundPhasesByGame returns the (round, phase) pairs whose
	// round boundary the player has acknowledged in the given game. The
	// round-walking iterator in [Service.GetNext] uses this set to skip
	// past seen round boundary phases (#548).
	ListSeenRoundPhasesByGame(ctx context.Context, gameID string) ([]SeenRoundPhase, error)
}

// SeenRoundPhase is one acknowledged round boundary phase: the round
// and which half of its boundary the player has already passed through
// (#548).
type SeenRoundPhase struct {
	RoundID int64
	Phase   RoundPhase
}

// LeaderboardPublisher is the tiny seam Service uses to signal that a
// quiz's leaderboard has moved. Implemented by *leaderboard.Hub in
// production; nil-by-default so tests that don't care about streaming
// don't have to wire anything up.
type LeaderboardPublisher interface {
	Publish(quizID int64)
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

// slotKind names the variant of [roundSlot]. A zero value (kind == "")
// means the walk reached the end with nothing left to emit.
const (
	slotKindQuestion      = "question"
	slotKindRoundBoundary = "round_boundary"
)

// roundSlot is one slot in the round-driven play sequence used by
// [Service.GetNext]. Exactly one of question or round is set, matched
// by kind. When round is set, phase names which half of the boundary to
// emit. A zero-value roundSlot (kind == "") means the walk reached the
// end with nothing left to emit.
type roundSlot struct {
	kind     string
	question *quiz.Question
	round    *quiz.Round
	phase    RoundPhase
}

// seenKey identifies one acknowledged round boundary phase in the
// seen-state set passed to [nextRoundSlot].
type seenKey struct {
	roundID int64
	phase   RoundPhase
}

// nextRoundSlot walks the quiz's rounds in position order. For each
// round, when the round has a non-empty summary it emits (a) an intro
// boundary before the round's first not-yet-issued question, then (b)
// the round's not-yet-issued questions in position order, then (c) a
// results boundary once every question in the round has been issued.
// Each boundary phase is suppressed once the player has acknowledged
// it. Returns a zero-value roundSlot once every question is issued and
// every shown boundary phase has been seen.
//
// A round with an empty Summary has nothing to show at its boundary, so
// both phases are skipped: every quiz is created with one default round
// holding all its questions, and emitting boundary cards around the
// single round's questions would be a surprising change for existing
// quizzes. The cards only appear once a host authors a round summary.
//
// Questions whose round_id matches no round in the quiz's round list
// (a defensive case; questions.round_id is NOT NULL and FK-references
// rounds) are swept after all rounds so they are never
// silently dropped.
func nextRoundSlot(
	rounds []*quiz.Round,
	questions []*quiz.Question,
	asked map[int64]bool,
	seenRoundPhases map[seenKey]bool,
) roundSlot {
	byRound := make(map[int64][]*quiz.Question, len(rounds))
	for _, q := range questions {
		byRound[q.RoundID] = append(byRound[q.RoundID], q)
	}

	for _, round := range rounds {
		hasSummary := round.Summary != ""
		if hasSummary && !seenRoundPhases[seenKey{roundID: round.ID, phase: RoundPhaseIntro}] {
			return roundSlot{kind: slotKindRoundBoundary, round: round, phase: RoundPhaseIntro}
		}
		for _, q := range byRound[round.ID] {
			if !asked[q.ID] {
				return roundSlot{kind: slotKindQuestion, question: q}
			}
		}
		if hasSummary && !seenRoundPhases[seenKey{roundID: round.ID, phase: RoundPhaseResults}] {
			return roundSlot{kind: slotKindRoundBoundary, round: round, phase: RoundPhaseResults}
		}
	}

	// Sweep any question whose round is not in the quiz's round list so
	// an orphaned question is still served rather than stranding the
	// game short of the leaderboard.
	known := make(map[int64]bool, len(rounds))
	for _, round := range rounds {
		known[round.ID] = true
	}
	for _, q := range questions {
		if !known[q.RoundID] && !asked[q.ID] {
			return roundSlot{kind: slotKindQuestion, question: q}
		}
	}

	return roundSlot{}
}

// resumeCandidate returns the most recently issued game_question for
// the game when it can be handed back as-is (unanswered, answer window
// still open, quiz question still on the quiz). Returns nil when the
// caller should advance to the next question instead - including the
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
	applyRoundProgress(&resumed, qz)

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

// GetQuizLeaderboard returns the top scoring players for a quiz.
// Mid-quiz players appear with their running partial score so the live
// view shows everyone who has joined. Ties are broken by displayName so
// the ordering is stable across requests. currentPlayerID flags the
// requester's entry (and drives CurrentPlayer when they fall outside
// top-N, #181); pass 0 to flag nothing. limit defaults to 10.
func (s *Service) GetQuizLeaderboard(
	ctx context.Context, quizID, currentPlayerID int64, limit int,
) (*LeaderboardResult, error) {
	if limit <= 0 {
		limit = defaultLeaderboardLimit
	}

	// Verify the quiz exists so callers can map ErrQuizNotFound to a 404.
	// Cheap existence check - leaderboard rendering does not need the
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
	participants, err := s.store.ListParticipantsForQuizLeaderboard(ctx, quizID, time.Now().Add(-s.stalePeriod))
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

	entries := leaderboardEntries(participants, playerTotals, currentPlayerID)

	slices.SortFunc(entries, func(a, b LeaderboardEntry) int {
		// Higher scores first; ties broken by ascending displayName.
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}

		return strings.Compare(a.DisplayName, b.DisplayName)
	})

	return finalizeLeaderboardInPlace(entries, currentPlayerID, limit), nil
}

// leaderboardEntries builds the quiz leaderboard entry set from the solo
// participants (the canonical set, #335); playerTotals supplies each player's
// running score. The quiz leaderboard reflects solo play only - a hosted live
// session keeps its own standings and does not feed this board (#771).
func leaderboardEntries(
	participants []*LeaderboardParticipant,
	playerTotals map[int64]int,
	currentPlayerID int64,
) []LeaderboardEntry {
	entries := make([]LeaderboardEntry, 0, len(participants))
	for _, p := range participants {
		entries = append(entries, LeaderboardEntry{
			PlayerID:        p.PlayerID,
			DisplayName:     p.DisplayName,
			Score:           playerTotals[p.PlayerID],
			IsCurrentPlayer: p.PlayerID == currentPlayerID,
			Completed:       p.IsCompleted,
			// Stale rows stay on the board (#336) but drop the dot.
			InProgress: !p.IsCompleted && !p.IsStale,
		})
	}

	return entries
}

// finalizeLeaderboardInPlace stamps 1-indexed rank on every entry, extracts the
// current player's standing from the full ordering (so a player outside
// the visible top-N still gets a Rank that matches their global position),
// and then truncates entries to the requested limit. Split out of
// GetQuizLeaderboard to keep that function under the project's per-function
// length budget; the steps need to run in this order - ranks must be stamped
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
