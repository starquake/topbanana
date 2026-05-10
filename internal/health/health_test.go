package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/health"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

type stubQuizStore struct {
	pingErr error
}

func (s *stubQuizStore) Ping(_ context.Context) error                      { return s.pingErr }
func (*stubQuizStore) ListQuizzes(_ context.Context) ([]*quiz.Quiz, error) { return nil, nil }
func (*stubQuizStore) QuestionCountsByQuiz(_ context.Context) (map[int64]int, error) {
	return map[int64]int{}, nil
}

func (*stubQuizStore) GetQuiz(_ context.Context, _ int64) (*quiz.Quiz, error) {
	return nil, errors.ErrUnsupported
}
func (*stubQuizStore) CreateQuiz(_ context.Context, _ *quiz.Quiz) error { return nil }
func (*stubQuizStore) UpdateQuiz(_ context.Context, _ *quiz.Quiz) error { return nil }
func (*stubQuizStore) DeleteQuiz(_ context.Context, _ int64) error      { return nil }
func (*stubQuizStore) ListQuestions(_ context.Context, _ int64) ([]*quiz.Question, error) {
	return nil, nil
}

func (*stubQuizStore) GetQuestion(_ context.Context, _ int64) (*quiz.Question, error) {
	return nil, errors.ErrUnsupported
}
func (*stubQuizStore) CreateQuestion(_ context.Context, _ *quiz.Question) error { return nil }
func (*stubQuizStore) UpdateQuestion(_ context.Context, _ *quiz.Question) error { return nil }
func (*stubQuizStore) DeleteQuestion(_ context.Context, _ int64) error          { return nil }
func (*stubQuizStore) GetOption(_ context.Context, _ int64) (*quiz.Option, error) {
	return nil, errors.ErrUnsupported
}

func (*stubQuizStore) GetOptionsByIDs(_ context.Context, _ []int64) ([]*quiz.Option, error) {
	return nil, nil
}

type stubGameStore struct{}

func (*stubGameStore) Ping(_ context.Context) error { return nil }
func (*stubGameStore) GetGame(_ context.Context, _ string) (*game.Game, error) {
	return nil, errors.ErrUnsupported
}

func (*stubGameStore) GetGameByPlayerAndQuiz(_ context.Context, _, _ int64) (*game.Game, error) {
	return nil, errors.ErrUnsupported
}
func (*stubGameStore) CreateGame(_ context.Context, _ *game.Game) error               { return nil }
func (*stubGameStore) StartGame(_ context.Context, _ string) error                    { return nil }
func (*stubGameStore) CreateParticipant(_ context.Context, _ *game.Participant) error { return nil }
func (*stubGameStore) CreateQuestion(_ context.Context, _ *game.Question) error       { return nil }
func (*stubGameStore) CreateAnswer(_ context.Context, _ *game.Answer) error           { return nil }
func (*stubGameStore) ListAnswersForQuizLeaderboard(
	_ context.Context, _ int64,
) ([]*game.LeaderboardAnswer, error) {
	return nil, nil
}

func (*stubGameStore) DeleteGamesForPlayerOnQuiz(_ context.Context, _, _ int64) error {
	return nil
}

func TestHandleHealthz(t *testing.T) {
	t.Parallel()

	t.Run("returns ok when database is healthy", func(t *testing.T) {
		t.Parallel()
		stores := &store.Stores{
			Quizzes: &stubQuizStore{},
			Games:   &stubGameStore{},
		}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
		w := httptest.NewRecorder()
		health.HandleHealthz(slog.Default(), stores)(w, req)

		if got, want := w.Code, http.StatusOK; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		var res struct {
			Status string            `json:"status"`
			Checks map[string]string `json:"checks"`
		}
		if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if got, want := res.Status, "ok"; got != want {
			t.Errorf("status = %q, want %q", got, want)
		}
		if got, want := res.Checks["database"], "healthy"; got != want {
			t.Errorf("checks.database = %q, want %q", got, want)
		}
	})

	t.Run("returns degraded when database ping fails", func(t *testing.T) {
		t.Parallel()
		stores := &store.Stores{
			Quizzes: &stubQuizStore{pingErr: errors.New("connection refused")},
			Games:   &stubGameStore{},
		}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
		w := httptest.NewRecorder()
		health.HandleHealthz(slog.Default(), stores)(w, req)

		if got, want := w.Code, http.StatusServiceUnavailable; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		var res struct {
			Status string            `json:"status"`
			Checks map[string]string `json:"checks"`
		}
		if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if got, want := res.Status, "degraded"; got != want {
			t.Errorf("status = %q, want %q", got, want)
		}
		if got, want := res.Checks["database"], "unhealthy"; !strings.Contains(got, want) {
			t.Errorf("checks.database = %q, should contain %q", got, want)
		}
	})
}
