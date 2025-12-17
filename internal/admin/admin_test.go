package admin_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
	listQuizzes     func(ctx context.Context) ([]*quiz.Quiz, error)
	getQuizByID     func(ctx context.Context, id int64) (*quiz.Quiz, error)
	createQuiz      func(ctx context.Context, qz *quiz.Quiz) error
	updateQuiz      func(ctx context.Context, qz *quiz.Quiz) error
	getQuestionByID func(ctx context.Context, id int64) (*quiz.Question, error)
	createQuestion  func(ctx context.Context, qs *quiz.Question) error
}

func (s stubQuizStore) GetQuizByID(ctx context.Context, id int64) (*quiz.Quiz, error) {
	if s.getQuizByID == nil {
		return nil, errors.New("getQuizByID not supplied in stub")
	}

	return s.getQuizByID(ctx, id)
}

func (s stubQuizStore) GetQuestionByID(ctx context.Context, id int64) (*quiz.Question, error) {
	if s.getQuestionByID == nil {
		return nil, errors.New("getQuestionByID not supplied in stub")
	}

	return s.getQuestionByID(ctx, id)
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

func (s stubQuizStore) CreateQuestion(ctx context.Context, qs *quiz.Question) error {
	if s.createQuestion == nil {
		return errors.New("createQuestion not supplied in stub")
	}

	return s.createQuestion(ctx, qs)
}

func (stubQuizStore) UpdateQuestion(_ context.Context, _ *quiz.Question) error {
	panic("not implemented")
}

func TestHandleQuizList(t *testing.T) {
	t.Parallel()

	logger := logging.NewLogger(io.Discard)

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

	t.Run("list error", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer
		logger := logging.NewLogger(&buf)

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

	logger := logging.NewLogger(io.Discard)
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

	t.Run("get quiz", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

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
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Body.String(), "Quiz One"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("quiz not found", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

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
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := buf.String(), fmt.Sprintf("err=%q", quiz.ErrQuizNotFound); !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})
}

func TestHandleQuizView_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("parsing id fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

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
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := buf.String(), "error parsing quizID"; !strings.Contains(got, want) {
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

		handler := admin.HandleQuizView(logger, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
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
		t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
	}
	if got, want := rr.Body.String(), "Create Quiz"; !strings.Contains(got, want) {
		t.Fatalf("got: %q, should contain: %q", got, want)
	}
}

func TestHandleQuizEdit(t *testing.T) {
	t.Parallel()

	t.Run("quiz found", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

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
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Body.String(), "Edit Quiz"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("quiz not found", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

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
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
	})
}

func TestHandleQuizEdit_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("parsing id fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		quizStore := stubQuizStore{}

		handler := admin.HandleQuizEdit(logger, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/abc/edit", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "not-an-int")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := buf.String(), "error parsing quizID"; !strings.Contains(got, want) {
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

		handler := admin.HandleQuizEdit(logger, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1/edit", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
	})
}

func TestHandleQuizSave(t *testing.T) {
	t.Parallel()

	t.Run("new quiz", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		testQuiz := quiz.Quiz{ID: 1, Title: "Quiz One", Slug: "quiz-one", Description: "First"}

		var quizzes []*quiz.Quiz
		quizStore := stubQuizStore{
			createQuiz: func(_ context.Context, qz *quiz.Quiz) error {
				qz.ID = int64(len(quizzes) + 1)
				quizzes = append(quizzes, qz)

				return nil
			},
		}

		handler := admin.HandleQuizSave(logger, quizStore)

		form := url.Values{
			"title":       {testQuiz.Title},
			"slug":        {testQuiz.Slug},
			"description": {testQuiz.Description},
		}
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/admin/quizzes",
			strings.NewReader(form.Encode()),
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		log := buf.String()
		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, log)
		}
		if got, want := rr.Header().Get("Location"), fmt.Sprintf("/admin/quizzes/%d", testQuiz.ID); got != want {
			t.Fatalf("got Location header %q, want %q, log:\n%v", got, want, log)
		}
		if got, want := len(quizzes), 1; got != want {
			t.Fatalf("got %v quizzes, want %v", got, want)
		}
		if diff := cmp.Diff(quizzes[0], &testQuiz); diff != "" {
			t.Fatalf("quizzes differ (-got +want):\n%s", diff)
		}
	})

	t.Run("existing quiz", func(t *testing.T) {
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

		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Header().Get("Location"), fmt.Sprintf("/admin/quizzes/%d", updatedQuiz.ID); got != want {
			t.Fatalf("got Location header %q, want %q", got, want)
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
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
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
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
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
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
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
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
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
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
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

func TestHandleQuestionCreate(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	testQuiz := quiz.Quiz{ID: 123456789, Title: "Quiz One", Slug: "quiz-one", Description: "First"}

	quizStore := stubQuizStore{
		getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
			if id != testQuiz.ID {
				return nil, quiz.ErrQuizNotFound
			}

			return &testQuiz, nil
		},
	}

	handler := admin.HandleQuestionCreate(logger, quizStore)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1/questions/new", nil)
	if err != nil {
		t.Fatalf("http.NewRequest error: %v", err)
	}
	req.SetPathValue("quizID", strconv.FormatInt(testQuiz.ID, 10))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
	}
	if got, want := rr.Body.String(), "List of Quizzes"; !strings.Contains(got, want) {
		t.Fatalf("got: %v, should contain: %q", got, want)
	}
}

func TestHandleQuestionCreate_ErrorHandling(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	testQuizID := 123456789

	testError := errors.New("test error")

	quizStore := stubQuizStore{
		getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
			return nil, testError
		},
	}

	handler := admin.HandleQuestionCreate(logger, quizStore)

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		fmt.Sprintf("/admin/quizzes/%d/questions/new", testQuizID),
		nil,
	)
	if err != nil {
		t.Fatalf("http.NewRequest error: %v", err)
	}
	req.SetPathValue("quizID", strconv.FormatInt(int64(testQuizID), 10))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	log := buf.String()
	if got, want := rr.Code, http.StatusInternalServerError; got != want {
		t.Fatalf("got status code %v, want %v, log:\n%v", got, want, log)
	}
	if got, want := buf.String(), fmt.Sprintf("err=%q", testError); !strings.Contains(got, want) {
		t.Fatalf("got: %v, should contain: %q", got, want)
	}
}

