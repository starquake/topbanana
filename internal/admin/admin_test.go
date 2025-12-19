package admin_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/quiz"
)

type stubQuizStore struct {
	listQuizzes     func(ctx context.Context) ([]*quiz.Quiz, error)
	getQuizByID     func(ctx context.Context, id int64) (*quiz.Quiz, error)
	createQuiz      func(ctx context.Context, qz *quiz.Quiz) error
	updateQuiz      func(ctx context.Context, qz *quiz.Quiz) error
	getQuestionByID func(ctx context.Context, id int64) (*quiz.Question, error)
	createQuestion  func(ctx context.Context, qs *quiz.Question) error
	updateQuestion  func(ctx context.Context, qs *quiz.Question) error
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

func (s stubQuizStore) UpdateQuestion(ctx context.Context, qs *quiz.Question) error {
	if s.updateQuestion == nil {
		return errors.New("updateQuestion not supplied in stub")
	}

	return s.updateQuestion(ctx, qs)
}

func TestTemplateRenderer_Render_LogsError(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	renderer := admin.NewTemplateRenderer(logger, "admin/pages/quizview.gohtml")

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	if err != nil {
		t.Fatalf("http.NewRequest error: %v", err)
	}
	rr := httptest.NewRecorder()

	data := struct{ UnknownField string }{"trigger"}

	renderer.Render(rr, req, http.StatusOK, data)
	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
	}
	log := buf.String()
	if got, want := log, "error executing template"; !strings.Contains(got, want) {
		t.Fatalf("got: %q, should contain: %q, log: %q", got, want, log)
	}
}

