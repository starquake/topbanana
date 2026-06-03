package clientapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/auth"
	. "github.com/starquake/topbanana/internal/clientapi"
	"github.com/starquake/topbanana/internal/game"
)

// withPlayer returns ctx annotated with an authenticated player. Use it
// when the test exercises a handler that pulls the player off the context
// (typically because EnsurePlayer would do so in production). The id must
// be a real players row so the game/participant foreign keys hold.
func withPlayer(ctx context.Context, id int64) context.Context {
	return auth.WithPlayer(ctx, &auth.Player{ID: id, DisplayName: "stub", Role: auth.RolePlayer})
}

func TestHandleQuizList(t *testing.T) {
	t.Parallel()

	t.Run("returns quizzes as JSON", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		env.seedQuiz(t, twoQuestionQuiz("Quiz One", "quiz-one"))
		env.seedQuiz(t, twoQuestionQuiz("Quiz Two", "quiz-two"))

		handler := HandleQuizList(env.logger, env.quizzes)

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/quizzes", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusOK; got != want {
			t.Fatalf("status code = %v, want %v", got, want)
		}

		var result []map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if got, want := len(result), 2; got != want {
			t.Fatalf("len(quizzes) = %v, want %v", got, want)
		}
	})

	t.Run("returns 500 on store error", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		env.closeStore(t)

		handler := HandleQuizList(env.logger, env.quizzes)

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/quizzes", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})
}

func TestHandleQuizGet(t *testing.T) {
	t.Parallel()

	t.Run("returns full quiz with questions and options", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz One", "quiz-one"))

		handler := HandleQuizGet(env.logger, env.quizzes)

		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodGet, fmt.Sprintf("/api/quizzes/quiz-one-%d", qz.ID), nil,
		)
		req.SetPathValue("slugID", fmt.Sprintf("quiz-one-%d", qz.ID))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusOK; got != want {
			t.Fatalf("status code = %v, want %v", got, want)
		}

		var result map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if got, want := result["title"], "Quiz One"; got != want {
			t.Errorf("title = %v, want %v", got, want)
		}

		questions, ok := result["questions"].([]any)
		if !ok {
			t.Fatal("questions field missing or wrong type")
		}

		if got, want := len(questions), 2; got != want {
			t.Fatalf("len(questions) = %v, want %v", got, want)
		}

		q, ok := questions[0].(map[string]any)
		if !ok {
			t.Fatal("question is not a map")
		}

		opts, ok := q["options"].([]any)
		if !ok {
			t.Fatal("options field missing or wrong type")
		}

		if got, want := len(opts), 2; got != want {
			t.Errorf("len(options) = %v, want %v", got, want)
		}
	})

	t.Run("returns 404 when quiz not found", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)

		handler := HandleQuizGet(env.logger, env.quizzes)

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/quizzes/quiz-one-99", nil)
		req.SetPathValue("slugID", "quiz-one-99")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 500 on store error", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		env.closeStore(t)

		handler := HandleQuizGet(env.logger, env.quizzes)

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/quizzes/quiz-one-1", nil)
		req.SetPathValue("slugID", "quiz-one-1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})
}

