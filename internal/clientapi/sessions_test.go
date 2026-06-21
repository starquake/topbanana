package clientapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/starquake/topbanana/internal/auth"
	. "github.com/starquake/topbanana/internal/clientapi"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/livesession"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// boundCaptureHandler is a slog.Handler that records each record's attrs,
// merging any WithAttrs-bound prefix attrs (the request-scoped logger's
// request id, the per-handler player) so a test can assert a handler line
// inherited the bound correlation fields. slog.With binds attrs via
// WithAttrs, not on the Record, so a no-op WithAttrs would drop them.
type boundCaptureHandler struct {
	mu      *sync.Mutex
	records *[]map[string]slog.Value
	attrs   []slog.Attr
}

func newBoundCaptureHandler() boundCaptureHandler {
	return boundCaptureHandler{mu: &sync.Mutex{}, records: &[]map[string]slog.Value{}}
}

func (boundCaptureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h boundCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	flat := make(map[string]slog.Value, len(h.attrs)+r.NumAttrs())
	for _, a := range h.attrs {
		flat[a.Key] = a.Value
	}
	r.Attrs(func(a slog.Attr) bool {
		flat[a.Key] = a.Value

		return true
	})

	h.mu.Lock()
	defer h.mu.Unlock()
	*h.records = append(*h.records, flat)

	return nil
}

func (h boundCaptureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)

	return h
}

func (h boundCaptureHandler) WithGroup(string) slog.Handler { return h }

// hasRecordWith reports whether any captured record carries every wanted
// attribute key with a non-empty string value.
func (h boundCaptureHandler) hasRecordWith(keys ...string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, rec := range *h.records {
		ok := true
		for _, k := range keys {
			if _, present := rec[k]; !present {
				ok = false

				break
			}
		}
		if ok {
			return true
		}
	}

	return false
}

// sessionTestEnv bundles a live-session service over real stores on a
// dbtest DB so a session handler test hits real data.
type sessionTestEnv struct {
	db      *sql.DB
	service *livesession.Service
	players auth.PlayerStore
	quizzes quiz.Store
	media   media.Store
}

func newSessionTestEnv(t *testing.T) *sessionTestEnv {
	t.Helper()

	discard := slog.New(slog.DiscardHandler)
	conn := dbtest.Open(t)
	stores := store.New(conn, discard)
	service := livesession.NewService(stores.LiveSessions, stores.Quizzes, discard)

	return &sessionTestEnv{
		db:      conn,
		service: service,
		players: stores.Players,
		quizzes: stores.Quizzes,
		media:   stores.Media,
	}
}

func (e *sessionTestEnv) seedAnonymousPlayer(t *testing.T, name string) int64 {
	t.Helper()

	p, err := e.players.CreateAnonymousPlayer(t.Context(), name)
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer(%q) err = %v, want nil", name, err)
	}

	return p.ID
}