func TestHandleQuizList(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

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
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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

	logger := slog.New(slog.DiscardHandler)
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
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{
					ID: 1234, Title: "Quiz One", Slug: "quiz-one", Description: "First",
					Questions: []*quiz.Question{
						{
							ID: 4567, QuizID: 1234, Text: "Question One",
							Options: []*quiz.Option{
								{Text: "Option 1-1"},
								{Text: "Option 1-2"},
							},
						},
						{
							ID: 4567, QuizID: 1234, Text: "Question Two",
							Options: []*quiz.Option{
								{Text: "Option 2-1"},
								{Text: "Option 2-2"},
							},
						},
					},
				}, nil
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
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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
	logger := slog.New(slog.NewTextHandler(&buf, nil))

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
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		testQuiz := quiz.Quiz{Title: "Quiz One", Slug: "quiz-one", Description: "First"}

		var createdQuizID int64
		var quizzes []*quiz.Quiz
		quizStore := stubQuizStore{
			createQuiz: func(_ context.Context, qz *quiz.Quiz) error {
				createdQuizID = int64(len(quizzes) + 1)
				qz.ID = createdQuizID
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
		if got, want := rr.Header().Get("Location"), fmt.Sprintf("/admin/quizzes/%d", createdQuizID); got != want {
			t.Fatalf("got Location header %q, want %q, log:\n%v", got, want, log)
		}
		if got, want := len(quizzes), 1; got != want {
			t.Fatalf("got %v quizzes, want %v", got, want)
		}
		if diff := cmp.Diff(quizzes[0], &testQuiz,
			cmpopts.IgnoreFields(quiz.Quiz{}, "ID"),
		); diff != "" {
			t.Fatalf("quizzes differ (-got +want):\n%s", diff)
		}
		if got, want := quizzes[0].ID, createdQuizID; got != want {
			t.Fatalf("got quiz ID %d, want %d", got, want)
		}
	})

	t.Run("existing quiz", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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

		form := url.Values{
			"title":       {updatedQuiz.Title},
			"slug":        {updatedQuiz.Slug},
			"description": {updatedQuiz.Description},
		}

		handler := admin.HandleQuizSave(logger, quizStore)
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/admin/quizzes/1",
			strings.NewReader(form.Encode()),
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", strconv.FormatInt(originalQuiz.ID, 10))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Header().Get("Location"), fmt.Sprintf("/admin/quizzes/%d", originalQuiz.ID); got != want {
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
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		quizStore := stubQuizStore{}

		handler := admin.HandleQuizSave(logger, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, "/admin/quizzes/not-an-int/edit", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "not-an-int")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		quizStore := stubQuizStore{}

		handler := admin.HandleQuizSave(logger, quizStore)
		// Request with empty body and no header so parsing the form triggers an error.
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

	t.Run("form is invalid", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		quizStore := stubQuizStore{}

		handler := admin.HandleQuizSave(logger, quizStore)

		form := url.Values{}

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

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Body.String(), "validation errors"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("storing new quiz fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		testQuiz := quiz.Quiz{Title: "Quiz One", Slug: "quiz-one", Description: "First"}

		testError := errors.New("test error")

		quizStore := stubQuizStore{
			createQuiz: func(_ context.Context, _ *quiz.Quiz) error {
				return testError
			},
		}

		form := url.Values{
			"title":       {testQuiz.Title},
			"slug":        {testQuiz.Slug},
			"description": {testQuiz.Description},
		}

		handler := admin.HandleQuizSave(logger, quizStore)
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
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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

		form := url.Values{
			"title":       {updatedQuiz.Title},
			"slug":        {updatedQuiz.Slug},
			"description": {updatedQuiz.Description},
		}

		handler := admin.HandleQuizSave(logger, quizStore)
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d", updatedQuiz.ID),
			strings.NewReader(form.Encode()),
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", strconv.FormatInt(updatedQuiz.ID, 10))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

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
	logger := slog.New(slog.NewTextHandler(&buf, nil))

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

	t.Run("parsing id fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		quizStore := stubQuizStore{}

		handler := admin.HandleQuestionCreate(logger, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, "/admin/quizzes/not-an-int/edit", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "not-an-int")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Body.String(), "error parsing quizID"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("getting quiz fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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
	})
}

func TestHandleQuestionEdit(t *testing.T) {
	t.Parallel()

	t.Run("new question", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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
			fmt.Sprintf("/admin/quizzes/%d/questions/new", testQuiz.ID),
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
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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
			fmt.Sprintf("/admin/quizzes/%d/questions/%d/edit", testQuestion.QuizID, testQuestion.ID),
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
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return nil, quiz.ErrQuizNotFound
			},
		}

		handler := admin.HandleQuestionEdit(logger, quizStore)

		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%d/questions/%d/edit", 1234, 5678),
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
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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
			fmt.Sprintf("/admin/quizzes/%d/questions/%d/edit", 1234, 5678),
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
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		quizStore := stubQuizStore{}

		quizID := "not-an-int"
		questionID := "5678"

		handler := admin.HandleQuestionEdit(logger, quizStore)
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%s/questions/%s/edit", quizID, questionID),
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
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		quizStore := stubQuizStore{}

		quizID := "1234"
		questionID := "not-an-int"

		handler := admin.HandleQuestionEdit(logger, quizStore)
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%s/questions/%s/edit", quizID, questionID),
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

	t.Run("getting question fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		testError := errors.New("test error")

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{}, nil
			},
			getQuestionByID: func(_ context.Context, _ int64) (*quiz.Question, error) {
				return nil, testError
			},
		}

		quizID := "1234"
		questionID := "5678"

		handler := admin.HandleQuestionEdit(logger, quizStore)
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%s/questions/%s/edit", quizID, questionID),
			nil,
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", quizID)
		req.SetPathValue("questionID", questionID)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := buf.String(), fmt.Sprintf("err=%q", testError); !strings.Contains(got, want) {
			t.Fatalf("got: %v, should contain: %q", got, want)
		}
	})
}

