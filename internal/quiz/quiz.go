// Package quiz provides a store for quizzes, questions, and options.
// It only supports SQLite for now.
package quiz

import (
	"context"
	"errors"
	"slices"
	"time"
)

// Store represents a store for quizzes.
// This can be implemented for different databases.
type Store interface {
	// Ping returns the status of the database connection.
	Ping(ctx context.Context) error
	// ListQuizzes returns all quizzes regardless of visibility. Used by
	// admin surfaces; public-facing callers should use ListPublicQuizzes.
	ListQuizzes(ctx context.Context) ([]*Quiz, error)
	// ListPublicQuizzes returns the visibility=public subset of
	// ListQuizzes (#103). Unlisted quizzes are reachable only by their
	// share link; private quizzes are gated behind authentication at
	// the handler layer.
	ListPublicQuizzes(ctx context.Context) ([]*Quiz, error)
	// ListLiveQuizzes returns the mode='live' subset of ListQuizzes (#836).
	// Used by the host intermission picker to offer the room's next quiz;
	// visibility is not filtered, matching CreateSession's mode='live' gate.
	ListLiveQuizzes(ctx context.Context) ([]*Quiz, error)
	// QuestionCountsByQuiz returns the number of questions per quiz, keyed by
	// quiz ID. Quizzes with no questions are absent from the map; callers
	// should treat a missing entry as 0. Used alongside ListQuizzes by the
	// admin list to render counts without loading every quiz's full tree.
	QuestionCountsByQuiz(ctx context.Context) (map[int64]int, error)
	// GetQuiz returns a quiz including related questions and options by its ID.
	// Returns ErrQuizNotFound if the quiz is not found.
	GetQuiz(ctx context.Context, id int64) (*Quiz, error)
	// QuizExists is a cheap existence check for a quiz by ID. It runs a
	// single one-row SELECT EXISTS probe and does not load the quiz's
	// questions or options. Prefer this over GetQuiz when the caller only
	// needs to know whether the quiz is real (e.g. to map a missing quiz
	// to a 404) and does not need the rest of the tree.
	QuizExists(ctx context.Context, id int64) (bool, error)
	// GetQuizVisibility returns just the visibility of a quiz by ID. It
	// runs a single one-column SELECT and does not load the quiz's
	// questions or options. Prefer this over GetQuiz when the caller only
	// needs the visibility level (e.g. the read-path visibility gate) and
	// does not need the rest of the tree. Returns ErrQuizNotFound when the
	// quiz does not exist.
	GetQuizVisibility(ctx context.Context, id int64) (string, error)
	// CreateQuiz creates a quiz.
	CreateQuiz(ctx context.Context, qz *Quiz) error
	// UpdateQuiz updates a quiz.
	UpdateQuiz(ctx context.Context, qz *Quiz) error
	// SetQuizMode flips just the play mode of a quiz between ModeSolo and
	// ModeLive without touching its questions (#830). Returns ErrInvalidMode
	// when mode is neither, and ErrQuizNotFound when no row matches the id.
	SetQuizMode(ctx context.Context, id int64, mode string) error
	// SetQuizPublished flips just the published flag of a quiz without
	// touching its questions (#1192). Returns ErrQuizNotFound when no row
	// matches the id.
	SetQuizPublished(ctx context.Context, id int64, published bool) error
	// QuizHasRealPlays reports whether the quiz has at least one non-preview
	// game (#1192). Once a real player has started a game the quiz can no
	// longer be unpublished; host preview games do not count.
	QuizHasRealPlays(ctx context.Context, id int64) (bool, error)
	// ListQuestions returns all questions for a quiz by its ID.
	ListQuestions(ctx context.Context, quizID int64) ([]*Question, error)
	// GetQuestion returns a question with options, by its question ID.
	GetQuestion(ctx context.Context, questionID int64) (*Question, error)
	// CreateQuestion creates a question.
	CreateQuestion(ctx context.Context, qs *Question) error
	// CreateQuestionAtNextPosition reads max(position)+1 and inserts
	// the question with that position inside a single transaction,
	// closing the TOCTOU race that would otherwise produce two
	// questions at the same position under concurrent "Add question"
	// clicks (#352).
	CreateQuestionAtNextPosition(ctx context.Context, qs *Question) error
	// UpdateQuestion updates a question.
	UpdateQuestion(ctx context.Context, qs *Question) error
	// SetQuestionMedia patches only a question's media references - its image
	// and audio media ids and the audio repeat flag - without touching the
	// question's text, position, time limit, or options (#1113). The archive
	// importer uses it to wire each restored question to its newly assigned
	// media after the quiz and its media rows exist. A nil id stores NULL,
	// clearing that reference. Returns ErrUpdatingQuestionNoRowsAffected when
	// the id does not match a row.
	SetQuestionMedia(ctx context.Context, questionID int64, imageMediaID, audioMediaID *int64, audioRepeat bool) error
	// SwapQuestionPositions swaps the question with questionID against
	// its neighbour on the given side ("up" = previous position,
	// "down" = next position) within the same quiz, atomically.
	// Returns ErrQuestionAtTop / ErrQuestionAtBottom when there is no
	// neighbour in that direction, ErrQuestionNotFound when the id
	// does not belong to the quiz, and ErrInvalidDirection on any
	// direction other than "up"/"down".
	SwapQuestionPositions(ctx context.Context, quizID, questionID int64, direction string) error
	// GetOption returns an option by its ID.
	GetOption(ctx context.Context, optionID int64) (*Option, error)
	// GetOptionsByIDs returns options for the given IDs.
	GetOptionsByIDs(ctx context.Context, ids []int64) ([]*Option, error)
	// DeleteQuiz deletes a quiz and all its questions and options by ID.
	DeleteQuiz(ctx context.Context, id int64) error
	// DeleteQuestion deletes a question and all its options by ID.
	DeleteQuestion(ctx context.Context, id int64) error
	// ListRoundsByQuiz returns the rounds for a quiz
	// in ascending position order (#444).
	ListRoundsByQuiz(ctx context.Context, quizID int64) ([]*Round, error)
	// RoundCountsByQuiz returns the number of rounds per quiz, keyed by
	// quiz ID. Quizzes with no rounds are absent from the map; callers
	// should treat a missing entry as 0. Mirrors QuestionCountsByQuiz so
	// the public all-quizzes list can render round counts without an N+1
	// per-row lookup (#927).
	RoundCountsByQuiz(ctx context.Context) (map[int64]int, error)
	// GetRound returns a round by its ID. Returns
	// ErrRoundNotFound when the row does not exist.
	GetRound(ctx context.Context, id int64) (*Round, error)
	// GetDefaultRound returns the lowest-position round for a quiz.
	// Every quiz has at least one round (the default created by
	// migration 20260530000000), so question-insert paths resolve the
	// round to attach a new question to via this method. Returns
	// ErrRoundNotFound when the quiz has no rounds.
	GetDefaultRound(ctx context.Context, quizID int64) (*Round, error)
	// CreateRound inserts a round at the caller-supplied
	// position. Returns ErrRoundPositionTaken when the (quiz_id,
	// position) slot is already in use.
	CreateRound(ctx context.Context, g *Round) error
	// UpdateRound updates a round's mutable fields (title, round summary,
	// and position). Returns ErrUpdatingRoundNoRowsAffected when the id
	// does not match a row, and ErrRoundPositionTaken when the new
	// position collides with another round on the same quiz.
	UpdateRound(ctx context.Context, g *Round) error
	// DeleteRound removes a round by ID. Returns
	// ErrDeletingRoundNoRowsAffected when the id does not match a row.
	DeleteRound(ctx context.Context, id int64) error
	// MoveRound shifts a round by one slot in the given direction
	// ("up" = decrement position, "down" = increment position) within
	// the same quiz. Returns ErrRoundNotFound when the round id does
	// not belong to the quiz, ErrInvalidDirection when direction is
	// neither "up" nor "down", and ErrRoundMoveImpossible when the
	// target slot is out of range or already occupied by another round.
	MoveRound(ctx context.Context, quizID, groupID int64, direction string) error
	// MoveQuestionToRound reassigns the question with questionID to the
	// round with groupID. Both must belong to quizID; a mismatch returns
	// ErrQuestionNotFound (question not on quiz) or ErrRoundNotFound
	// (round not on quiz). The question keeps its quiz-wide position.
	MoveQuestionToRound(ctx context.Context, quizID, questionID, groupID int64) error
	// MoveRoundToPosition moves the round with roundID to the 1-based
	// newPosition within its quiz, renumbering every round so positions
	// stay dense 1..N. newPosition is clamped to [1, N] rather than
	// erroring, matching the drag UX. Returns ErrRoundNotFound when the
	// round id does not belong to the quiz.
	MoveRoundToPosition(ctx context.Context, quizID, roundID int64, newPosition int) error
	// MoveQuestionToPosition moves the question with questionID into
	// targetRoundID at the 1-based newPosition within that round, then
	// recomputes every question's quiz-wide position so each round's
	// questions stay contiguous and in round-position order, dense 1..N.
	// newPosition is clamped to the target round's bounds rather than
	// erroring, matching the drag UX. Both ids must belong to quizID; a
	// mismatch returns ErrQuestionNotFound (question not on quiz) or
	// ErrRoundNotFound (round not on quiz).
	MoveQuestionToPosition(ctx context.Context, quizID, questionID, targetRoundID int64, newPosition int) error
}

