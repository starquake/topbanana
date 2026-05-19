package clientapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	. "github.com/starquake/topbanana/internal/clientapi"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/quiz"
)

// withPlayer returns ctx annotated with a stub authenticated player. Use it
// when the test exercises a handler that pulls the player off the context
// (typically because EnsurePlayer would do so in production).
func withPlayer(ctx context.Context, id int64) context.Context {
	return auth.WithPlayer(ctx, &auth.Player{ID: id, Username: "stub", Role: auth.RolePlayer})
}

var errStub = errors.New("stub error")

type stubQuizStore struct {
	listQuizzes func(ctx context.Context) ([]*quiz.Quiz, error)
	getQuiz     func(ctx context.Context, id int64) (*quiz.Quiz, error)
	quizExists  func(ctx context.Context, id int64) (bool, error)
	getOption   func(ctx context.Context, id int64) (*quiz.Option, error)
}

func (stubQuizStore) Ping(_ context.Context) error { return nil }

func (s stubQuizStore) ListQuizzes(ctx context.Context) ([]*quiz.Quiz, error) {
	if s.listQuizzes == nil {
		return nil, errStub
	}

	return s.listQuizzes(ctx)
}

func (stubQuizStore) QuestionCountsByQuiz(_ context.Context) (map[int64]int, error) {
	return map[int64]int{}, nil
}

func (s stubQuizStore) GetQuiz(ctx context.Context, id int64) (*quiz.Quiz, error) {
	if s.getQuiz == nil {
		return nil, errStub
	}

	return s.getQuiz(ctx, id)
}

func (s stubQuizStore) QuizExists(ctx context.Context, id int64) (bool, error) {
	if s.quizExists == nil {
		return false, errors.ErrUnsupported
	}

	return s.quizExists(ctx, id)
}

func (s stubQuizStore) GetOption(ctx context.Context, id int64) (*quiz.Option, error) {
	if s.getOption == nil {
		return nil, errStub
	}

	return s.getOption(ctx, id)
}

func (stubQuizStore) CreateQuiz(_ context.Context, _ *quiz.Quiz) error         { return nil }
func (stubQuizStore) UpdateQuiz(_ context.Context, _ *quiz.Quiz) error         { return nil }
func (stubQuizStore) DeleteQuiz(_ context.Context, _ int64) error              { return nil }
func (stubQuizStore) CreateQuestion(_ context.Context, _ *quiz.Question) error { return nil }
func (stubQuizStore) UpdateQuestion(_ context.Context, _ *quiz.Question) error { return nil }
func (stubQuizStore) NextQuestionPosition(_ context.Context, _ int64) (int, error) {
	return 0, errStub
}

func (stubQuizStore) SwapQuestionPositions(_ context.Context, _, _ int64, _ string) error {
	return errStub
}
func (stubQuizStore) DeleteQuestion(_ context.Context, _ int64) error { return nil }

func (stubQuizStore) ListQuestions(_ context.Context, _ int64) ([]*quiz.Question, error) {
	return nil, errStub
}

func (stubQuizStore) GetQuestion(_ context.Context, _ int64) (*quiz.Question, error) {
	return nil, errStub
}

func (stubQuizStore) GetOptionsByIDs(_ context.Context, _ []int64) ([]*quiz.Option, error) {
	return nil, errStub
}

type stubGameStore struct {
	getGame                       func(ctx context.Context, id string) (*game.Game, error)
	getGameByPlayerAndQuiz        func(ctx context.Context, playerID, quizID int64) (*game.Game, error)
	createGame                    func(ctx context.Context, g *game.Game) error
	startGame                     func(ctx context.Context, id string) error
	createParticipant             func(ctx context.Context, p *game.Participant) error
	createQuestion                func(ctx context.Context, gq *game.Question) error
	createAnswer                  func(ctx context.Context, a *game.Answer) error
	listAnswersForQuizLeaderboard func(ctx context.Context, quizID int64) ([]*game.LeaderboardAnswer, error)
	deleteGamesForPlayerOnQuiz    func(ctx context.Context, playerID, quizID int64) error
	listQuizIDsForPlayer          func(ctx context.Context, playerID int64) ([]int64, error)
}