func TestHandleQuestionEdit(t *testing.T) {
	t.Parallel()

	t.Run("new question", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		testQuiz := quiz.Quiz{ID: 1234, Title: "Quiz One", Slug: "quiz-one", Description: "First"}

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				if id != testQuiz.ID {
					return nil, quiz.ErrQuizNotFound
				}

				return &testQuiz, nil
			},
		}

		handler := admin.HandleQuestionEdit(logger, quizStore)

		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%d/question/new", testQuiz.ID),
			nil,
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", strconv.FormatInt(testQuiz.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
	})

	t.Run("existing question", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		testQuestion := quiz.Question{
			ID:       5678,
			QuizID:   1234,
			Text:     "Question One",
			ImageURL: "https://example.com/image.png",
			Position: 10,
		}
		testQuiz := quiz.Quiz{
			ID: 1234, Title: "Quiz One", Slug: "quiz-one", Description: "First",
			Questions: []*quiz.Question{&testQuestion},
		}

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				if id != testQuiz.ID {
					return nil, quiz.ErrQuizNotFound
				}

				return &testQuiz, nil
			},
			getQuestionByID: func(_ context.Context, id int64) (*quiz.Question, error) {
				if id != testQuestion.ID {
					return nil, quiz.ErrQuestionNotFound
				}

				return &testQuestion, nil
			},
		}

		handler := admin.HandleQuestionEdit(logger, quizStore)

		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%d/question/%d/edit", testQuestion.QuizID, testQuestion.ID),
			nil,
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", strconv.FormatInt(testQuiz.ID, 10))
		req.SetPathValue("questionID", strconv.FormatInt(testQuestion.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
	})

	t.Run("quiz not found", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return nil, quiz.ErrQuizNotFound
			},
		}

		handler := admin.HandleQuestionEdit(logger, quizStore)

		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%d/question/%d/edit", 1234, 5678),
			nil,
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", strconv.Itoa(1234))
		req.SetPathValue("questionID", strconv.Itoa(5678))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		log := buf.String()
		if got, want := rr.Code, http.StatusNotFound; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, log)
		}
		if got, want := log, fmt.Sprintf("err=%q", quiz.ErrQuizNotFound); !strings.Contains(got, want) {
			t.Fatalf("got: %v, should contain: %q, log:\n%v", got, want, log)
		}
	})

	t.Run("question not found", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		testQuiz := quiz.Quiz{ID: 1234, Title: "Quiz One", Slug: "quiz-one", Description: "First"}

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				if id != testQuiz.ID {
					return nil, quiz.ErrQuizNotFound
				}

				return &testQuiz, nil
			},
			getQuestionByID: func(_ context.Context, _ int64) (*quiz.Question, error) {
				return nil, quiz.ErrQuestionNotFound
			},
		}

		handler := admin.HandleQuestionEdit(logger, quizStore)

		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%d/question/%d/edit", 1234, 5678),
			nil,
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", strconv.Itoa(1234))
		req.SetPathValue("questionID", strconv.Itoa(5678))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		log := buf.String()
		if got, want := rr.Code, http.StatusNotFound; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, log)
		}
		if got, want := log, fmt.Sprintf("err=%q", quiz.ErrQuestionNotFound); !strings.Contains(got, want) {
			t.Fatalf("got: %v, should contain: %q, log:\n%v", got, want, log)
		}
	})
}