var (
	// ErrQuizNotFound is returned when a quiz is not found.
	ErrQuizNotFound = errors.New("quiz not found")
	// ErrQuestionNotFound is returned when a question is not found.
	ErrQuestionNotFound = errors.New("question not found")
	// ErrOptionNotFound is returned when an option is not found.
	ErrOptionNotFound = errors.New("option not found")
	// ErrUpdatingQuizNoRowsAffected is returned when no rows are affected when updating a quiz.
	ErrUpdatingQuizNoRowsAffected = errors.New("no rows affected when updating quiz")
	// ErrUpdatingQuestionNoRowsAffected is returned when no rows are affected when updating a question.
	ErrUpdatingQuestionNoRowsAffected = errors.New("no rows affected when updating question")
	// ErrDeletingQuizNoRowsAffected is returned when no rows are affected when deleting a quiz.
	ErrDeletingQuizNoRowsAffected = errors.New("no rows affected when deleting quiz")
	// ErrDeletingQuestionNoRowsAffected is returned when no rows are affected when deleting a question.
	ErrDeletingQuestionNoRowsAffected = errors.New("no rows affected when deleting question")
	// ErrUpdatingOptionNoRowsAffected is returned when no rows are affected when updating a option.
	ErrUpdatingOptionNoRowsAffected = errors.New("no rows affected when updating option")
	// ErrDeletingOptionNoRowsAffected is returned when no rows are affected when deleting a option.
	ErrDeletingOptionNoRowsAffected = errors.New("no rows affected when deleting option")
	// ErrCannotUpdateQuizWithIDZero is returned when trying to update a quiz with ID 0.
	ErrCannotUpdateQuizWithIDZero = errors.New("cannot update quiz with ID 0")
	// ErrCannotUpdateQuestionWithIDZero is returned when trying to update a question with ID 0.
	ErrCannotUpdateQuestionWithIDZero = errors.New("cannot update question with ID 0")
	// ErrQuestionAtTop is returned by SwapQuestionPositions when the
	// caller asked to move a question up but it already has the
	// lowest position in its quiz.
	ErrQuestionAtTop = errors.New("question is already at the top")
	// ErrQuestionAtBottom is returned by SwapQuestionPositions when the
	// caller asked to move a question down but it already has the
	// highest position in its quiz.
	ErrQuestionAtBottom = errors.New("question is already at the bottom")
	// ErrInvalidDirection is returned by SwapQuestionPositions when the
	// supplied direction is neither "up" nor "down".
	ErrInvalidDirection = errors.New("invalid direction")
	// ErrInvalidMode is returned by SetQuizMode when the supplied mode is
	// neither ModeSolo nor ModeLive (#830).
	ErrInvalidMode = errors.New("invalid play mode")
	// ErrCreatorRequired is returned by CreateQuiz when the caller did
	// not set Quiz.CreatedByPlayerID. The column is NOT NULL at the DB
	// level (#281, migration 20260520200000); the sentinel lets handler
	// and store callers surface a clear error before they hit the FK
	// failure from SQLite.
	ErrCreatorRequired = errors.New("quiz creator player id required")
	// ErrSlugTaken is returned by CreateQuiz / UpdateQuiz when the
	// requested slug collides with an existing row. quizzes.slug carries
	// a UNIQUE NOT NULL constraint; the sentinel lets the admin handlers
	// translate the collision into a 409 + inline form error instead of
	// the generic 500 the wrapped SQLite error would produce (#293).
	ErrSlugTaken = errors.New("quiz slug already in use")
	// ErrRoundNotFound is returned when a round is not found (#444).
	ErrRoundNotFound = errors.New("round not found")
	// ErrRoundPositionTaken is returned by CreateRound / UpdateRound when
	// the requested (quiz_id, position) slot is already occupied. The
	// unique index rounds_quiz_position_idx surfaces this as a
	// SQLite SQLITE_CONSTRAINT_UNIQUE (#444).
	ErrRoundPositionTaken = errors.New("round position already taken")
	// ErrUpdatingRoundNoRowsAffected is returned when an UPDATE
	// rounds statement matches no rows; surfaces a stale id
	// without the caller having to inspect a sql.Result (#444).
	ErrUpdatingRoundNoRowsAffected = errors.New("no rows affected when updating round")
	// ErrDeletingRoundNoRowsAffected is returned when a DELETE
	// rounds statement matches no rows (#444).
	ErrDeletingRoundNoRowsAffected = errors.New("no rows affected when deleting round")
	// ErrCannotUpdateRoundWithIDZero guards UpdateRound against a caller
	// that forgot to set the ID (#444).
	ErrCannotUpdateRoundWithIDZero = errors.New("cannot update round with ID 0")
	// ErrRoundMoveImpossible is returned by MoveRound when the requested
	// direction has no valid target slot - either the resulting position
	// is out of range or another round already occupies it (#444).
	ErrRoundMoveImpossible = errors.New("round cannot move in that direction")
)