func (stubGameStore) Ping(_ context.Context) error { return nil }

func (s stubGameStore) GetGame(ctx context.Context, id string) (*game.Game, error) {
	if s.getGame == nil {
		return nil, errStub
	}

	return s.getGame(ctx, id)
}

func (s stubGameStore) GetGameByPlayerAndQuiz(
	ctx context.Context, playerID, quizID int64,
) (*game.Game, error) {
	if s.getGameByPlayerAndQuiz == nil {
		// Default to "no existing game" so existing CreateGame tests
		// continue to exercise the success path.
		return nil, game.ErrGameNotFound
	}

	return s.getGameByPlayerAndQuiz(ctx, playerID, quizID)
}

func (s stubGameStore) CreateGame(ctx context.Context, g *game.Game) error {
	if s.createGame == nil {
		return errStub
	}

	return s.createGame(ctx, g)
}

func (s stubGameStore) DeleteGamesForPlayerOnQuiz(
	ctx context.Context, playerID, quizID int64,
) error {
	if s.deleteGamesForPlayerOnQuiz == nil {
		return errStub
	}

	return s.deleteGamesForPlayerOnQuiz(ctx, playerID, quizID)
}

func (s stubGameStore) ListQuizIDsForPlayer(ctx context.Context, playerID int64) ([]int64, error) {
	if s.listQuizIDsForPlayer == nil {
		return nil, nil
	}

	return s.listQuizIDsForPlayer(ctx, playerID)
}

func (s stubGameStore) StartGame(ctx context.Context, id string) error {
	if s.startGame == nil {
		return errStub
	}

	return s.startGame(ctx, id)
}

func (s stubGameStore) CreateParticipant(ctx context.Context, p *game.Participant) error {
	if s.createParticipant == nil {
		return errStub
	}

	return s.createParticipant(ctx, p)
}

func (s stubGameStore) CreateQuestion(ctx context.Context, gq *game.Question) error {
	if s.createQuestion == nil {
		return errStub
	}

	return s.createQuestion(ctx, gq)
}

func (s stubGameStore) CreateAnswer(ctx context.Context, a *game.Answer) error {
	if s.createAnswer == nil {
		return errStub
	}

	return s.createAnswer(ctx, a)
}

func (s stubGameStore) ListAnswersForQuizLeaderboard(
	ctx context.Context, quizID int64,
) ([]*game.LeaderboardAnswer, error) {
	if s.listAnswersForQuizLeaderboard == nil {
		return nil, errStub
	}

	return s.listAnswersForQuizLeaderboard(ctx, quizID)
}

func newService(gs stubGameStore, qs stubQuizStore) *game.Service {
	return game.NewService(gs, qs, slog.New(slog.DiscardHandler))
}

