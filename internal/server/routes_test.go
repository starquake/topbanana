package server_test

import (
	"context"
	"errors"
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

var errRouteStub = errors.New("stub")

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

func (stubQuizStore) GetOptionsByIDs(_ context.Context, _ []int64) ([]*quiz.Option, error) {
	return nil, nil
}

type stubGameStore struct{}

func (stubGameStore) Ping(_ context.Context) error { return nil }

func (stubGameStore) GetGame(_ context.Context, _ string) (*game.Game, error) {
	return nil, errRouteStub
}

func (stubGameStore) CreateGame(_ context.Context, _ *game.Game) error { return errRouteStub }
func (stubGameStore) StartGame(_ context.Context, _ string) error      { return errRouteStub }
func (stubGameStore) CreateParticipant(_ context.Context, _ *game.Participant) error {
	return errRouteStub
}
func (stubGameStore) CreateQuestion(_ context.Context, _ *game.Question) error { return errRouteStub }
func (stubGameStore) CreateAnswer(_ context.Context, _ *game.Answer) error     { return errRouteStub }

func TestAddRoutes_RegisteredRoutesDoNot404(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	stores := &store.Stores{
		Quizzes: stubQuizStore{},
	}
	gameSvc := game.NewService(stubGameStore{}, stubQuizStore{}, logger)
	mux := http.NewServeMux()
	ExportAddRoutes(mux, logger, stores, gameSvc, &config.Config{})

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

		{name: "API Quiz List", method: http.MethodGet, path: "/api/quizzes"},
		{name: "API Quiz Get", method: http.MethodGet, path: "/api/quizzes/1"},

		{name: "API Game Create", method: http.MethodPost, path: "/api/games"},
		{name: "API Question Next", method: http.MethodGet, path: "/api/games/game-1/questions/next"},
		{name: "API Answer Post", method: http.MethodPost, path: "/api/games/game-1/questions/1/answers"},
		{name: "API Game Results", method: http.MethodGet, path: "/api/games/game-1/results"},

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
			req := httptest.NewRequestWithContext(context.Background(), tc.method, tc.path, nil)
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
	ExportAddRoutes(mux, logger, stores, game.NewService(stubGameStore{}, stubQuizStore{}, logger), &config.Config{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/unknown/path", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusNotFound; got != want {
		t.Errorf("unexpected status code: got %v, want %v", got, want)
	}
}
