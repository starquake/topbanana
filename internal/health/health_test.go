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
	"time"

	"github.com/starquake/topbanana/internal/game"
	. "github.com/starquake/topbanana/internal/health"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// stubQuizStore satisfies quiz.Store for the /healthz handler tests.
// Only Ping is exercised by the handler, so every other method returns
// errors.ErrUnsupported to surface accidental use.
type stubQuizStore struct {
	pingErr error
}

func (s *stubQuizStore) Ping(_ context.Context) error { return s.pingErr }
func (*stubQuizStore) ListQuizzes(_ context.Context) ([]*quiz.Quiz, error) {
	return nil, errors.ErrUnsupported
}

func (*stubQuizStore) ListPublicQuizzes(_ context.Context) ([]*quiz.Quiz, error) {
	return nil, errors.ErrUnsupported
}

func (*stubQuizStore) QuestionCountsByQuiz(_ context.Context) (map[int64]int, error) {
	return nil, errors.ErrUnsupported
}

func (*stubQuizStore) GetQuiz(_ context.Context, _ int64) (*quiz.Quiz, error) {
	return nil, errors.ErrUnsupported
}

func (*stubQuizStore) QuizExists(_ context.Context, _ int64) (bool, error) {
	return false, errors.ErrUnsupported
}
func (*stubQuizStore) CreateQuiz(_ context.Context, _ *quiz.Quiz) error { return errors.ErrUnsupported }
func (*stubQuizStore) UpdateQuiz(_ context.Context, _ *quiz.Quiz) error { return errors.ErrUnsupported }
func (*stubQuizStore) DeleteQuiz(_ context.Context, _ int64) error      { return errors.ErrUnsupported }
func (*stubQuizStore) ListQuestions(_ context.Context, _ int64) ([]*quiz.Question, error) {
	return nil, errors.ErrUnsupported
}

func (*stubQuizStore) GetQuestion(_ context.Context, _ int64) (*quiz.Question, error) {
	return nil, errors.ErrUnsupported
}

func (*stubQuizStore) CreateQuestion(_ context.Context, _ *quiz.Question) error {
	return errors.ErrUnsupported
}

func (*stubQuizStore) CreateQuestionAtNextPosition(_ context.Context, _ *quiz.Question) error {
	return errors.ErrUnsupported
}

func (*stubQuizStore) UpdateQuestion(_ context.Context, _ *quiz.Question) error {
	return errors.ErrUnsupported
}

func (*stubQuizStore) SwapQuestionPositions(_ context.Context, _, _ int64, _ string) error {
	return errors.ErrUnsupported
}

func (*stubQuizStore) DeleteQuestion(_ context.Context, _ int64) error {
	return errors.ErrUnsupported
}

func (*stubQuizStore) GetOption(_ context.Context, _ int64) (*quiz.Option, error) {
	return nil, errors.ErrUnsupported
}

func (*stubQuizStore) GetOptionsByIDs(_ context.Context, _ []int64) ([]*quiz.Option, error) {
	return nil, errors.ErrUnsupported
}

func (*stubQuizStore) ListBreaksByQuiz(_ context.Context, _ int64) ([]*quiz.Break, error) {
	return nil, errors.ErrUnsupported
}

func (*stubQuizStore) GetBreak(_ context.Context, _ int64) (*quiz.Break, error) {
	return nil, errors.ErrUnsupported
}

func (*stubQuizStore) CreateBreak(_ context.Context, _ *quiz.Break) error {
	return errors.ErrUnsupported
}

func (*stubQuizStore) UpdateBreak(_ context.Context, _ *quiz.Break) error {
	return errors.ErrUnsupported
}
func (*stubQuizStore) DeleteBreak(_ context.Context, _ int64) error { return errors.ErrUnsupported }
func (*stubQuizStore) MoveBreak(_ context.Context, _, _ int64, _ string) error {
	return errors.ErrUnsupported
}

// stubGameStore satisfies game.Store for the /healthz handler tests.
// Only Ping is exercised by the handler, so every other method returns
// errors.ErrUnsupported to surface accidental use.
type stubGameStore struct{}

func (*stubGameStore) Ping(_ context.Context) error { return nil }
func (*stubGameStore) GetGame(_ context.Context, _ string) (*game.Game, error) {
	return nil, errors.ErrUnsupported
}

func (*stubGameStore) GetGameByPlayerAndQuiz(_ context.Context, _, _ int64) (*game.Game, error) {
	return nil, errors.ErrUnsupported
}
func (*stubGameStore) CreateGame(_ context.Context, _ *game.Game) error { return errors.ErrUnsupported }
func (*stubGameStore) StartGame(_ context.Context, _ string) error      { return errors.ErrUnsupported }
func (*stubGameStore) CreateParticipant(_ context.Context, _ *game.Participant) error {
	return errors.ErrUnsupported
}

func (*stubGameStore) CreateGameAndParticipant(_ context.Context, _ *game.Game, _ *game.Participant) error {
	return errors.ErrUnsupported
}

func (*stubGameStore) CreateQuestion(_ context.Context, _ *game.Question) error {
	return errors.ErrUnsupported
}

func (*stubGameStore) CreateAnswer(_ context.Context, _ *game.Answer) error {
	return errors.ErrUnsupported
}

func (*stubGameStore) ListAnswersForQuizLeaderboard(
	_ context.Context, _ int64,
) ([]*game.LeaderboardAnswer, error) {
	return nil, errors.ErrUnsupported
}

func (*stubGameStore) ListParticipantsForQuizLeaderboard(
	_ context.Context, _ int64, _ time.Time,
) ([]*game.LeaderboardParticipant, error) {
	return nil, errors.ErrUnsupported
}

func (*stubGameStore) DeleteGamesForPlayerOnQuiz(_ context.Context, _, _ int64) error {
	return errors.ErrUnsupported
}

func (*stubGameStore) ListQuizIDsForPlayer(_ context.Context, _ int64) ([]int64, error) {
	return nil, errors.ErrUnsupported
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
		HandleHealthz(slog.Default(), stores)(w, req)

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
		HandleHealthz(slog.Default(), stores)(w, req)

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