func TestHandleCreateGame(t *testing.T) {
	t.Parallel()

	t.Run("returns 400 on bad request body", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		handler := HandleCreateGame(env.logger, env.service)

		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost, "/api/games",
			strings.NewReader("{bad json}"),
		)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 404 when quiz not found", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		playerID := env.seedPlayer(t, "creator-404")
		handler := HandleCreateGame(env.logger, env.service)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodPost, "/api/games",
			strings.NewReader(`{"quizId": 999}`),
		)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 500 on game store error", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz", "quiz"))
		playerID := env.seedPlayer(t, "creator-500")
		env.closeStore(t)

		handler := HandleCreateGame(env.logger, env.service)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodPost, "/api/games",
			strings.NewReader(fmt.Sprintf(`{"quizId": %d}`, qz.ID)),
		)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("uses player ID from context", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz", "quiz"))
		playerID := env.seedPlayer(t, "creator-ctx")
		handler := HandleCreateGame(env.logger, env.service)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodPost, "/api/games",
			strings.NewReader(fmt.Sprintf(`{"quizId": %d}`, qz.ID)),
		)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusCreated; got != want {
			t.Fatalf("status code = %v, want %v (body=%q)", got, want, rec.Body.String())
		}

		// The created game must belong to the player on the context: a
		// resume probe for that (player, quiz) pair now finds the game.
		g, err := env.service.GetGameForPlayerOnQuiz(t.Context(), playerID, qz.ID)
		if err != nil {
			t.Fatalf("GetGameForPlayerOnQuiz err = %v, want the created game", err)
		}
		if got := g.ID; got == "" {
			t.Error("created game has empty ID")
		}
	})

	t.Run("returns 500 when player missing on context", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz", "quiz"))
		handler := HandleCreateGame(env.logger, env.service)

		// No player on the context - handler should refuse rather than
		// silently fall back to a hardcoded ID.
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost, "/api/games",
			strings.NewReader(fmt.Sprintf(`{"quizId": %d}`, qz.ID)),
		)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})
}

func TestHandleCreateGame_AlreadyExists(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	qz := env.seedQuiz(t, twoQuestionQuiz("Quiz", "quiz"))
	playerID := env.seedPlayer(t, "creator-dup")

	// First game claims the (player, quiz) slot; the second create must
	// surface 409 via the UNIQUE(player_id, quiz_id) guard.
	if _, err := env.service.CreateGame(t.Context(), qz.ID, playerID); err != nil {
		t.Fatalf("seed CreateGame err = %v, want nil", err)
	}

	handler := HandleCreateGame(env.logger, env.service)

	req := httptest.NewRequestWithContext(
		withPlayer(t.Context(), playerID), http.MethodPost, "/api/games",
		strings.NewReader(fmt.Sprintf(`{"quizId": %d}`, qz.ID)),
	)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusConflict; got != want {
		t.Errorf("status code = %v, want %v", got, want)
	}
}

// gameForQuizTestResponse mirrors the JSON shape returned by
// HandleGameForQuiz; pulled out of the test fn so the parent decode target
// is not a nested struct (revive's nested-structs rule).
type gameForQuizTestResponse struct {
	GameID    string `json:"gameId"`
	Completed bool   `json:"completed"`
}

