package admin_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/logging"
	"github.com/starquake/topbanana/internal/quiz"
)

type stubQuizStore struct {
	listQuizzes func(ctx context.Context) ([]*quiz.Quiz, error)
	getQuizByID func(ctx context.Context, id int64) (*quiz.Quiz, error)
	createQuiz  func(ctx context.Context, qz *quiz.Quiz) error
	updateQuiz  func(ctx context.Context, qz *quiz.Quiz) error
}

func (s stubQuizStore) GetQuizByID(ctx context.Context, id int64) (*quiz.Quiz, error) {
	if s.getQuizByID == nil {
		return nil, errors.New("getQuizByID not supplied in stub")
	}

	return s.getQuizByID(ctx, id)
}

func (stubQuizStore) GetQuestionByID(_ context.Context, _ int64) (*quiz.Question, error) {
	panic("not implemented")
}

func (s stubQuizStore) ListQuizzes(ctx context.Context) ([]*quiz.Quiz, error) {
	if s.listQuizzes == nil {
		return nil, errors.New("listQuizzes not supplied in stub")
	}

	return s.listQuizzes(ctx)
}

func (s stubQuizStore) CreateQuiz(ctx context.Context, qz *quiz.Quiz) error {
	if s.createQuiz == nil {
		return errors.New("createQuiz not supplied in stub")
	}

	return s.createQuiz(ctx, qz)
}

func (s stubQuizStore) UpdateQuiz(ctx context.Context, qz *quiz.Quiz) error {
	if s.updateQuiz == nil {
		return errors.New("updateQuiz not supplied in stub")
	}

	return s.updateQuiz(ctx, qz)
}

func (stubQuizStore) CreateQuestion(_ context.Context, _ *quiz.Question) error {
	panic("not implemented")
}

func (stubQuizStore) UpdateQuestion(_ context.Context, _ *quiz.Question) error {
	panic("not implemented")
}

func TestHandleQuizList(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := logging.NewLogger(&buf)

	t.Run("list quizzes", func(t *testing.T) {
		t.Parallel()

		store := stubQuizStore{
			listQuizzes: func(_ context.Context) ([]*quiz.Quiz, error) {
				return []*quiz.Quiz{
					{ID: 1, Title: "Quiz One", Slug: "quiz-one", Description: "First"},
					{ID: 2, Title: "Quiz Two", Slug: "quiz-two", Description: "Second"},
				}, nil
			},
		}

		handler := admin.HandleQuizList(logger, store)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("status code = %v, want %v", got, want)
		}

		body := rr.Body.String()
		if got, want := body, "Admin Dashboard - Quiz List"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
		if got, want := body, "Quiz One"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
		if got, want := body, "Quiz Two"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("no quizzes", func(t *testing.T) {
		t.Parallel()

		store := stubQuizStore{
			listQuizzes: func(_ context.Context) ([]*quiz.Quiz, error) {
				return []*quiz.Quiz{}, nil
			},
		}

		handler := admin.HandleQuizList(logger, store)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("status code = %v, want %v", got, want)
		}

		body := rr.Body.String()
		if got, want := body, "Admin Dashboard - Quiz List"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
		if got, want := body, "No quizzes found."; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})
}

func TestHandleQuizList_ErrorHandling(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := logging.NewLogger(&buf)

	t.Run("list error", func(t *testing.T) {
		t.Parallel()

		testError := errors.New("test error")

		store := stubQuizStore{
			listQuizzes: func(_ context.Context) ([]*quiz.Quiz, error) {
				return nil, testError
			},
		}

		handler := admin.HandleQuizList(logger, store)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("status code = %v, want %v", got, want)
		}
		if got, want := buf.String(), fmt.Sprintf("err=%q", testError); !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})
}

func TestHandleIndex(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)
	handler := admin.HandleIndex(logger)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	if err != nil {
		t.Fatalf("http.NewRequest error: %v", err)
	}
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Errorf("got status code %v, want %v", got, want)
	}
	if got, want := rr.Body.String(), "Admin Dashboard"; !strings.Contains(got, want) {
		t.Errorf("rr.Body.String() = %q, want %q", got, want)
	}
}

func TestHandleQuizView(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	t.Run("get quiz", func(t *testing.T) {
		t.Parallel()

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{ID: 1, Title: "Quiz One", Slug: "quiz-one", Description: "First"}, nil
			},
		}

		handler := admin.HandleQuizView(logger, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
		if got, want := rr.Body.String(), "Quiz One"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("quiz not found", func(t *testing.T) {
		t.Parallel()

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return nil, quiz.ErrQuizNotFound
			},
		}

		handler := admin.HandleQuizView(logger, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusNotFound; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
		if got, want := buf.String(), fmt.Sprintf("err=%q", quiz.ErrQuizNotFound); !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})
}