// seedLiveQuiz persists a mode='live' quiz with two single-answer questions so
// a session can be opened on it (CreateSession gates on mode='live'). It mirrors
// twoQuestionQuiz but in live mode, attributed to the seeded admin so the
// NOT NULL created_by_player_id column is satisfied.
func (e *sessionTestEnv) seedLiveQuiz(t *testing.T, slug string) *quiz.Quiz {
	t.Helper()

	qz := &quiz.Quiz{
		Title:             "Live Quiz",
		Slug:              slug,
		Description:       "seeded",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeLive,
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
	if err := e.quizzes.CreateQuiz(t.Context(), qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}

	return qz
}

// attachAudio creates an audio media row scoped to the question's quiz and
// points the question at it via AudioMediaID, persisting both the reference and
// the repeat flag. Returns the new media id so a test can assert the clip's
// audioUrl. Mirrors the solo testEnv helper; the host manifest needs real
// audio-bearing questions (questions.audio_media_id is an enforced FK).
func (e *sessionTestEnv) attachAudio(t *testing.T, q *quiz.Question, repeat bool) int64 {
	t.Helper()

	durationMs := 1500
	row, err := e.media.CreateMedia(t.Context(), &media.Media{
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
	if err := e.quizzes.UpdateQuestion(t.Context(), q); err != nil {
		t.Fatalf("UpdateQuestion err = %v, want nil", err)
	}

	return row.ID
}

// TestHandleSessionState_LogLineInheritsRequestScopedFields pins the request-
// scoped logger adoption: a session handler pulls the logger off the context
// and binds the player id, so the line it emits on a store failure carries
// both the middleware-bound requestId and the handler-bound player without
// restating either at the call site.
func TestHandleSessionState_LogLineInheritsRequestScopedFields(t *testing.T) {
	t.Parallel()

	env := newSessionTestEnv(t)
	hostID := env.seedAnonymousPlayer(t, "host")

	sess, err := env.service.CreateSession(t.Context(), nil, hostID)
	if err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}

	logs := newBoundCaptureHandler()
	// Mimic the middleware: a request-scoped logger carrying a request id,
	// stashed on the context exactly as requestLogger does in production.
	reqLogger := slog.New(logs).With(slog.String("requestId", "req-xyz"))
	ctx := handlers.WithLogger(t.Context(), reqLogger)
	ctx = withPlayer(ctx, hostID)

	// Close the DB so GetSessionState fails, driving the writeInternalError
	// branch that logs through the request-scoped, player-bound logger.
	if cerr := env.db.Close(); cerr != nil {
		t.Fatalf("closing test DB: %v", cerr)
	}

	handler := HandleSessionState(env.service)
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/sessions/"+sess.JoinCode+"/state", nil)
	req.SetPathValue("code", sess.JoinCode)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusInternalServerError; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if !logs.hasRecordWith("requestId", "player") {
		t.Error("no log line carried both requestId and player; want the handler to inherit both")
	}
}

func TestHandleSessionAudio(t *testing.T) {
	t.Parallel()

	t.Run("returns audio-bearing clips in position order", func(t *testing.T) {
		t.Parallel()

		env := newSessionTestEnv(t)
		qz := env.seedLiveQuiz(t, "live-audio-quiz")
		mediaID0 := env.attachAudio(t, qz.Questions[0], false)
		mediaID1 := env.attachAudio(t, qz.Questions[1], true)
		hostID := env.seedAnonymousPlayer(t, "audio-host")

		sess, err := env.service.CreateSession(t.Context(), &qz.ID, hostID)
		if err != nil {
			t.Fatalf("CreateSession err = %v, want nil", err)
		}

		mux := http.NewServeMux()
		mux.Handle("GET /api/sessions/{code}/audio", HandleSessionAudio(env.service))

		req := getRequestWithPlayer(t, hostID, "/api/sessions/"+sess.JoinCode+"/audio")
		req.SetPathValue("code", sess.JoinCode)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusOK; got != want {
			t.Fatalf("status code = %v, want %v", got, want)
		}

		var manifest audioManifest
		if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
			t.Fatalf("decoding manifest: %v", err)
		}
		if got, want := len(manifest.Clips), 2; got != want {
			t.Fatalf("clips = %d, want %d", got, want)
		}

		first := manifest.Clips[0]
		if got, want := first.QuestionID, qz.Questions[0].ID; got != want {
			t.Errorf("clip[0].questionId = %d, want %d", got, want)
		}
		if got, want := first.AudioURL, fmt.Sprintf("/media/%d", mediaID0); got != want {
			t.Errorf("clip[0].audioUrl = %q, want %q", got, want)
		}
		if got, want := first.AudioRepeat, false; got != want {
			t.Errorf("clip[0].audioRepeat = %v, want %v", got, want)
		}

		second := manifest.Clips[1]
		if got, want := second.QuestionID, qz.Questions[1].ID; got != want {
			t.Errorf("clip[1].questionId = %d, want %d", got, want)
		}
		if got, want := second.AudioURL, fmt.Sprintf("/media/%d", mediaID1); got != want {
			t.Errorf("clip[1].audioUrl = %q, want %q", got, want)
		}
		if got, want := second.AudioRepeat, true; got != want {
			t.Errorf("clip[1].audioRepeat = %v, want %v", got, want)
		}
	})

	t.Run("skips questions without audio", func(t *testing.T) {
		t.Parallel()

		env := newSessionTestEnv(t)
		qz := env.seedLiveQuiz(t, "live-mixed-audio-quiz")
		mediaID := env.attachAudio(t, qz.Questions[1], false)
		hostID := env.seedAnonymousPlayer(t, "mixed-audio-host")

		sess, err := env.service.CreateSession(t.Context(), &qz.ID, hostID)
		if err != nil {
			t.Fatalf("CreateSession err = %v, want nil", err)
		}

		mux := http.NewServeMux()
		mux.Handle("GET /api/sessions/{code}/audio", HandleSessionAudio(env.service))

		req := getRequestWithPlayer(t, hostID, "/api/sessions/"+sess.JoinCode+"/audio")
		req.SetPathValue("code", sess.JoinCode)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusOK; got != want {
			t.Fatalf("status code = %v, want %v", got, want)
		}

		var manifest audioManifest
		if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
			t.Fatalf("decoding manifest: %v", err)
		}
		if got, want := len(manifest.Clips), 1; got != want {
			t.Fatalf("clips = %d, want %d", got, want)
		}
		if got, want := manifest.Clips[0].QuestionID, qz.Questions[1].ID; got != want {
			t.Errorf("clip[0].questionId = %d, want %d", got, want)
		}
		if got, want := manifest.Clips[0].AudioURL, fmt.Sprintf("/media/%d", mediaID); got != want {
			t.Errorf("clip[0].audioUrl = %q, want %q", got, want)
		}
	})

	t.Run("returns empty clips array for an empty room with no quiz", func(t *testing.T) {
		t.Parallel()

		env := newSessionTestEnv(t)
		hostID := env.seedAnonymousPlayer(t, "empty-room-host")

		// An empty room (no quiz picked yet, #836) carries no quiz, so the
		// manifest must still serialize clips as [], not null.
		sess, err := env.service.CreateSession(t.Context(), nil, hostID)
		if err != nil {
			t.Fatalf("CreateSession err = %v, want nil", err)
		}

		mux := http.NewServeMux()
		mux.Handle("GET /api/sessions/{code}/audio", HandleSessionAudio(env.service))

		req := getRequestWithPlayer(t, hostID, "/api/sessions/"+sess.JoinCode+"/audio")
		req.SetPathValue("code", sess.JoinCode)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusOK; got != want {
			t.Fatalf("status code = %v, want %v", got, want)
		}
		if got, want := strings.TrimSpace(rec.Body.String()), `{"clips":[]}`; got != want {
			t.Errorf("body = %q, want %q", got, want)
		}
	})

	t.Run("returns 404 when caller is not a participant", func(t *testing.T) {
		t.Parallel()

		env := newSessionTestEnv(t)
		qz := env.seedLiveQuiz(t, "live-gated-audio-quiz")
		env.attachAudio(t, qz.Questions[0], false)
		hostID := env.seedAnonymousPlayer(t, "gated-audio-host")

		sess, err := env.service.CreateSession(t.Context(), &qz.ID, hostID)
		if err != nil {
			t.Fatalf("CreateSession err = %v, want nil", err)
		}
		strangerID := env.seedAnonymousPlayer(t, "gated-audio-stranger")

		mux := http.NewServeMux()
		mux.Handle("GET /api/sessions/{code}/audio", HandleSessionAudio(env.service))

		// A real player who is not on the roster (and not the host) must be
		// rejected the same way HandleSessionState rejects them: a 404, so the
		// code stays opaque to outsiders.
		req := getRequestWithPlayer(t, strangerID, "/api/sessions/"+sess.JoinCode+"/audio")
		req.SetPathValue("code", sess.JoinCode)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 404 when join code is unknown", func(t *testing.T) {
		t.Parallel()

		env := newSessionTestEnv(t)
		playerID := env.seedAnonymousPlayer(t, "unknown-code-player")

		mux := http.NewServeMux()
		mux.Handle("GET /api/sessions/{code}/audio", HandleSessionAudio(env.service))

		req := getRequestWithPlayer(t, playerID, "/api/sessions/NOPE/audio")
		req.SetPathValue("code", "NOPE")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})
}

// getRequestWithPlayer builds a GET request carrying an authenticated player on
// its context, the same shape EnsurePlayer would produce in production, so a
// session handler that reads the player off the context exercises its real gate.
func getRequestWithPlayer(t *testing.T, playerID int64, target string) *http.Request {
	t.Helper()

	ctx := withPlayer(handlers.WithLogger(t.Context(), slog.New(slog.DiscardHandler)), playerID)

	return httptest.NewRequestWithContext(ctx, http.MethodGet, target, nil)
}