func TestHandleGameForQuiz(t *testing.T) {
	t.Parallel()

	t.Run("returns 200 with completed=false for an in-progress game", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz", "quiz"))
		playerID := env.seedPlayer(t, "resume-progress")

		// Create the game and issue only the first of two questions, so
		// the resume probe reports it as still in progress.
		g, err := env.service.CreateGame(t.Context(), qz.ID, playerID)
		if err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if _, err := env.service.GetNext(t.Context(), g.ID, playerID); err != nil {
			t.Fatalf("GetNext err = %v, want nil", err)
		}

		handler := HandleGameForQuiz(env.logger, env.service)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodGet,
			fmt.Sprintf("/api/quizzes/q-%d/my-game", qz.ID), nil,
		)
		req.SetPathValue("slugID", fmt.Sprintf("q-%d", qz.ID))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusOK; got != want {
			t.Fatalf("status code = %v, want %v", got, want)
		}

		var body gameForQuizTestResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if got, want := body.GameID, g.ID; got != want {
			t.Errorf("body.GameID = %q, want %q", got, want)
		}
		if got, want := body.Completed, false; got != want {
			t.Errorf("body.Completed = %v, want %v", got, want)
		}
	})

	t.Run("returns 200 with completed=true once every question has been issued", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz", "quiz"))
		playerID := env.seedPlayer(t, "resume-done")

		// Play every question to completion so the game is completed and
		// has no open question window.
		env.playCorrectly(t, qz, playerID, len(qz.Questions))

		handler := HandleGameForQuiz(env.logger, env.service)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodGet,
			fmt.Sprintf("/api/quizzes/q-%d/my-game", qz.ID), nil,
		)
		req.SetPathValue("slugID", fmt.Sprintf("q-%d", qz.ID))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusOK; got != want {
			t.Fatalf("status code = %v, want %v", got, want)
		}

		var body gameForQuizTestResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if got, want := body.Completed, true; got != want {
			t.Errorf("body.Completed = %v, want %v", got, want)
		}
	})

	t.Run("returns 404 when player has no game for the quiz", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz", "quiz"))
		playerID := env.seedPlayer(t, "no-game")

		handler := HandleGameForQuiz(env.logger, env.service)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodGet,
			fmt.Sprintf("/api/quizzes/q-%d/my-game", qz.ID), nil,
		)
		req.SetPathValue("slugID", fmt.Sprintf("q-%d", qz.ID))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 404 when quiz itself does not exist", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		playerID := env.seedPlayer(t, "no-quiz")

		handler := HandleGameForQuiz(env.logger, env.service)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodGet,
			"/api/quizzes/q-99/my-game", nil,
		)
		req.SetPathValue("slugID", "q-99")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 500 when player missing from context", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz", "quiz"))

		handler := HandleGameForQuiz(env.logger, env.service)

		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodGet,
			fmt.Sprintf("/api/quizzes/q-%d/my-game", qz.ID), nil,
		)
		req.SetPathValue("slugID", fmt.Sprintf("q-%d", qz.ID))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})
}

func TestHandleQuestionNext(t *testing.T) {
	t.Parallel()

	t.Run("returns 400 when gameID missing", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		handler := HandleQuestionNext(env.logger, env.service)

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/games//questions/next", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 404 when game not found", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		playerID := env.seedPlayer(t, "next-missing-game")

		mux := http.NewServeMux()
		mux.Handle("GET /api/games/{gameID}/questions/next", HandleQuestionNext(env.logger, env.service))

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodGet, "/api/games/missing-game/questions/next", nil,
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 404 when quiz not found", func(t *testing.T) {
		t.Parallel()

		// A real game always references a real quiz (FK), so the
		// "game exists, quiz vanished" branch can't arise from seeded
		// data alone. Inject a game whose QuizID points at no quiz row so
		// the service's GetQuiz returns the real quiz.ErrQuizNotFound,
		// which the handler maps to 404. The player is wired in as a
		// participant so the gate passes through to the quiz load.
		env := newTestEnv(t)
		const playerID = int64(7)
		svc := game.NewService(
			errGameStore{
				Store: env.games,
				injectedGame: &game.Game{
					ID:           "game-1",
					QuizID:       999999,
					Participants: []*game.Participant{{GameID: "game-1", PlayerID: playerID}},
				},
			},
			env.quizzes, env.logger,
		)

		mux := http.NewServeMux()
		mux.Handle("GET /api/games/{gameID}/questions/next", HandleQuestionNext(env.logger, svc))

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodGet, "/api/games/game-1/questions/next", nil,
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 404 when no more questions", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz", "quiz"))
		playerID := env.seedPlayer(t, "next-exhausted")

		// Drain every question so the next /next call exhausts the quiz.
		gameID := env.playCorrectly(t, qz, playerID, len(qz.Questions))

		mux := http.NewServeMux()
		mux.Handle("GET /api/games/{gameID}/questions/next", HandleQuestionNext(env.logger, env.service))

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodGet,
			fmt.Sprintf("/api/games/%s/questions/next", gameID), nil,
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 500 on unexpected error without leaking wrapped error to body", func(t *testing.T) {
		t.Parallel()

		// Use an error whose message is recognisable so the assertion
		// below can pin its absence from the response body. Before #274
		// the body would echo the wrapped string verbatim. A real store
		// can't produce this exact marker, so inject it at the game store
		// the service wraps (see errGameStore).
		env := newTestEnv(t)
		playerID := env.seedPlayer(t, "next-leak")

		secretErr := errors.New("internal-database-table-name-leak")
		svc := game.NewService(
			errGameStore{Store: env.games, getGameErr: secretErr},
			env.quizzes, env.logger,
		)

		mux := http.NewServeMux()
		mux.Handle("GET /api/games/{gameID}/questions/next", HandleQuestionNext(env.logger, svc))

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodGet, "/api/games/game-1/questions/next", nil,
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		// Response body must not carry the wrapped error string (#274).
		// The handler logs it via slog instead and writes a generic
		// "internal error" body.
		if got := rec.Body.String(); strings.Contains(got, "internal-database-table-name-leak") {
			t.Errorf("5xx body leaked wrapped error: %q", got)
		}

		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns the question with imageUrl when set", func(t *testing.T) {
		t.Parallel()

		const imageURL = "https://example.com/picture.png"
		env := newTestEnv(t)
		qz := twoQuestionQuiz("Quiz", "quiz")
		qz.Questions[0].ImageURL = imageURL
		env.seedQuiz(t, qz)
		playerID := env.seedPlayer(t, "next-image")

		g, err := env.service.CreateGame(t.Context(), qz.ID, playerID)
		if err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}

		mux := http.NewServeMux()
		mux.Handle("GET /api/games/{gameID}/questions/next", HandleQuestionNext(env.logger, env.service))

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodGet,
			fmt.Sprintf("/api/games/%s/questions/next", g.ID), nil,
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusOK; got != want {
			t.Fatalf("status code = %v, want %v", got, want)
		}
		if got, want := rec.Body.String(), `"imageUrl":"`+imageURL+`"`; !strings.Contains(got, want) {
			t.Errorf("body should contain %q, got %q", want, got)
		}
	})
}