func TestHandleQuizView_ErrorHandling(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	t.Run("parsing id fails", func(t *testing.T) {
		t.Parallel()

		quizStore := stubQuizStore{}

		handler := admin.HandleQuizView(logger, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/abc", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "abc")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
		if got, want := buf.String(), "error parsing quizID"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("get quiz by id fails", func(t *testing.T) {
		t.Parallel()

		testError := errors.New("test error")

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return nil, testError
			},
		}

		handler := admin.HandleQuizView(logger, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
		if got, want := buf.String(), fmt.Sprintf("err=%q", testError); !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})
}

func TestHandleQuizCreate(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	handler := admin.HandleQuizCreate(logger)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/create", nil)
	if err != nil {
		t.Fatalf("http.NewRequest error: %v", err)
	}
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("got status code %v, want %v", got, want)
	}
	if got, want := rr.Body.String(), "Create Quiz"; !strings.Contains(got, want) {
		t.Fatalf("got: %q, should contain: %q", got, want)
	}
}

func TestHandleQuizEdit(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	t.Run("quiz found", func(t *testing.T) {
		t.Parallel()

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{ID: 1, Title: "Quiz One", Slug: "quiz-one", Description: "First"}, nil
			},
		}

		handler := admin.HandleQuizEdit(logger, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1/edit", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
		if got, want := rr.Body.String(), "Edit Quiz"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("quiz not found", func(t *testing.T) {
		t.Parallel()

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return nil, quiz.ErrQuizNotFound
			},
		}

		handler := admin.HandleQuizEdit(logger, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1/edit", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusNotFound; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
	})
}

func TestHandleQuizEdit_ErrorHandling(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	t.Run("parsing id fails", func(t *testing.T) {
		t.Parallel()

		quizStore := stubQuizStore{}

		handler := admin.HandleQuizEdit(logger, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/abc/edit", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "abc")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
		if got, want := buf.String(), "error parsing quizID"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("get quiz by id fails", func(t *testing.T) {
		t.Parallel()

		testError := errors.New("test error")

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return nil, testError
			},
		}

		handler := admin.HandleQuizEdit(logger, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1/edit", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
	})
}

func TestHandleQuizSave(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	t.Run("new quiz saved successfully", func(t *testing.T) {
		t.Parallel()

		testQuiz := quiz.Quiz{Title: "Quiz One", Slug: "quiz-one", Description: "First"}

		var quizzes []*quiz.Quiz
		quizStore := stubQuizStore{
			createQuiz: func(_ context.Context, qz *quiz.Quiz) error {
				quizzes = append(quizzes, qz)

				return nil
			},
		}

		handler := admin.HandleQuizSave(logger, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.PostForm = url.Values{
			"title":       {testQuiz.Title},
			"slug":        {testQuiz.Slug},
			"description": {testQuiz.Description},
		}

		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusFound; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
		if got, want := len(quizzes), 1; got != want {
			t.Fatalf("got %v quizzes, want %v", got, want)
		}
		if diff := cmp.Diff(quizzes[0], &testQuiz); diff != "" {
			t.Fatalf("quizzes differ (-got +want):\n%s", diff)
		}
	})

	t.Run("existing quiz saved successfully", func(t *testing.T) {
		t.Parallel()

		originalQuiz := quiz.Quiz{ID: 123456789, Title: "Quiz One", Slug: "quiz-one", Description: "First"}
		updatedQuiz := quiz.Quiz{
			ID:          originalQuiz.ID,
			Title:       originalQuiz.Title + " Updated",
			Slug:        originalQuiz.Slug + "-updated",
			Description: originalQuiz.Description + " Updated",
		}

		var quizzes []*quiz.Quiz
		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				if id != originalQuiz.ID {
					return nil, quiz.ErrQuizNotFound
				}

				return &originalQuiz, nil
			},
			updateQuiz: func(_ context.Context, qz *quiz.Quiz) error {
				quizzes = append(quizzes, qz)

				return nil
			},
		}

		handler := admin.HandleQuizSave(logger, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes/1", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", strconv.FormatInt(originalQuiz.ID, 10))
		req.PostForm = url.Values{
			"title":       {updatedQuiz.Title},
			"slug":        {updatedQuiz.Slug},
			"description": {updatedQuiz.Description},
		}

		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusFound; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
		if got, want := len(quizzes), 1; got != want {
			t.Fatalf("got %v quizzes, want %v", got, want)
		}
		if diff := cmp.Diff(quizzes[0], &updatedQuiz); diff != "" {
			t.Fatalf("quizzes differ (-got +want):\n%s", diff)
		}
	})
}

func TestHandleQuizSave_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("parsing id fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		quizStore := stubQuizStore{}

		handler := admin.HandleQuizSave(logger, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, "/admin/quizzes/abc/edit", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "abc")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
		if got, want := rr.Body.String(), "error parsing quizID"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("get quiz by id fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		testError := errors.New("test error")

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return nil, testError
			},
		}

		handler := admin.HandleQuizSave(logger, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1/edit", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
		if got, want := buf.String(), fmt.Sprintf("err=%q", testError); !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("parsing form fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		quizStore := stubQuizStore{}

		handler := admin.HandleQuizSave(logger, quizStore)
		// Request with empty body so parsing the form triggers an error.
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
		if got, want := rr.Body.String(), "error parsing form"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("storing new quiz fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		testQuiz := quiz.Quiz{Title: "Quiz One", Slug: "quiz-one", Description: "First"}

		testError := errors.New("test error")

		quizStore := stubQuizStore{
			createQuiz: func(_ context.Context, _ *quiz.Quiz) error {
				return testError
			},
		}

		handler := admin.HandleQuizSave(logger, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.PostForm = url.Values{
			"title":       {testQuiz.Title},
			"slug":        {testQuiz.Slug},
			"description": {testQuiz.Description},
		}
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}

		log := buf.String()
		if got, want := log, "error creating quiz"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
		if got, want := log, fmt.Sprintf("err=%q", testError); !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("storing existing quiz fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		originalQuiz := quiz.Quiz{ID: 123456789, Title: "Quiz One", Slug: "quiz-one", Description: "First"}
		updatedQuiz := quiz.Quiz{
			ID:          originalQuiz.ID,
			Title:       originalQuiz.Title + " Updated",
			Slug:        originalQuiz.Slug + "-updated",
			Description: originalQuiz.Description + " Updated",
		}

		testError := errors.New("test error")

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				if id != originalQuiz.ID {
					return nil, quiz.ErrQuizNotFound
				}

				return &originalQuiz, nil
			},
			updateQuiz: func(_ context.Context, _ *quiz.Quiz) error {
				return testError
			},
		}

		handler := admin.HandleQuizSave(logger, quizStore)
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d", updatedQuiz.ID),
			nil,
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", strconv.FormatInt(updatedQuiz.ID, 10))
		req.PostForm = url.Values{
			"title":       {updatedQuiz.Title},
			"slug":        {updatedQuiz.Slug},
			"description": {updatedQuiz.Description},
		}
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}

		log := buf.String()
		if got, want := log, "error updating quiz"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
		if got, want := log, fmt.Sprintf("err=%q", testError); !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})
}