func TestHandleQuestionSave(t *testing.T) {
	t.Parallel()

	t.Run("new question", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		testQuiz := quiz.Quiz{
			ID: 1234, Title: "Quiz One", Slug: "quiz-one", Description: "First",
			Questions: []*quiz.Question{
				{
					ID: 5678, QuizID: 1234, Text: "Question One", ImageURL: "https://example.com/image.png", Position: 10,
					Options: []*quiz.Option{
						{Text: "Option 1-1", Correct: true},
						{Text: "Option 1-2"},
						{Text: "Option 1-3"},
					},
				},
				{
					ID: 9012, QuizID: 1234, Text: "Question Two", ImageURL: "https://example.com/image2.png", Position: 20,
					Options: []*quiz.Option{
						{Text: "Option 2-1"},
						{Text: "Option 2-2", Correct: true},
						{Text: "Option 2-3"},
					},
				},
				{
					ID: 3456, QuizID: 1234, Text: "Question Three", ImageURL: "https://example.com/image3.png", Position: 30,
					Options: []*quiz.Option{
						{Text: "Option 3-1"},
						{Text: "Option 3-2"},
						{Text: "Option 3-3", Correct: true},
					},
				},
			},
		}
		testQuestion := quiz.Question{
			QuizID:   testQuiz.ID,
			Text:     "Question Four",
			ImageURL: "https://example.com/image.png",
			Position: 10,
			Options: []*quiz.Option{
				{Text: "Option 1"},
				{Text: "Option 2", Correct: true},
				{Text: "Option 3"},
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
			form.Add(fmt.Sprintf("option[%d].text", i), option.Text)
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
		if got, want := rr.Header().Get("Location"), fmt.Sprintf("/admin/quizzes/%d", testQuiz.ID); got != want {
			t.Fatalf("got Location header %q, want %q", got, want)
		}
		if got, want := len(questions), 1; got != want {
			t.Fatalf("got len(questions) %v, want %v", got, want)
		}
		if diff := cmp.Diff(questions[0], &testQuestion,
			cmpopts.IgnoreFields(quiz.Question{}, "ID"),
			cmpopts.IgnoreFields(quiz.Option{}, "ID", "QuestionID"),
		); diff != "" {
			t.Fatalf("questions differ (-got +want):\n%s", diff)
		}
		if questions[0].ID == 0 {
			t.Fatal("question ID should not be zero")
		}
		for i, option := range questions[0].Options {
			if option.ID == 0 {
				t.Fatalf("option ID for option %d should not be zero", i)
			}
		}
	})

	t.Run("existing question", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		originalQuiz := quiz.Quiz{ID: 1234, Title: "Quiz One", Slug: "quiz-one", Description: "First"}
		originalQuestion := quiz.Question{
			ID:       1,
			QuizID:   originalQuiz.ID,
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
		updatedQuestion := quiz.Question{
			ID:       originalQuestion.ID,
			QuizID:   originalQuestion.QuizID,
			Text:     originalQuestion.Text + " Updated",
			ImageURL: originalQuestion.ImageURL + "?updated",
			Position: originalQuestion.Position + 10,
			Options: []*quiz.Option{
				{
					ID:         originalQuestion.Options[1].ID,
					QuestionID: originalQuestion.ID,
					Text:       originalQuestion.Options[1].Text + " Updated",
					Correct:    !originalQuestion.Options[1].Correct,
				},
				{
					ID:         originalQuestion.Options[2].ID,
					QuestionID: originalQuestion.ID,
					Text:       originalQuestion.Options[2].Text + " Updated",
					Correct:    !originalQuestion.Options[2].Correct,
				},
				{
					QuestionID: originalQuestion.ID,
					Text:       "Option 4 Added",
					Correct:    false,
				},
			},
		}

		var questions []*quiz.Question
		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				if id != originalQuiz.ID {
					return nil, quiz.ErrQuizNotFound
				}

				return &originalQuiz, nil
			},
			getQuestionByID: func(_ context.Context, id int64) (*quiz.Question, error) {
				if id != originalQuestion.ID {
					return nil, quiz.ErrQuestionNotFound
				}

				return &originalQuestion, nil
			},
			updateQuestion: func(_ context.Context, q *quiz.Question) error {
				q.ID = int64(len(questions) + 1)
				for i, option := range q.Options {
					option.ID = int64(i) + 1
					option.QuestionID = q.ID
				}
				questions = append(questions, q)

				return nil
			},
		}

		handler := admin.HandleQuestionSave(logger, quizStore)

		form := url.Values{
			"text":      {updatedQuestion.Text},
			"image_url": {updatedQuestion.ImageURL},
			"position":  {strconv.Itoa(updatedQuestion.Position)},
		}
		for i, option := range updatedQuestion.Options {
			if option.ID != 0 {
				form.Add(fmt.Sprintf("option[%d].id", i), strconv.FormatInt(option.ID, 10))
			}
			form.Add(fmt.Sprintf("option[%d].text", i), option.Text)
			if option.Correct {
				form.Add(fmt.Sprintf("option[%d].correct", i), "on")
			}
		}

		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions/%d", originalQuiz.ID, originalQuestion.ID),
			strings.NewReader(form.Encode()),
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("quizID", strconv.FormatInt(originalQuiz.ID, 10))
		req.SetPathValue("questionID", strconv.FormatInt(originalQuestion.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		log := buf.String()
		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, log)
		}
		if got, want := rr.Header().Get("Location"), fmt.Sprintf("/admin/quizzes/%d", originalQuiz.ID); got != want {
			t.Fatalf("got Location header %q, want %q", got, want)
		}
		if got, want := len(questions), 1; got != want {
			t.Fatalf("got len(questions) %v, want %v", got, want)
		}
		if diff := cmp.Diff(questions[0], &updatedQuestion,
			cmpopts.IgnoreFields(quiz.Option{}, "ID"),
		); diff != "" {
			t.Fatalf("questions differ (-got +want):\n%s", diff)
		}
	})
}