func TestHandleAnswerPost(t *testing.T) {
	t.Parallel()

	t.Run("returns 400 when gameID missing", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		handler := HandleAnswerPost(env.logger, env.service)

		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 400 when questionID invalid", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		playerID := env.seedPlayer(t, "answer-badqid")

		mux := http.NewServeMux()
		mux.Handle(
			"POST /api/games/{gameID}/questions/{questionID}/answers",
			HandleAnswerPost(env.logger, env.service),
		)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodPost,
			"/api/games/game-1/questions/not-a-number/answers",
			strings.NewReader(`{"optionId": 1}`),
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 400 on bad request body", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		playerID := env.seedPlayer(t, "answer-badbody")

		mux := http.NewServeMux()
		mux.Handle(
			"POST /api/games/{gameID}/questions/{questionID}/answers",
			HandleAnswerPost(env.logger, env.service),
		)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodPost,
			"/api/games/game-1/questions/1/answers",
			strings.NewReader("{bad json}"),
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 404 when game not found", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		playerID := env.seedPlayer(t, "answer-nogame")

		mux := http.NewServeMux()
		mux.Handle(
			"POST /api/games/{gameID}/questions/{questionID}/answers",
			HandleAnswerPost(env.logger, env.service),
		)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodPost,
			"/api/games/missing/questions/1/answers",
			strings.NewReader(`{"optionId": 1}`),
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 404 when question not in game", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz", "quiz"))
		playerID := env.seedPlayer(t, "answer-qnotin")

		// Fresh game has no issued questions, so any question id is "not
		// in game" from the answer path's view.
		g, err := env.service.CreateGame(t.Context(), qz.ID, playerID)
		if err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}

		mux := http.NewServeMux()
		mux.Handle(
			"POST /api/games/{gameID}/questions/{questionID}/answers",
			HandleAnswerPost(env.logger, env.service),
		)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodPost,
			fmt.Sprintf("/api/games/%s/questions/%d/answers", g.ID, qz.Questions[0].ID),
			strings.NewReader(`{"optionId": 1}`),
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 400 when option does not belong to question", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz", "quiz"))
		playerID := env.seedPlayer(t, "answer-wrongopt")

		// Issue the first question so it is in the game, then submit an
		// option id that belongs to a different question: SubmitAnswer
		// surfaces ErrOptionNotInQuestion, which the handler maps to 400.
		g, err := env.service.CreateGame(t.Context(), qz.ID, playerID)
		if err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if _, err := env.service.GetNext(t.Context(), g.ID, playerID); err != nil {
			t.Fatalf("GetNext err = %v, want nil", err)
		}
		foreignOptionID := qz.Questions[1].Options[0].ID

		mux := http.NewServeMux()
		mux.Handle(
			"POST /api/games/{gameID}/questions/{questionID}/answers",
			HandleAnswerPost(env.logger, env.service),
		)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodPost,
			fmt.Sprintf("/api/games/%s/questions/%d/answers", g.ID, qz.Questions[0].ID),
			strings.NewReader(fmt.Sprintf(`{"optionId": %d}`, foreignOptionID)),
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 500 when player missing on context", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz", "quiz"))

		mux := http.NewServeMux()
		mux.Handle(
			"POST /api/games/{gameID}/questions/{questionID}/answers",
			HandleAnswerPost(env.logger, env.service),
		)

		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost,
			fmt.Sprintf("/api/games/game-1/questions/%d/answers", qz.Questions[0].ID),
			strings.NewReader(`{"optionId": 1}`),
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 500 on game error", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		playerID := env.seedPlayer(t, "answer-storeerr")
		env.closeStore(t)

		mux := http.NewServeMux()
		mux.Handle(
			"POST /api/games/{gameID}/questions/{questionID}/answers",
			HandleAnswerPost(env.logger, env.service),
		)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodPost,
			"/api/games/game-1/questions/1/answers",
			strings.NewReader(`{"optionId": 1}`),
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})
}