// func TestQuizDataFromQuiz(t *testing.T) {
//	tests := []struct {
//		name string
//		qz   *quiz.Quiz
//		want *admin.QuizData
//	}{
//		{
//			name: "valid quiz with no questions",
//			qz: &quiz.Quiz{
//				ID:          1,
//				Title:       "Test Quiz",
//				Slug:        "test-quiz",
//				Description: "A test quiz.",
//				Questions:   []*quiz.Question{},
//			},
//			want: &admin.QuizData{
//				ID:          1,
//				Title:       "Test Quiz",
//				Slug:        "test-quiz",
//				Description: "A test quiz.",
//				Questions:   []*admin.QuestionData{},
//			},
//		},
//		{
//			name: "quiz with questions",
//			qz: &quiz.Quiz{
//				ID:          2,
//				Title:       "Another Quiz",
//				Slug:        "another-quiz",
//				Description: "Another test quiz.",
//				Questions: []*quiz.Question{
//					{
//						ID:       10,
//						QuizID:   2,
//						Text:     "Sample question?",
//						ImageURL: "",
//						Position: 1,
//						Options:  []*quiz.Option{},
//					},
//				},
//			},
//			want: &admin.QuizData{
//				ID:          2,
//				Title:       "Another Quiz",
//				Slug:        "another-quiz",
//				Description: "Another test quiz.",
//				Questions: []*admin.QuestionData{
//					{
//						ID:       10,
//						QuizID:   2,
//						Text:     "Sample question?",
//						ImageURL: "",
//						Position: 1,
//						Options:  []*admin.OptionData{},
//					},
//				},
//			},
//		},
//	}
//
//	for _, tt := range tests {
//		t.Run(tt.name, func(t *testing.T) {
//			got := admin.quizDataFromQuiz(tt.qz)
//			if diff := cmp.Diff(tt.want, got); diff != "" {
//				t.Errorf("quizDataFromQuiz mismatch (-want +got):\n%s", diff)
//			}
//		})
//	}
// }