func TestHandleQuizList(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	now := time.Now().Truncate(time.Second)

	t.Run("returns quizzes as JSON", func(t *testing.T) {
		t.Parallel()

		handler := HandleQuizList(logger, stubQuizStore{
			listQuizzes: func(_ context.Context) ([]*quiz.Quiz, error) {
				return []*quiz.Quiz{
					{ID: 1, Title: "Quiz One", Slug: "quiz-one", Description: "First", CreatedAt: now},
					{ID: 2, Title: "Quiz Two", Slug: "quiz-two", Description: "Second", CreatedAt: now},
				}, nil
			},
		})

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

		handler := HandleQuizList(logger, stubQuizStore{
			listQuizzes: func(_ context.Context) ([]*quiz.Quiz, error) {
				return nil, errStub
			},
		})

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

	logger := slog.New(slog.DiscardHandler)
	now := time.Now().Truncate(time.Second)

	t.Run("returns full quiz with questions and options", func(t *testing.T) {
		t.Parallel()

		store := stubQuizStore{
			getQuiz: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{
					ID:          id,
					Title:       "Quiz One",
					Slug:        "quiz-one",
					Description: "First",
					CreatedAt:   now,
					Questions: []*quiz.Question{
						{
							ID:       10,
							QuizID:   id,
							Text:     "Question text",
							ImageURL: "http://example.com/img.png",
							Position: 1,
							Options: []*quiz.Option{
								{ID: 100, QuestionID: 10, Text: "Option A", Correct: true},
								{ID: 101, QuestionID: 10, Text: "Option B", Correct: false},
							},
						},
					},
				}, nil
			},
		}

		handler := HandleQuizGet(logger, store)

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/quizzes/quiz-one-1", nil)
		req.SetPathValue("slugID", "quiz-one-1")
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

		if got, want := len(questions), 1; got != want {
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

		store := stubQuizStore{
			getQuiz: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return nil, quiz.ErrQuizNotFound
			},
		}

		handler := HandleQuizGet(logger, store)

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

		store := stubQuizStore{
			getQuiz: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return nil, errStub
			},
		}

		handler := HandleQuizGet(logger, store)

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

	logger := slog.New(slog.DiscardHandler)

	t.Run("returns 400 on bad request body", func(t *testing.T) {
		t.Parallel()

		svc := newService(stubGameStore{}, stubQuizStore{})
		handler := HandleCreateGame(logger, svc)

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodPost, "/api/games",
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

		svc := newService(stubGameStore{}, stubQuizStore{
			getQuiz: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return nil, quiz.ErrQuizNotFound
			},
		})
		handler := HandleCreateGame(logger, svc)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), 7), http.MethodPost, "/api/games",
			strings.NewReader(`{"quizId": 1}`),
		)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 500 on game store error", func(t *testing.T) {
		t.Parallel()

		svc := newService(
			stubGameStore{
				createGame: func(_ context.Context, _ *game.Game) error {
					return errStub
				},
			},
			stubQuizStore{
				getQuiz: func(_ context.Context, id int64) (*quiz.Quiz, error) {
					return &quiz.Quiz{ID: id, Title: "Q"}, nil
				},
			},
		)
		handler := HandleCreateGame(logger, svc)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), 7), http.MethodPost, "/api/games",
			strings.NewReader(`{"quizId": 1}`),
		)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("uses player ID from context", func(t *testing.T) {
		t.Parallel()

		const wantPlayerID = int64(42)
		var seenPlayerID int64
		svc := newService(
			stubGameStore{
				createGame: func(_ context.Context, _ *game.Game) error { return nil },
				createParticipant: func(_ context.Context, p *game.Participant) error {
					seenPlayerID = p.PlayerID

					return nil
				},
				startGame: func(_ context.Context, _ string) error { return nil },
			},
			stubQuizStore{
				getQuiz: func(_ context.Context, id int64) (*quiz.Quiz, error) {
					return &quiz.Quiz{ID: id, Title: "Q"}, nil
				},
			},
		)
		handler := HandleCreateGame(logger, svc)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), wantPlayerID), http.MethodPost, "/api/games",
			strings.NewReader(`{"quizId": 1}`),
		)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusCreated; got != want {
			t.Fatalf("status code = %v, want %v (body=%q)", got, want, rec.Body.String())
		}
		if got, want := seenPlayerID, wantPlayerID; got != want {
			t.Errorf("CreateParticipant PlayerID = %d, want %d", got, want)
		}
	})

	t.Run("returns 500 when player missing on context", func(t *testing.T) {
		t.Parallel()

		svc := newService(stubGameStore{}, stubQuizStore{
			getQuiz: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{ID: id, Title: "Q"}, nil
			},
		})
		handler := HandleCreateGame(logger, svc)

		// No player on the context — handler should refuse rather than
		// silently fall back to a hardcoded ID.
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost, "/api/games",
			strings.NewReader(`{"quizId": 1}`),
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

	logger := slog.New(slog.DiscardHandler)

	svc := newService(
		stubGameStore{
			getGameByPlayerAndQuiz: func(_ context.Context, _, _ int64) (*game.Game, error) {
				return &game.Game{ID: "existing"}, nil
			},
		},
		stubQuizStore{
			getQuiz: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{ID: id, Title: "Q"}, nil
			},
		},
	)
	handler := HandleCreateGame(logger, svc)

	req := httptest.NewRequestWithContext(
		withPlayer(t.Context(), 7), http.MethodPost, "/api/games",
		strings.NewReader(`{"quizId": 1}`),
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

	logger := slog.New(slog.DiscardHandler)

	t.Run("returns 200 with completed=false for an in-progress game", func(t *testing.T) {
		t.Parallel()

		svc := newService(
			stubGameStore{
				getGameByPlayerAndQuiz: func(_ context.Context, _, _ int64) (*game.Game, error) {
					return &game.Game{
						ID:     "abc",
						QuizID: 1,
						// Only the first of two questions has been issued.
						Questions: []*game.Question{{QuestionID: 10}},
					}, nil
				},
			},
			stubQuizStore{
				getQuiz: func(_ context.Context, id int64) (*quiz.Quiz, error) {
					return &quiz.Quiz{
						ID: id,
						Questions: []*quiz.Question{
							{ID: 10}, {ID: 20},
						},
					}, nil
				},
			},
		)
		handler := HandleGameForQuiz(logger, svc)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), 7), http.MethodGet,
			"/api/quizzes/q-1/my-game", nil,
		)
		req.SetPathValue("slugID", "q-1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusOK; got != want {
			t.Fatalf("status code = %v, want %v", got, want)
		}

		var body gameForQuizTestResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if got, want := body.GameID, "abc"; got != want {
			t.Errorf("body.GameID = %q, want %q", got, want)
		}
		if got, want := body.Completed, false; got != want {
			t.Errorf("body.Completed = %v, want %v", got, want)
		}
	})

	t.Run("returns 200 with completed=true once every question has been issued", func(t *testing.T) {
		t.Parallel()

		svc := newService(
			stubGameStore{
				getGameByPlayerAndQuiz: func(_ context.Context, _, _ int64) (*game.Game, error) {
					return &game.Game{
						ID:        "done",
						QuizID:    1,
						Questions: []*game.Question{{QuestionID: 10}, {QuestionID: 20}},
					}, nil
				},
			},
			stubQuizStore{
				getQuiz: func(_ context.Context, id int64) (*quiz.Quiz, error) {
					return &quiz.Quiz{
						ID: id,
						Questions: []*quiz.Question{
							{ID: 10}, {ID: 20},
						},
					}, nil
				},
			},
		)
		handler := HandleGameForQuiz(logger, svc)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), 7), http.MethodGet,
			"/api/quizzes/q-1/my-game", nil,
		)
		req.SetPathValue("slugID", "q-1")
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

		svc := newService(
			stubGameStore{
				getGameByPlayerAndQuiz: func(_ context.Context, _, _ int64) (*game.Game, error) {
					return nil, game.ErrGameNotFound
				},
			},
			stubQuizStore{
				getQuiz: func(_ context.Context, id int64) (*quiz.Quiz, error) {
					return &quiz.Quiz{ID: id}, nil
				},
			},
		)
		handler := HandleGameForQuiz(logger, svc)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), 7), http.MethodGet,
			"/api/quizzes/q-1/my-game", nil,
		)
		req.SetPathValue("slugID", "q-1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 404 when quiz itself does not exist", func(t *testing.T) {
		t.Parallel()

		svc := newService(stubGameStore{}, stubQuizStore{
			getQuiz: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return nil, quiz.ErrQuizNotFound
			},
		})
		handler := HandleGameForQuiz(logger, svc)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), 7), http.MethodGet,
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

		handler := HandleGameForQuiz(logger, newService(stubGameStore{}, stubQuizStore{}))

		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodGet,
			"/api/quizzes/q-1/my-game", nil,
		)
		req.SetPathValue("slugID", "q-1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})
}