// Reorder directions accepted by [Store.SwapQuestionPositions].
const (
	DirectionUp   = "up"
	DirectionDown = "down"
)

// Per-question / per-quiz answer-window bounds (#99). The DB CHECK on
// time_limit_seconds enforces the same range; these constants drive the
// admin form's validation message and stay in lockstep with the
// migration.
const (
	MinTimeLimitSeconds     = 1
	MaxTimeLimitSeconds     = 600
	DefaultTimeLimitSeconds = 10
)

// Visibility levels (#103). The DB CHECK on quizzes.visibility enforces
// the same set; keeping them here as typed constants means handlers and
// templates don't sprinkle stringly-typed values across the codebase.
//
//   - VisibilityPublic - listed everywhere and playable by anyone.
//   - VisibilityUnlisted - not on any list; reachable only by sharing
//     the /play/<slug>-<id> link.
//   - VisibilityPrivate - reachable only by logged-in players.
//     Anonymous visitors get a 404 from every read and write surface.
const (
	VisibilityPublic   = "public"
	VisibilityUnlisted = "unlisted"
	VisibilityPrivate  = "private"
)

// VisibilityValues lists the visibility levels in the order the admin
// form's selector renders them. Returned as a fresh slice on every call
// so callers can range over it without worrying about a shared backing
// array (and to keep the gochecknoglobals linter happy).
func VisibilityValues() []string {
	return []string{VisibilityPublic, VisibilityUnlisted, VisibilityPrivate}
}

