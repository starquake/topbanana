package clientapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/clientapi"
	"github.com/starquake/topbanana/internal/quiz"
)

type stubQuizStore struct {
	listQuizzes func(ctx context.Context) ([]*quiz.Quiz, error)
	getQuiz     func(ctx context.Context, id int64) (*quiz.Quiz, error)
}

var errNotImplemented = errors.New("not implemented in stub")

func (stubQuizStore) Ping(_ context.Context) error { return nil }

func (s stubQuizStore) ListQuizzes(ctx context.Context) ([]*quiz.Quiz, error) {
	if s.listQuizzes == nil {
		return nil, errNotImplemented
	}

	return s.listQuizzes(ctx)
}

func (s stubQuizStore) GetQuiz(ctx context.Context, id int64) (*quiz.Quiz, error) {
	if s.getQuiz == nil {
		return nil, errNotImplemented
	}

	return s.getQuiz(ctx, id)
}

func (stubQuizStore) CreateQuiz(_ context.Context, _ *quiz.Quiz) error         { return nil }
func (stubQuizStore) UpdateQuiz(_ context.Context, _ *quiz.Quiz) error         { return nil }
func (stubQuizStore) CreateQuestion(_ context.Context, _ *quiz.Question) error { return nil }
func (stubQuizStore) UpdateQuestion(_ context.Context, _ *quiz.Question) error { return nil }

func (stubQuizStore) ListQuestions(_ context.Context, _ int64) ([]*quiz.Question, error) {
	return nil, errNotImplemented
}

func (stubQuizStore) GetQuestion(_ context.Context, _ int64) (*quiz.Question, error) {
	return nil, errNotImplemented
}

func (stubQuizStore) GetOption(_ context.Context, _ int64) (*quiz.Option, error) {
	return nil, errNotImplemented
}

func (stubQuizStore) GetOptionsByIDs(_ context.Context, _ []int64) ([]*quiz.Option, error) {
	return nil, errNotImplemented
}

func TestHandleQuizList(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	now := time.Now().Truncate(time.Second)

	t.Run("returns quizzes as JSON", func(t *testing.T) {
		t.Parallel()

		store := stubQuizStore{
			listQuizzes: func(_ context.Context) ([]*quiz.Quiz, error) {
				return []*quiz.Quiz{
					{ID: 1, Title: "Quiz One", Slug: "quiz-one", Description: "First", CreatedAt: now},
					{ID: 2, Title: "Quiz Two", Slug: "quiz-two", Description: "Second", CreatedAt: now},
				}, nil
			},
		}

		handler := HandleQuizList(logger, store)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/quizzes", nil)
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

		store := stubQuizStore{
			listQuizzes: func(_ context.Context) ([]*quiz.Quiz, error) {
				return nil, errors.New("db error")
			},
		}

		handler := HandleQuizList(logger, store)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/quizzes", nil)
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

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/quizzes/1", nil)
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

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/quizzes/99", nil)
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
				return nil, errors.New("db error")
			},
		}

		mux := http.NewServeMux()
		mux.Handle("GET /api/quizzes/{quizID}", HandleQuizGet(logger, store))

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/quizzes/1", nil)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status code = %v, want %v", got, want)
		}
	})
}
