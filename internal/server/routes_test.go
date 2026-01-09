package server_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/quiz"
	. "github.com/starquake/topbanana/internal/server"
	"github.com/starquake/topbanana/internal/store"
)

type stubQuizStore struct{}

func (stubQuizStore) Ping(_ context.Context) error {
	return nil
}

func (stubQuizStore) GetQuiz(_ context.Context, id int64) (*quiz.Quiz, error) {
	return &quiz.Quiz{
		ID:          id,
		Title:       "Stub Quiz",
		Slug:        "stub-quiz",
		Description: "stub",
		Questions:   nil,
	}, nil
}

func (stubQuizStore) GetQuestion(_ context.Context, id int64) (*quiz.Question, error) {
	return &quiz.Question{
		ID:       id,
		QuizID:   1,
		Text:     "Stub Question",
		ImageURL: "",
		Position: 1,
		Options:  nil,
	}, nil
}

func (stubQuizStore) ListQuizzes(_ context.Context) ([]*quiz.Quiz, error) {
	return []*quiz.Quiz{
		{ID: 1, Title: "Stub Quiz", Slug: "stub-quiz", Description: "stub"},
	}, nil
}

func (stubQuizStore) CreateQuiz(_ context.Context, _ *quiz.Quiz) error {
	return nil
}

func (stubQuizStore) UpdateQuiz(_ context.Context, _ *quiz.Quiz) error {
	return nil
}

func (stubQuizStore) CreateQuestion(_ context.Context, _ *quiz.Question) error {
	return nil
}

func (stubQuizStore) UpdateQuestion(_ context.Context, _ *quiz.Question) error {
	return nil
}

func (stubQuizStore) ListQuestions(_ context.Context, _ int64) ([]*quiz.Question, error) {
	return nil, nil
}

func (stubQuizStore) GetOption(_ context.Context, _ int64) (*quiz.Option, error) {
	return &quiz.Option{}, nil
}

func TestAddRoutes_RegisteredRoutesDoNot404(t *testing.T) {
	t.Parallel()

	stores := &store.Stores{
		Quizzes: stubQuizStore{},
	}
	mux := http.NewServeMux()
	ExportAddRoutes(mux, slog.New(slog.DiscardHandler), stores, &game.Service{}, &config.Config{})

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "Admin Index", method: http.MethodGet, path: "/admin"},
		{name: "Admin Quiz List", method: http.MethodGet, path: "/admin/quizzes"},
		{name: "Admin Quiz View", method: http.MethodGet, path: "/admin/quizzes/1"},
		{name: "Admin Quiz Create", method: http.MethodGet, path: "/admin/quizzes/new"},
		{name: "Admin Quiz Edit", method: http.MethodGet, path: "/admin/quizzes/1/edit"},

		{name: "Admin Quiz Save (create)", method: http.MethodPost, path: "/admin/quizzes"},
		{name: "Admin Quiz Save (update)", method: http.MethodPost, path: "/admin/quizzes/1"},

		{name: "Question Create", method: http.MethodGet, path: "/admin/quizzes/1/questions/new"},
		{name: "Question Edit", method: http.MethodGet, path: "/admin/quizzes/1/questions/1/edit"},

		{name: "Question Save (create)", method: http.MethodPost, path: "/admin/quizzes/1/questions"},
		{name: "Question Save (update)", method: http.MethodPost, path: "/admin/quizzes/1/questions/1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if rec.Code == http.StatusNotFound {
				t.Errorf("unexpected 404 for %s %s", tc.method, tc.path)
			}
		})
	}
}

func TestAddRoutes_UnknownRouteReturns404(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	stores := &store.Stores{
		Quizzes: stubQuizStore{},
	}
	mux := http.NewServeMux()
	ExportAddRoutes(mux, logger, stores, &game.Service{}, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/unknown/path", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusNotFound; got != want {
		t.Errorf("unexpected status code: got %v, want %v", got, want)
	}
}