func TestHandleQuestionNext(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	t.Run("returns 400 when gameID missing", func(t *testing.T) {
		t.Parallel()

		handler := HandleQuestionNext(logger, newService(stubGameStore{}, stubQuizStore{}))

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/games//questions/next", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 404 when game not found", func(t *testing.T) {
		t.Parallel()

		svc := newService(stubGameStore{
			getGame: func(_ context.Context, _ string) (*game.Game, error) {
				return nil, game.ErrGameNotFound
			},
		}, stubQuizStore{})

		mux := http.NewServeMux()
		mux.Handle("GET /api/games/{gameID}/questions/next", HandleQuestionNext(logger, svc))

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodGet, "/api/games/missing-game/questions/next", nil,
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 404 when quiz not found", func(t *testing.T) {
		t.Parallel()

		svc := newService(
			stubGameStore{
				getGame: func(_ context.Context, id string) (*game.Game, error) {
					return &game.Game{ID: id, QuizID: 1}, nil
				},
			},
			stubQuizStore{
				getQuiz: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
					return nil, quiz.ErrQuizNotFound
				},
			},
		)

		mux := http.NewServeMux()
		mux.Handle("GET /api/games/{gameID}/questions/next", HandleQuestionNext(logger, svc))

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodGet, "/api/games/game-1/questions/next", nil,
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 404 when no more questions", func(t *testing.T) {
		t.Parallel()

		svc := newService(
			stubGameStore{
				getGame: func(_ context.Context, id string) (*game.Game, error) {
					return &game.Game{
						ID:     id,
						QuizID: 1,
						Questions: []*game.Question{
							{QuestionID: 10},
						},
					}, nil
				},
			},
			stubQuizStore{
				getQuiz: func(_ context.Context, qid int64) (*quiz.Quiz, error) {
					return &quiz.Quiz{
						ID: qid,
						Questions: []*quiz.Question{
							{ID: 10, Text: "Q1"},
						},
					}, nil
				},
			},
		)

		mux := http.NewServeMux()
		mux.Handle("GET /api/games/{gameID}/questions/next", HandleQuestionNext(logger, svc))

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodGet, "/api/games/game-1/questions/next", nil,
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 500 on unexpected error", func(t *testing.T) {
		t.Parallel()

		svc := newService(stubGameStore{
			getGame: func(_ context.Context, _ string) (*game.Game, error) {
				return nil, errStub
			},
		}, stubQuizStore{})

		mux := http.NewServeMux()
		mux.Handle("GET /api/games/{gameID}/questions/next", HandleQuestionNext(logger, svc))

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodGet, "/api/games/game-1/questions/next", nil,
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns the question with imageUrl when set", func(t *testing.T) {
		t.Parallel()

		const imageURL = "https://example.com/picture.png"
		svc := newService(
			stubGameStore{
				getGame: func(_ context.Context, id string) (*game.Game, error) {
					return &game.Game{ID: id, QuizID: 1}, nil
				},
				createQuestion: func(_ context.Context, _ *game.Question) error { return nil },
			},
			stubQuizStore{
				getQuiz: func(_ context.Context, qid int64) (*quiz.Quiz, error) {
					return &quiz.Quiz{
						ID: qid,
						Questions: []*quiz.Question{
							{ID: 42, Text: "Q1", ImageURL: imageURL, Options: []*quiz.Option{{ID: 1, Text: "A"}}},
						},
					}, nil
				},
			},
		)

		mux := http.NewServeMux()
		mux.Handle("GET /api/games/{gameID}/questions/next", HandleQuestionNext(logger, svc))

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodGet, "/api/games/game-1/questions/next", nil,
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

	logger := slog.New(slog.DiscardHandler)

	t.Run("returns 400 when gameID missing", func(t *testing.T) {
		t.Parallel()

		handler := HandleAnswerPost(logger, newService(stubGameStore{}, stubQuizStore{}))

		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 400 when questionID invalid", func(t *testing.T) {
		t.Parallel()

		mux := http.NewServeMux()
		mux.Handle(
			"POST /api/games/{gameID}/questions/{questionID}/answers",
			HandleAnswerPost(logger, newService(stubGameStore{}, stubQuizStore{})),
		)

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodPost,
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

		mux := http.NewServeMux()
		mux.Handle(
			"POST /api/games/{gameID}/questions/{questionID}/answers",
			HandleAnswerPost(logger, newService(stubGameStore{}, stubQuizStore{})),
		)

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodPost,
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

		svc := newService(stubGameStore{
			getGame: func(_ context.Context, _ string) (*game.Game, error) {
				return nil, game.ErrGameNotFound
			},
		}, stubQuizStore{})

		mux := http.NewServeMux()
		mux.Handle(
			"POST /api/games/{gameID}/questions/{questionID}/answers",
			HandleAnswerPost(logger, svc),
		)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), 7), http.MethodPost,
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

		svc := newService(stubGameStore{
			getGame: func(_ context.Context, id string) (*game.Game, error) {
				return &game.Game{ID: id, QuizID: 1}, nil
			},
		}, stubQuizStore{})

		mux := http.NewServeMux()
		mux.Handle(
			"POST /api/games/{gameID}/questions/{questionID}/answers",
			HandleAnswerPost(logger, svc),
		)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), 7), http.MethodPost,
			"/api/games/game-1/questions/99/answers",
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

		svc := newService(
			stubGameStore{
				getGame: func(_ context.Context, id string) (*game.Game, error) {
					return &game.Game{
						ID:     id,
						QuizID: 1,
						Questions: []*game.Question{
							{ID: 1, GameID: id, QuestionID: 10},
						},
					}, nil
				},
			},
			stubQuizStore{
				getOption: func(_ context.Context, _ int64) (*quiz.Option, error) {
					// Return an option that belongs to a different question (QuestionID: 99).
					return &quiz.Option{
						ID:         200,
						QuestionID: 99,
						Text:       "Option from another question",
						Correct:    true,
					}, nil
				},
			},
		)

		mux := http.NewServeMux()
		mux.Handle(
			"POST /api/games/{gameID}/questions/{questionID}/answers",
			HandleAnswerPost(logger, svc),
		)

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), 7), http.MethodPost,
			"/api/games/game-1/questions/10/answers",
			strings.NewReader(`{"optionId": 200}`),
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 500 when player missing on context", func(t *testing.T) {
		t.Parallel()

		svc := newService(
			stubGameStore{
				getGame: func(_ context.Context, id string) (*game.Game, error) {
					return &game.Game{
						ID:     id,
						QuizID: 1,
						Questions: []*game.Question{
							{ID: 1, GameID: id, QuestionID: 10},
						},
					}, nil
				},
			},
			stubQuizStore{},
		)

		mux := http.NewServeMux()
		mux.Handle(
			"POST /api/games/{gameID}/questions/{questionID}/answers",
			HandleAnswerPost(logger, svc),
		)

		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost,
			"/api/games/game-1/questions/10/answers",
			strings.NewReader(`{"optionId": 200}`),
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 500 on game error", func(t *testing.T) {
		t.Parallel()

		svc := newService(stubGameStore{
			getGame: func(_ context.Context, _ string) (*game.Game, error) {
				return nil, errStub
			},
		}, stubQuizStore{})

		mux := http.NewServeMux()
		mux.Handle(
			"POST /api/games/{gameID}/questions/{questionID}/answers",
			HandleAnswerPost(logger, svc),
		)

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodPost,
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

	logger := slog.New(slog.DiscardHandler)

	t.Run("returns 400 when gameID missing", func(t *testing.T) {
		t.Parallel()

		handler := HandleGameResults(logger, newService(stubGameStore{}, stubQuizStore{}))

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 404 when game not found", func(t *testing.T) {
		t.Parallel()

		svc := newService(stubGameStore{
			getGame: func(_ context.Context, _ string) (*game.Game, error) {
				return nil, game.ErrGameNotFound
			},
		}, stubQuizStore{})

		mux := http.NewServeMux()
		mux.Handle("GET /api/games/{gameID}/results", HandleGameResults(logger, svc))

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodGet, "/api/games/missing-game/results", nil,
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 500 on game store error", func(t *testing.T) {
		t.Parallel()

		svc := newService(stubGameStore{
			getGame: func(_ context.Context, _ string) (*game.Game, error) {
				return nil, errStub
			},
		}, stubQuizStore{})

		mux := http.NewServeMux()
		mux.Handle("GET /api/games/{gameID}/results", HandleGameResults(logger, svc))

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodGet, "/api/games/game-1/results", nil,
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
	Username        string `json:"username"`
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

	logger := slog.New(slog.DiscardHandler)

	// makeAnswer mirrors the helper in internal/game; replicated here so the
	// black-box test does not need an exported builder. The 10s window with
	// answeredOffset=0 yields a 1000-point CalculateScore for a correct
	// answer and 0 for a wrong one.
	makeAnswer := func(playerID int64, username string, correct bool) *game.LeaderboardAnswer {
		const window = 10 * time.Second
		start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

		return &game.LeaderboardAnswer{
			PlayerID:          playerID,
			Username:          username,
			QuestionStartedAt: start,
			QuestionExpiredAt: start.Add(window),
			AnsweredAt:        start,
			Correct:           correct,
		}
	}

	t.Run("returns leaderboard with isCurrentPlayer set", func(t *testing.T) {
		t.Parallel()

		gs := stubGameStore{
			listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*game.LeaderboardAnswer, error) {
				return []*game.LeaderboardAnswer{
					makeAnswer(1, "alice", true),
					makeAnswer(2, "bob", true),
					makeAnswer(2, "bob", true),
				}, nil
			},
		}
		qs := stubQuizStore{
			quizExists: func(_ context.Context, _ int64) (bool, error) {
				return true, nil
			},
		}
		handler := HandleQuizLeaderboard(logger, newService(gs, qs))

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), 1), http.MethodGet,
			"/api/quizzes/quiz-1/leaderboard", nil,
		)
		req.SetPathValue("slugID", "quiz-1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusOK; got != want {
			t.Fatalf("status code = %v, want %v", got, want)
		}

		var body leaderboardTestResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if got, want := body.QuizID, int64(1); got != want {
			t.Errorf("quizId = %d, want %d", got, want)
		}
		if got, want := len(body.Entries), 2; got != want {
			t.Fatalf("len(entries) = %d, want %d", got, want)
		}
		// bob (2000) should outrank alice (1000).
		if got, want := body.Entries[0].Username, "bob"; got != want {
			t.Errorf("entries[0].Username = %q, want %q", got, want)
		}
		if got, want := body.Entries[0].Rank, 1; got != want {
			t.Errorf("entries[0].Rank = %d, want %d", got, want)
		}
		if got, want := body.Entries[0].IsCurrentPlayer, false; got != want {
			t.Errorf("entries[0].IsCurrentPlayer = %v, want %v", got, want)
		}
		if got, want := body.Entries[1].Username, "alice"; got != want {
			t.Errorf("entries[1].Username = %q, want %q", got, want)
		}
		if got, want := body.Entries[1].Rank, 2; got != want {
			t.Errorf("entries[1].Rank = %d, want %d", got, want)
		}
		if got, want := body.Entries[1].IsCurrentPlayer, true; got != want {
			t.Errorf("entries[1].IsCurrentPlayer = %v, want %v", got, want)
		}
		// currentPlayer field is always populated when the player has a
		// row; in this case alice (player 1) ranks 2.
		if body.CurrentPlayer == nil {
			t.Fatal("currentPlayer = nil, want alice's standing")
		}
		if got, want := body.CurrentPlayer.PlayerID, int64(1); got != want {
			t.Errorf("currentPlayer.PlayerID = %d, want %d", got, want)
		}
		if got, want := body.CurrentPlayer.Rank, 2; got != want {
			t.Errorf("currentPlayer.Rank = %d, want %d", got, want)
		}
	})

	t.Run("returns 404 when quiz not found", func(t *testing.T) {
		t.Parallel()

		qs := stubQuizStore{
			quizExists: func(_ context.Context, _ int64) (bool, error) {
				return false, nil
			},
		}
		handler := HandleQuizLeaderboard(logger, newService(stubGameStore{}, qs))

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), 1), http.MethodGet,
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

		handler := HandleQuizLeaderboard(logger, newService(stubGameStore{}, stubQuizStore{}))

		// No withPlayer wrapper — simulate a misconfigured route that
		// forgot to wrap the handler in EnsurePlayer.
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodGet, "/api/quizzes/quiz-1/leaderboard", nil,
		)
		req.SetPathValue("slugID", "quiz-1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})

	t.Run("returns 500 when service errors", func(t *testing.T) {
		t.Parallel()

		gs := stubGameStore{
			listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*game.LeaderboardAnswer, error) {
				return nil, errStub
			},
		}
		qs := stubQuizStore{
			quizExists: func(_ context.Context, _ int64) (bool, error) {
				return true, nil
			},
		}
		handler := HandleQuizLeaderboard(logger, newService(gs, qs))

		req := httptest.NewRequestWithContext(
			withPlayer(t.Context(), 1), http.MethodGet,
			"/api/quizzes/quiz-1/leaderboard", nil,
		)
		req.SetPathValue("slugID", "quiz-1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})
}
