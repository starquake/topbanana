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
	// CreateQuiz creates a quiz.
	CreateQuiz(ctx context.Context, qz *Quiz) error
	// UpdateQuiz updates a quiz.
	UpdateQuiz(ctx context.Context, qz *Quiz) error
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
//   - VisibilityPublic — listed everywhere and playable by anyone.
//   - VisibilityUnlisted — not on any list; reachable only by sharing
//     the /play/<slug>-<id> link.
//   - VisibilityPrivate — reachable only by logged-in players.
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

// Quiz represents a quiz. CreatedByPlayerID + CreatedByUsername were
// added in migration 20260520200000 to support the creator-only-edit
// rule from #281. CreatedByPlayerID is NOT NULL at the DB level;
// existing rows were backfilled to the lowest-id admin during the
// migration. A zero value here means "caller forgot to set it";
// [QuizStore.CreateQuiz] surfaces ErrCreatorRequired rather than
// letting the FK insert fail at the wire.
type Quiz struct {
	ID                int64
	Title             string
	Slug              string
	Description       string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	CreatedByPlayerID int64
	CreatedByUsername string
	// TimeLimitSeconds is the default per-question answer window in
	// seconds. The game service resolves the priority chain
	// (Question.TimeLimitSeconds → Quiz.TimeLimitSeconds → defaultExpiration)
	// when issuing a question (#99). The DB default is 10 so existing
	// rows match the historical hard-coded window.
	TimeLimitSeconds int
	// Visibility controls who can find and play the quiz (#103). One of
	// VisibilityPublic, VisibilityUnlisted, VisibilityPrivate. A zero
	// value (empty string) is treated as VisibilityPublic by the store
	// layer so existing fixtures and the JSON-import path don't need to
	// repeat the default.
	Visibility string
	Questions  []*Question
}

// Question represents a question in a quiz.
//
// TimeLimitSeconds is the per-question override. Nil means "inherit the
// quiz default" — the game service applies the priority chain at
// question-issue time (#99).
type Question struct {
	ID               int64
	QuizID           int64
	Text             string
	ImageURL         string
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
