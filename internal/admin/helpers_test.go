package admin_test

import (
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/leaderboard"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// adminEnv bundles the real stores and game service the converted admin
// handler tests drive. Every field is backed by a freshly migrated
// in-memory SQLite DB, so handlers hit real data instead of stubbed
// returns.
type adminEnv struct {
	logger  *slog.Logger
	db      *sql.DB
	quizzes quiz.Store
	games   game.Store
	players auth.PlayerStore
	oauth   auth.OAuthIdentityStore
	lister  auth.PlayerLister
	admin   auth.AdminPlayerStore
	tokens  auth.VerifyTokenStore
	service *game.Service
}

// newAdminEnv opens a migrated dbtest DB, builds the real stores, and
// wires a game service with a live leaderboard publisher so the quiz
// view's "Played by" path is exercised end to end against real games.
func newAdminEnv(t *testing.T) *adminEnv {
	t.Helper()

	logger := slog.New(slog.DiscardHandler)
	conn := dbtest.Open(t)
	stores := store.New(conn, logger)

	svc := game.NewService(stores.Games, stores.Quizzes, logger)
	svc.SetLeaderboardPublisher(leaderboard.NewHub())

	return &adminEnv{
		logger:  logger,
		db:      conn,
		quizzes: stores.Quizzes,
		games:   stores.Games,
		players: stores.Players,
		oauth:   stores.OAuth,
		lister:  stores.PlayerLister,
		admin:   stores.AdminPlayers,
		tokens:  stores.VerifyTokens,
		service: svc,
	}
}

// closeStore closes the underlying DB so subsequent store calls fail
// with a real driver error. Used by the "store error renders 500"
// branches where the test only needs *a* store failure (not a specific
// message), per the slice's guidance to prefer a closed DB over a stub.
func (e *adminEnv) closeStore(t *testing.T) {
	t.Helper()

	if err := e.db.Close(); err != nil {
		t.Fatalf("closing test DB: %v", err)
	}
}

// seedQuiz persists qz via the real quiz store and returns it with its
// id (and nested question/option ids) populated. The store attaches a
// default round and stamps created_at/updated_at.
func (e *adminEnv) seedQuiz(t *testing.T, qz *quiz.Quiz) *quiz.Quiz {
	t.Helper()

	if err := e.quizzes.CreateQuiz(t.Context(), qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}

	return qz
}

// backdateQuizUpdatedAt rewrites quizzes.updated_at for the given quiz
// so the list view's relative-time rendering ("2 hr ago") can be pinned;
// the normal create path always stamps the current time. Raw SQL in a
// test fixture follows the established pattern in internal/store tests.
func (e *adminEnv) backdateQuizUpdatedAt(t *testing.T, quizID int64, when time.Time) {
	t.Helper()

	if _, err := e.db.ExecContext(
		t.Context(),
		"UPDATE quizzes SET updated_at = ? WHERE id = ?",
		when, quizID,
	); err != nil {
		t.Fatalf("backdating quiz %d updated_at: %v", quizID, err)
	}
}

// seedPlayer creates an anonymous player row and returns its id, so
// games and participants attributed to the player satisfy the
// game_participants.player_id foreign key.
func (e *adminEnv) seedPlayer(t *testing.T, displayName string) int64 {
	t.Helper()

	p, err := e.players.CreateAnonymousPlayer(t.Context(), displayName)
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer(%q) err = %v, want nil", displayName, err)
	}

	return p.ID
}

// seedCredentialledPlayer inserts a password-bearing row in the given
// role and returns its id. The stored hash is a literal placeholder:
// the players-list view only reads password_hash for the NULL/non-NULL
// onboarding-state and account-type derivation, never the value.
func (e *adminEnv) seedCredentialledPlayer(t *testing.T, displayName, email, role string) int64 {
	t.Helper()

	p, err := e.players.CreatePlayer(t.Context(), displayName, email, "hash", role)
	if err != nil {
		t.Fatalf("CreatePlayer(%q) err = %v, want nil", displayName, err)
	}

	return p.ID
}

