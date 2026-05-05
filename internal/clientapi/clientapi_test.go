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

	. "github.com/starquake/topbanana/internal/clientapi"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/quiz"
)

var errStub = errors.New("stub error")

type stubQuizStore struct {
	listQuizzes func(ctx context.Context) ([]*quiz.Quiz, error)
	getQuiz     func(ctx context.Context, id int64) (*quiz.Quiz, error)
	getOption   func(ctx context.Context, id int64) (*quiz.Option, error)
}

func (stubQuizStore) Ping(_ context.Context) error { return nil }

func (s stubQuizStore) ListQuizzes(ctx context.Context) ([]*quiz.Quiz, error) {
	if s.listQuizzes == nil {
		return nil, errStub
	}

	return s.listQuizzes(ctx)
}

func (s stubQuizStore) GetQuiz(ctx context.Context, id int64) (*quiz.Quiz, error) {
	if s.getQuiz == nil {
		return nil, errStub
	}

	return s.getQuiz(ctx, id)
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
func (stubQuizStore) DeleteQuestion(_ context.Context, _ int64) error          { return nil }

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
	getGame           func(ctx context.Context, id string) (*game.Game, error)
	createGame        func(ctx context.Context, g *game.Game) error
	startGame         func(ctx context.Context, id string) error
	createParticipant func(ctx context.Context, p *game.Participant) error
	createQuestion    func(ctx context.Context, gq *game.Question) error
	createAnswer      func(ctx context.Context, a *game.Answer) error
}

func (stubGameStore) Ping(_ context.Context) error { return nil }

func (s stubGameStore) GetGame(ctx context.Context, id string) (*game.Game, error) {
	if s.getGame == nil {
		return nil, errStub
	}

	return s.getGame(ctx, id)
}

func (s stubGameStore) CreateGame(ctx context.Context, g *game.Game) error {
	if s.createGame == nil {
		return errStub
	}

	return s.createGame(ctx, g)
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

		mux := http.NewServeMux()
		mux.Handle("GET /api/quizzes/{quizID}", HandleQuizGet(logger, store))

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/quizzes/1", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

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

		mux := http.NewServeMux()
		mux.Handle("GET /api/quizzes/{quizID}", HandleQuizGet(logger, store))

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/quizzes/99", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

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

		mux := http.NewServeMux()
		mux.Handle("GET /api/quizzes/{quizID}", HandleQuizGet(logger, store))

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/quizzes/1", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

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
			context.Background(), http.MethodPost, "/api/games",
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
			context.Background(), http.MethodPost, "/api/games",
			strings.NewReader(`{"quizId": 1}`),
		)
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
			context.Background(), http.MethodPost,
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
			context.Background(), http.MethodPost,
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
			context.Background(), http.MethodPost,
			"/api/games/game-1/questions/10/answers",
			strings.NewReader(`{"optionId": 200}`),
		)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusBadRequest; got != want {
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