func TestHandleQuestionEdit_HandleError(t *testing.T) {
	t.Parallel()

	t.Run("parsing quizID fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		quizStore := stubQuizStore{}

		quizID := "not-an-int"
		questionID := "5678"

		handler := admin.HandleQuestionEdit(logger, quizStore)
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%s/question/%s/edit", quizID, questionID),
			nil,
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "not-an-int")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := buf.String(), "error parsing quizID"; !strings.Contains(got, want) {
			t.Fatalf("got: %v, should contain: %q", got, want)
		}
	})

	t.Run("parsing questionID fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		quizStore := stubQuizStore{}

		quizID := "1234"
		questionID := "not-an-int"

		handler := admin.HandleQuestionEdit(logger, quizStore)
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%s/question/%s/edit", quizID, questionID),
			nil,
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", quizID)
		req.SetPathValue("questionID", "questionID")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := buf.String(), "error parsing questionID"; !strings.Contains(got, want) {
			t.Fatalf("got: %v, should contain: %q", got, want)
		}
	})
}

func TestHandleQuestionSave(t *testing.T) {
	t.Parallel()

	t.Run("new question", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		testQuiz := quiz.Quiz{ID: 1234, Title: "Quiz One", Slug: "quiz-one", Description: "First"}
		testQuestion := quiz.Question{
			ID:       1,
			QuizID:   testQuiz.ID,
			Text:     "Question One",
			ImageURL: "https://example.com/image.png",
			Position: 10,
			Options: []*quiz.Option{
				{
					ID: 1, QuestionID: 1, Text: "Option 1",
				},
				{
					ID: 2, QuestionID: 1, Text: "Option 2", Correct: true,
				},
				{
					ID: 3, QuestionID: 1, Text: "Option 3",
				},
			},
		}

		var questions []*quiz.Question
		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				if id != testQuiz.ID {
					return nil, quiz.ErrQuizNotFound
				}

				return &testQuiz, nil
			},
			createQuestion: func(_ context.Context, q *quiz.Question) error {
				q.ID = int64(len(questions) + 1)
				for i, option := range q.Options {
					option.ID = int64(i) + 1 // index starts at 1
					option.QuestionID = q.ID
				}
				questions = append(questions, q)

				return nil
			},
		}

		handler := admin.HandleQuestionSave(logger, quizStore)

		form := url.Values{
			"text":      {testQuestion.Text},
			"image_url": {testQuestion.ImageURL},
			"position":  {strconv.Itoa(testQuestion.Position)},
		}
		for i, option := range testQuestion.Options {
			if option.Text != "" {
				form.Add(fmt.Sprintf("option[%d].text", i), option.Text)
			}
			if option.Correct {
				form.Add(fmt.Sprintf("option[%d].correct", i), "on")
			}
		}

		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions", testQuiz.ID),
			strings.NewReader(form.Encode()),
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("quizID", strconv.FormatInt(testQuiz.ID, 10))

		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		log := buf.String()
		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, log)
		}
		if got, want := rr.Header().Get("Location"), fmt.Sprintf("/admin/quizzes/%d/questions/%d", testQuiz.ID, testQuestion.ID); got != want {
			t.Fatalf("got Location header %q, want %q", got, want)
		}
		if got, want := len(questions), 1; got != want {
			t.Fatalf("got len(questions) %v, want %v", got, want)
		}
		if diff := cmp.Diff(questions[0], &testQuestion); diff != "" {
			t.Fatalf("questions differ (-got +want):\n%s", diff)
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