func TestHandleQuestionSave_HandleError(t *testing.T) {
	t.Parallel()

	t.Run("parsing quizID fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		quizStore := stubQuizStore{}

		testQuizID := "not-an-int"

		handler := admin.HandleQuestionSave(logger, quizStore)
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%s/questions", testQuizID),
			strings.NewReader("text=Question One"),
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", testQuizID)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Body.String(), "error parsing quizID"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("parsing questionID fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		quizStore := stubQuizStore{}

		testQuizID := "1234"
		testQuestionID := "not-an-int"

		handler := admin.HandleQuestionSave(logger, quizStore)
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%s/questions/%s", testQuizID, testQuestionID),
			strings.NewReader("text=Question One"),
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", testQuizID)
		req.SetPathValue("questionID", testQuestionID)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Body.String(), "error parsing questionID"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("parsing form fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{}, nil
			},
		}

		handler := admin.HandleQuestionSave(logger, quizStore)
		// Request with empty body and no header so parsing the form triggers an error.
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes/1234/questions", nil)
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

	t.Run("parsing form optionID fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{}, nil
			},
		}

		handler := admin.HandleQuestionSave(logger, quizStore)

		form := url.Values{
			"text":           {""},
			"image_url":      {"http://example.com/image.png"},
			"position":       {"10"},
			"option[0].id":   {"not-an-int"},
			"option[0].text": {"Option 1"},
		}

		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/admin/quizzes/1234/questions",
			strings.NewReader(form.Encode()),
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Body.String(), "error parsing optionID"; !strings.Contains(got, want) {
			t.Fatalf("got: %v, should contain: %q", got, want)
		}
	})

	t.Run("parsing form position fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{}, nil
			},
		}

		handler := admin.HandleQuestionSave(logger, quizStore)

		form := url.Values{
			"text":      {""},
			"image_url": {"http://example.com/image.png"},
		}

		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/admin/quizzes/1234/questions",
			strings.NewReader(form.Encode()),
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Body.String(), "error parsing position"; !strings.Contains(got, want) {
			t.Fatalf("got: %v, should contain: %q", got, want)
		}
	})

	t.Run("form is invalid", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{}, nil
			},
		}

		handler := admin.HandleQuestionSave(logger, quizStore)

		form := url.Values{
			"text":      {""},
			"image_url": {"http://example.com/image.png"},
			"position":  {"10"},
		}

		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/admin/quizzes/1234/questions",
			strings.NewReader(form.Encode()),
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Body.String(), "validation errors"; !strings.Contains(got, want) {
			t.Fatalf("got: %v, should contain: %q", got, want)
		}
	})

	t.Run("storing new question fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		testQuiz := quiz.Quiz{ID: 1234, Title: "Quiz One", Slug: "quiz-one", Description: "First"}
		testQuestion := quiz.Question{
			Text:     "Question One",
			ImageURL: "https://example.com/image.png",
			Position: 10,
			Options: []*quiz.Option{
				{
					Text: "Option 1",
				},
				{
					Text: "Option 2", Correct: true,
				},
				{
					Text: "Option 3",
				},
			},
		}

		testError := errors.New("test error")

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				if id != testQuiz.ID {
					return nil, quiz.ErrQuizNotFound
				}

				return &testQuiz, nil
			},
			createQuestion: func(_ context.Context, _ *quiz.Question) error {
				return testError
			},
		}

		form := url.Values{
			"text":      {testQuestion.Text},
			"image_url": {testQuestion.ImageURL},
			"position":  {strconv.Itoa(testQuestion.Position)},
		}
		for i, option := range testQuestion.Options {
			form.Add(fmt.Sprintf("option[%d].text", i), option.Text)
			if option.Correct {
				form.Add(fmt.Sprintf("option[%d].correct", i), "on")
			}
		}

		handler := admin.HandleQuestionSave(logger, quizStore)
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

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}

		log := buf.String()
		if got, want := log, "error creating question"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
		if got, want := log, fmt.Sprintf("err=%q", testError); !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("storing existing question fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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

		testError := errors.New("test error")

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
			updateQuestion: func(_ context.Context, _ *quiz.Question) error {
				return testError
			},
		}

		form := url.Values{
			"id":        {strconv.FormatInt(testQuestion.ID, 10)},
			"text":      {testQuestion.Text},
			"image_url": {testQuestion.ImageURL},
			"position":  {strconv.Itoa(testQuestion.Position)},
		}
		for i, option := range testQuestion.Options {
			form.Add(fmt.Sprintf("option[%d].text", i), option.Text)
			if option.Correct {
				form.Add(fmt.Sprintf("option[%d].correct", i), "on")
			}
		}

		handler := admin.HandleQuestionSave(logger, quizStore)
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions/%d", testQuiz.ID, testQuestion.ID),
			strings.NewReader(form.Encode()),
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("quizID", strconv.FormatInt(testQuiz.ID, 10))
		req.SetPathValue("questionID", strconv.FormatInt(testQuestion.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}

		log := buf.String()
		if got, want := log, "error updating question"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
		if got, want := log, fmt.Sprintf("err=%q", testError); !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("quiz not found", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return nil, quiz.ErrQuizNotFound
			},
		}

		handler := admin.HandleQuestionSave(logger, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes/1234/questions", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusNotFound; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := buf.String(), "quiz not found"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("question not found", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		testQuiz := quiz.Quiz{ID: 1234, Title: "Quiz One", Slug: "quiz-one", Description: "First"}
		testQuestionID := int64(1)

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return &testQuiz, nil
			},
			getQuestionByID: func(_ context.Context, _ int64) (*quiz.Question, error) {
				return nil, quiz.ErrQuestionNotFound
			},
		}

		handler := admin.HandleQuestionSave(logger, quizStore)
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions/%d", testQuiz.ID, testQuestionID),
			nil,
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", strconv.FormatInt(testQuiz.ID, 10))
		req.SetPathValue("questionID", strconv.FormatInt(testQuestionID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusNotFound; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
	})
}
