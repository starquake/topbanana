package clientapi_test

import (
	"database/sql"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/leaderboard"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// seededAdminID is the id of the admin row inserted by migration
// 20260111110308_add_admin_player.sql. Quiz fixtures attribute themselves
// to this admin so the NOT NULL created_by_player_id column is satisfied
// without first having to seed a creator.
const seededAdminID int64 = 1

// testEnv bundles the real stores and game service the converted API
// tests drive. Every field is backed by a freshly migrated in-memory
// SQLite DB, so handlers hit real data instead of stubbed returns.
type testEnv struct {
	logger  *slog.Logger
	db      *sql.DB
	quizzes quiz.Store
	games   game.Store
	players auth.PlayerStore
	media   media.Store
	service *game.Service
}

// newTestEnv opens a migrated dbtest DB, builds the real stores, and
// wires a game service with a live leaderboard publisher so the
// create-game and submit-answer publish paths are exercised end to end.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	logger := slog.New(slog.DiscardHandler)
	conn := dbtest.Open(t)
	stores := store.New(conn, logger)

	svc := game.NewService(stores.Games, stores.Quizzes, logger)
	svc.SetLeaderboardPublisher(leaderboard.NewHub())

	return &testEnv{
		logger:  logger,
		db:      conn,
		quizzes: stores.Quizzes,
		games:   stores.Games,
		players: stores.Players,
		media:   stores.Media,
		service: svc,
	}
}

// attachQuestionAudio creates an audio media row scoped to the question's quiz
// and points the question at it via AudioMediaID, persisting both the reference
// and the repeat flag. It returns the new media id so a test can assert the
// clip's audioUrl. Used by the audio-manifest tests (solo + host), which need
// real audio-bearing questions (questions.audio_media_id is an enforced FK to a
// media row).
func attachQuestionAudio(
	t *testing.T,
	mediaStore media.Store,
	quizStore quiz.Store,
	q *quiz.Question,
	repeat bool,
) int64 {
	t.Helper()

	durationMs := 1500
	row, err := mediaStore.CreateMedia(t.Context(), &media.Media{
		QuizID:            q.QuizID,
		Type:              media.TypeAudio,
		MIME:              "audio/mpeg",
		Path:              "a.mp3",
		SizeBytes:         2048,
		SHA256:            fmt.Sprintf("audio-%d", q.ID),
		DurationMs:        &durationMs,
		CreatedByPlayerID: seededAdminID,
	})
	if err != nil {
		t.Fatalf("CreateMedia err = %v, want nil", err)
	}

	q.AudioMediaID = &row.ID
	q.AudioRepeat = repeat
	if err := quizStore.UpdateQuestion(t.Context(), q); err != nil {
		t.Fatalf("UpdateQuestion err = %v, want nil", err)
	}

	return row.ID
}

// attachAudio is the testEnv convenience wrapper over [attachQuestionAudio].
func (e *testEnv) attachAudio(t *testing.T, q *quiz.Question, repeat bool) int64 {
	t.Helper()

	return attachQuestionAudio(t, e.media, e.quizzes, q, repeat)
}

// closeStore closes the underlying DB so subsequent store calls fail with
// a real driver error. Used by the "returns 500 on store error" branches
// where the test only needs *a* store failure (not a specific message),
// per the slice's guidance to prefer a closed DB over a stub for those.
func (e *testEnv) closeStore(t *testing.T) {
	t.Helper()

	if err := e.db.Close(); err != nil {
		t.Fatalf("closing test DB: %v", err)
	}
}

// seedPlayer creates an anonymous player row and returns its id, so
// games and participants attributed to the player satisfy the
// game_participants.player_id foreign key.
func (e *testEnv) seedPlayer(t *testing.T, displayName string) int64 {
	t.Helper()

	p, err := e.players.CreateAnonymousPlayer(t.Context(), displayName)
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer(%q) err = %v, want nil", displayName, err)
	}

	return p.ID
}

// twoQuestionQuiz is the default fixture: a public quiz with two
// single-correct-answer questions, attributed to the seeded admin.
func twoQuestionQuiz(title, slug string) *quiz.Quiz {
	return &quiz.Quiz{
		Title:             title,
		Slug:              slug,
		Description:       "seeded",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Questions: []*quiz.Question{
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
		},
	}
}

// seedQuiz persists qz via the real quiz store and returns it with its
// id (and nested question/option ids) populated.
func (e *testEnv) seedQuiz(t *testing.T, qz *quiz.Quiz) *quiz.Quiz {
	t.Helper()

	if err := e.quizzes.CreateQuiz(t.Context(), qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}

	return qz
}

// correctOptionID returns the question id and the id of its correct
// option for the question at the given zero-based position in the seeded
// quiz, so a play-through can answer correctly without re-deriving it
// from the JSON response.
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

// wrongOptionID returns an incorrect option id for the question at the given
// zero-based position in the seeded quiz.
func wrongOptionID(t *testing.T, qz *quiz.Quiz, questionIndex int) int64 {
	t.Helper()

	q := qz.Questions[questionIndex]
	for _, o := range q.Options {
		if !o.Correct {
			return o.ID
		}
	}
	t.Fatalf("question %d in quiz %d has no incorrect option", questionIndex, qz.ID)

	return 0
}

// playCorrectly creates a game for the player and answers the first
// `questions` questions correctly through the real service, producing
// leaderboard answers with the deterministic maximum score: the answer
// lands before the reveal-delayed window opens (tappedAt is zero, so the
// clamp falls back to the server's now), so each correct pick scores
// maxPoints. Returns the created game id.
func (e *testEnv) playCorrectly(t *testing.T, qz *quiz.Quiz, playerID int64, questions int) string {
	t.Helper()

	ctx := t.Context()

	g, err := e.service.CreateGame(ctx, qz.ID, playerID)
	if err != nil {
		t.Fatalf("CreateGame err = %v, want nil", err)
	}

	for i := range questions {
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

	return g.ID
}