// seedVerifiedPlayer inserts a password-bearing row and stamps
// email_verified_at, putting it in the "verified" onboarding bucket.
func (e *adminEnv) seedVerifiedPlayer(t *testing.T, displayName, email, role string) {
	t.Helper()

	id := e.seedCredentialledPlayer(t, displayName, email, role)
	if err := e.admin.SetPlayerEmailVerifiedNow(t.Context(), id); err != nil {
		t.Fatalf("SetPlayerEmailVerifiedNow(%d) err = %v, want nil", id, err)
	}
}

// seedOAuthPlayer inserts an OAuth-only row (no password) linked to the
// given provider, putting it in the "oauth" onboarding bucket.
func (e *adminEnv) seedOAuthPlayer(t *testing.T, displayName, email, provider, subject string) int64 {
	t.Helper()

	p, err := e.oauth.CreatePlayerFromOAuth(t.Context(), displayName, email)
	if err != nil {
		t.Fatalf("CreatePlayerFromOAuth(%q) err = %v, want nil", displayName, err)
	}
	if err := e.oauth.LinkProviderIdentity(t.Context(), p.ID, provider, subject); err != nil {
		t.Fatalf("LinkProviderIdentity(%d) err = %v, want nil", p.ID, err)
	}

	return p.ID
}

// ownedQuiz is the default fixture: a public quiz with the given title
// and slug, attributed to the seeded admin so requireQuizOwner passes.
func ownedQuiz(title, slug string) *quiz.Quiz {
	return &quiz.Quiz{
		Title:             title,
		Slug:              slug,
		Description:       "seeded",
		CreatedByPlayerID: testAdminID,
		Visibility:        quiz.VisibilityPublic,
	}
}

// twoQuestionQuiz returns ownedQuiz plus two single-correct-answer
// questions, so a play-through can answer each correctly.
func twoQuestionQuiz(title, slug string) *quiz.Quiz {
	qz := ownedQuiz(title, slug)
	qz.Questions = []*quiz.Question{
		{
			Text:     "What is the capital of France?",
			Position: 1,
			Options: []*quiz.Option{
				{Text: "Paris", Correct: true},
				{Text: "London", Correct: false},
			},
		},
		{
			Text:     "What is the capital of Germany?",
			Position: 2,
			Options: []*quiz.Option{
				{Text: "Berlin", Correct: true},
				{Text: "Hamburg", Correct: false},
			},
		},
	}

	return qz
}

// correctOptionID returns the question id and the id of its correct
// option for the question at the given zero-based position in the seeded
// quiz, so a play-through can answer correctly without re-deriving it.
func correctOptionID(t *testing.T, qz *quiz.Quiz, questionIndex int) (questionID, optionID int64) {
	t.Helper()

	q := qz.Questions[questionIndex]
	for _, o := range q.Options {
		if o.Correct {
			return q.ID, o.ID
		}
	}
	t.Fatalf("question %d in quiz %d has no correct option", questionIndex, qz.ID)

	return 0, 0
}

// playThrough creates a game for the player and answers all the quiz's
// questions correctly through the real service, producing a completed
// participant that surfaces on the quiz view's "Played by" table.
func (e *adminEnv) playThrough(t *testing.T, qz *quiz.Quiz, playerID int64) {
	t.Helper()

	ctx := t.Context()

	g, err := e.service.CreateGame(ctx, qz.ID, playerID)
	if err != nil {
		t.Fatalf("CreateGame err = %v, want nil", err)
	}

	for i := range qz.Questions {
		if _, err := e.service.GetNext(ctx, g.ID, playerID); err != nil {
			t.Fatalf("GetNext(question %d) err = %v, want nil", i, err)
		}
		questionID, optionID := correctOptionID(t, qz, i)
		if _, err := e.service.SubmitAnswer(
			ctx, g.ID, playerID, questionID, optionID, time.Time{},
		); err != nil {
			t.Fatalf("SubmitAnswer(question %d) err = %v, want nil", i, err)
		}
	}
}