// IsValidVisibility reports whether v is one of the recognised
// visibility levels (#103).
func IsValidVisibility(v string) bool {
	return slices.Contains(VisibilityValues(), v)
}

// Play modes (MP-0 / #677). The DB CHECK on quizzes.mode enforces the
// same set; keeping them as typed constants means handlers and
// templates don't sprinkle stringly-typed values across the codebase.
//
//   - ModeSolo - self-paced; listed in the solo browse paths and
//     playable by anyone who can read it.
//   - ModeLive - hosted-only (MP-1+); never listed in the solo browse
//     paths and never solo-playable, so it cannot be pre-played and
//     spoiled before a hosted game.
const (
	ModeSolo = "solo"
	ModeLive = "live"
)

// ModeValues lists the play modes in the order the admin form's
// selector renders them. Returned as a fresh slice on every call so
// callers can range over it without sharing a backing array (and to
// keep the gochecknoglobals linter happy).
func ModeValues() []string {
	return []string{ModeSolo, ModeLive}
}

// IsValidMode reports whether m is one of the recognised play modes
// (MP-0 / #677).
func IsValidMode(m string) bool {
	return slices.Contains(ModeValues(), m)
}

// Quiz represents a quiz. CreatedByPlayerID + CreatedByDisplayName were
// added in migration 20260520200000 to support the creator-only-edit
// rule from #281. CreatedByPlayerID is NOT NULL at the DB level;
// existing rows were backfilled to the lowest-id admin during the
// migration. A zero value here means "caller forgot to set it";
// [QuizStore.CreateQuiz] surfaces ErrCreatorRequired rather than
// letting the FK insert fail at the wire.
type Quiz struct {
	ID                   int64
	Title                string
	Slug                 string
	Description          string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	CreatedByPlayerID    int64
	CreatedByDisplayName string
	// TimeLimitSeconds is the default per-question answer window in
	// seconds. The game service resolves the priority chain
	// (Question.TimeLimitSeconds -> Quiz.TimeLimitSeconds -> defaultExpiration)
	// when issuing a question (#99). The DB default is 10 so existing
	// rows match the historical hard-coded window.
	TimeLimitSeconds int
	// Visibility controls who can find and play the quiz (#103). One of
	// VisibilityPublic, VisibilityUnlisted, VisibilityPrivate. A zero
	// value (empty string) is treated as VisibilityPublic by the store
	// layer so existing fixtures and the JSON-import path don't need to
	// repeat the default.
	Visibility string
	// Mode is the play mode (MP-0 / #677): ModeSolo or ModeLive. A live
	// quiz is hosted-only - never listed in the solo browse paths and
	// never solo-playable. A zero value (empty string) is treated as
	// ModeSolo by the store layer so existing fixtures and the
	// JSON-import path don't need to repeat the default.
	Mode string
	// PlayCount is the durable hit counter on the quiz row (#891): bumped
	// once when a play of the quiz completes (the solo path bumps when the
	// final game_questions row is issued, since that is the moment
	// Game.IsCompleted flips true; the live path bumps when the runner moves
	// the session to intermission) and never decremented, so the surfaced
	// "times played" number survives any later retention sweep of old games.
	PlayCount int64
	// Published reports whether the quiz is finished and playable by real
	// players (#1192). A draft (false) is previewable only by its owner and
	// stays out of the public and live-host listings; once published it is
	// locked from content edits. New quizzes default to draft; existing
	// quizzes were backfilled to published by the #1192 migration.
	Published bool
	Questions []*Question
	// Rounds, when non-empty, tells the create path to author the quiz's
	// rounds explicitly instead of dropping every question in the single
	// default round (#546). Each Round carries the questions that belong
	// to it; their quiz-wide Position is still assigned 1..N across all
	// rounds. Leaving this nil keeps the default-round behaviour: every
	// question in Questions lands in the auto-created first round. The
	// regular admin quiz form never sets it.
	Rounds []*Round
}