func TestHandleGameResults(t *testing.T) {
	t.Parallel()

	t.Run("returns 400 when gameID missing", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		handler := HandleGameResults(env.logger, env.service)

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 404 when game not found", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		playerID := env.seedPlayer(t, "results-nogame")

		mux := http.NewServeMux()
		mux.Handle("GET /api/games/{gameID}/results", HandleGameResults(env.logger, env.service))

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodGet, "/api/games/missing-game/results", nil,
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 500 on game store error", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		playerID := env.seedPlayer(t, "results-storeerr")
		env.closeStore(t)

		mux := http.NewServeMux()
		mux.Handle("GET /api/games/{gameID}/results", HandleGameResults(env.logger, env.service))

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodGet, "/api/games/game-1/results", nil,
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})
}

// leaderboardTestEntry mirrors one entry in the JSON leaderboard response;
// pulled out of the surrounding test fn so the parent decode target stays
// flat (revive's nested-structs rule).
type leaderboardTestEntry struct {
	PlayerID        int64  `json:"playerId"`
	DisplayName     string `json:"displayName"`
	Score           int    `json:"score"`
	Rank            int    `json:"rank"`
	IsCurrentPlayer bool   `json:"isCurrentPlayer"`
}

// leaderboardTestResponse is the decode target for the leaderboard endpoint.
type leaderboardTestResponse struct {
	QuizID        int64                  `json:"quizId"`
	Entries       []leaderboardTestEntry `json:"entries"`
	CurrentPlayer *leaderboardTestEntry  `json:"currentPlayer"`
}