// Question represents a question in a quiz.
//
// TimeLimitSeconds is the per-question override. Nil means "inherit the
// quiz default" - the game service applies the priority chain at
// question-issue time (#99).
type Question struct {
	ID     int64
	QuizID int64
	// RoundID is the round (round) this question belongs to
	// (#444). NOT NULL at the DB level since migration 20260530000000;
	// the store resolves it to the quiz's default round on create when
	// the caller leaves it zero.
	RoundID int64
	Text    string
	// ImageMediaID references an uploaded image in the question's own quiz
	// library (#937). Nil means no image attached. The referenced media
	// row is quiz-scoped; the admin save handler validates same-quiz
	// ownership before persisting. The questions.image_media_id foreign key is
	// ON DELETE SET NULL, so deleting the image clears this and leaves the
	// question intact minus its picture (#936).
	ImageMediaID *int64
	// AudioMediaID references an uploaded sound in the question's own quiz
	// library (#1059). Nil means no sound attached. It is separate from
	// ImageMediaID so a question can carry both an image and a sound. The
	// questions.audio_media_id foreign key is ON DELETE SET NULL, so deleting
	// the sound clears this and leaves the question intact minus its audio.
	AudioMediaID *int64
	// AudioRepeat, when true, makes the play surfaces replay the attached clip
	// up to 3 times (#1073). Meaningful only when AudioMediaID is set.
	AudioRepeat      bool
	Position         int
	TimeLimitSeconds *int
	Options          []*Option
}

// Option represents an option for a question.
type Option struct {
	ID         int64
	QuestionID int64
	Text       string
	Correct    bool
}

// Round is a named section within a quiz (#444). Every question belongs
// to exactly one round (questions.round_id is NOT NULL).
// Position orders rounds within a quiz; the unique index
// rounds_quiz_position_idx enforces at most one round per
// (quiz_id, position) slot. Summary is the round summary authored later
// via the admin UI; an empty string is valid. Questions is populated by
// callers that need the round's questions; it is not loaded by the basic
// round reads.
type Round struct {
	ID       int64
	QuizID   int64
	Position int
	Title    string
	Summary  string
	// BoundaryDurationSeconds is the per-round override for the
	// round-boundary auto-advance window shared by the intro and
	// recap/results cards. Nil means "inherit the quiz default" - the
	// game service applies the priority chain at boundary time (#554).
	BoundaryDurationSeconds *int
	CreatedAt               time.Time
	UpdatedAt               time.Time
	Questions               []*Question
}