func TestHandleQuizLeaderboard(t *testing.T) {
	t.Parallel()

	t.Run("returns leaderboard with isCurrentPlayer set", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz", "quiz"))

		// alice answers one question correctly (1000); bob answers both
		// (2000), so bob outranks alice. The current player is alice.
		alice := env.seedPlayer(t, "alice")
		bob := env.seedPlayer(t, "bob")
		env.playCorrectly(t, qz, alice, 1)
		env.playCorrectly(t, qz, bob, 2)

		handler := HandleQuizLeaderboard(env.logger, env.service)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), alice), http.MethodGet,
			fmt.Sprintf("/api/quizzes/quiz-%d/leaderboard", qz.ID), nil,
		)
		req.SetPathValue("slugID", fmt.Sprintf("quiz-%d", qz.ID))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusOK; got != want {
			t.Fatalf("status code = %v, want %v (body=%q)", got, want, rec.Body.String())
		}

		var body leaderboardTestResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if got, want := body.QuizID, qz.ID; got != want {
			t.Errorf("quizId = %d, want %d", got, want)
		}
		if got, want := len(body.Entries), 2; got != want {
			t.Fatalf("len(entries) = %d, want %d", got, want)
		}
		// bob (2000) should outrank alice (1000).
		if got, want := body.Entries[0].DisplayName, "bob"; got != want {
			t.Errorf("entries[0].DisplayName = %q, want %q", got, want)
		}
		if got, want := body.Entries[0].Rank, 1; got != want {
			t.Errorf("entries[0].Rank = %d, want %d", got, want)
		}
		if got, want := body.Entries[0].IsCurrentPlayer, false; got != want {
			t.Errorf("entries[0].IsCurrentPlayer = %v, want %v", got, want)
		}
		if got, want := body.Entries[1].DisplayName, "alice"; got != want {
			t.Errorf("entries[1].DisplayName = %q, want %q", got, want)
		}
		if got, want := body.Entries[1].Rank, 2; got != want {
			t.Errorf("entries[1].Rank = %d, want %d", got, want)
		}
		if got, want := body.Entries[1].IsCurrentPlayer, true; got != want {
			t.Errorf("entries[1].IsCurrentPlayer = %v, want %v", got, want)
		}
		// currentPlayer field is always populated when the player has a
		// row; in this case alice ranks 2.
		if body.CurrentPlayer == nil {
			t.Fatal("currentPlayer = nil, want alice's standing")
		}
		if got, want := body.CurrentPlayer.PlayerID, alice; got != want {
			t.Errorf("currentPlayer.PlayerID = %d, want %d", got, want)
		}
		if got, want := body.CurrentPlayer.Rank, 2; got != want {
			t.Errorf("currentPlayer.Rank = %d, want %d", got, want)
		}
	})

	t.Run("returns 404 when quiz not found", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		playerID := env.seedPlayer(t, "lb-noquiz")

		handler := HandleQuizLeaderboard(env.logger, env.service)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodGet,
			"/api/quizzes/missing-99/leaderboard", nil,
		)
		req.SetPathValue("slugID", "missing-99")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 500 when player missing from context", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz", "quiz"))

		handler := HandleQuizLeaderboard(env.logger, env.service)

		// No withPlayer wrapper - simulate a misconfigured route that
		// forgot to wrap the handler in EnsurePlayer.
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodGet,
			fmt.Sprintf("/api/quizzes/quiz-%d/leaderboard", qz.ID), nil,
		)
		req.SetPathValue("slugID", fmt.Sprintf("quiz-%d", qz.ID))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 500 when service errors", func(t *testing.T) {
		t.Parallel()

		env := newTestEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz", "quiz"))
		playerID := env.seedPlayer(t, "lb-storeerr")
		env.closeStore(t)

		handler := HandleQuizLeaderboard(env.logger, env.service)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), playerID), http.MethodGet,
			fmt.Sprintf("/api/quizzes/quiz-%d/leaderboard", qz.ID), nil,
		)
		req.SetPathValue("slugID", fmt.Sprintf("quiz-%d", qz.ID))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})
}
