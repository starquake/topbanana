package server_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/home"
	"github.com/starquake/topbanana/internal/leaderboard"
	"github.com/starquake/topbanana/internal/mailer"
	"github.com/starquake/topbanana/internal/quiz"
	. "github.com/starquake/topbanana/internal/server"
	"github.com/starquake/topbanana/internal/store"
)

type stubPlayerStore struct{}

func (stubPlayerStore) GetPlayerByUsername(_ context.Context, _ string) (*auth.Player, error) {
	return nil, auth.ErrPlayerNotFound
}

func (stubPlayerStore) GetPlayerByEmail(_ context.Context, _ string) (*auth.Player, error) {
	return nil, auth.ErrPlayerNotFound
}

func (stubPlayerStore) GetPlayerByID(_ context.Context, _ int64) (*auth.Player, error) {
	return nil, auth.ErrPlayerNotFound
}

func (stubPlayerStore) CreatePlayer(_ context.Context, _, _, _, _ string) (*auth.Player, error) {
	return nil, errRouteStub
}

func (stubPlayerStore) CreateAnonymousPlayer(_ context.Context, _ string) (*auth.Player, error) {
	return nil, errRouteStub
}

func (stubPlayerStore) ClaimPlayer(_ context.Context, _ int64, _, _, _, _ string) (*auth.Player, error) {
	return nil, errRouteStub
}

func (stubPlayerStore) SetPlayerPasswordHash(_ context.Context, _, _ string) error {
	return errRouteStub
}

func (stubPlayerStore) UpdatePlayerUsername(_ context.Context, _ int64, _ string) (*auth.Player, error) {
	return nil, errRouteStub
}

func (stubPlayerStore) RenamePlayer(_ context.Context, _ int64, _ string) (*auth.Player, error) {
	return nil, errRouteStub
}

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

func (stubQuizStore) QuizExists(_ context.Context, _ int64) (bool, error) {
	return true, nil
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

func (s stubQuizStore) ListPublicQuizzes(ctx context.Context) ([]*quiz.Quiz, error) {
	return s.ListQuizzes(ctx)
}

func (stubQuizStore) QuestionCountsByQuiz(_ context.Context) (map[int64]int, error) {
	return map[int64]int{}, nil
}

func (stubQuizStore) CreateQuiz(_ context.Context, _ *quiz.Quiz) error {
	return errRouteStub
}

func (stubQuizStore) UpdateQuiz(_ context.Context, _ *quiz.Quiz) error {
	return errRouteStub
}

func (stubQuizStore) DeleteQuiz(_ context.Context, _ int64) error {
	return errRouteStub
}

func (stubQuizStore) CreateQuestion(_ context.Context, _ *quiz.Question) error {
	return errRouteStub
}

func (stubQuizStore) CreateQuestionAtNextPosition(_ context.Context, _ *quiz.Question) error {
	return errRouteStub
}

func (stubQuizStore) UpdateQuestion(_ context.Context, _ *quiz.Question) error {
	return errRouteStub
}

func (stubQuizStore) SwapQuestionPositions(_ context.Context, _, _ int64, _ string) error {
	return errRouteStub
}

func (stubQuizStore) DeleteQuestion(_ context.Context, _ int64) error {
	return errRouteStub
}

func (stubQuizStore) ListQuestions(_ context.Context, _ int64) ([]*quiz.Question, error) {
	return nil, errRouteStub
}

func (stubQuizStore) GetOption(_ context.Context, _ int64) (*quiz.Option, error) {
	return nil, errRouteStub
}

func (stubQuizStore) GetOptionsByIDs(_ context.Context, _ []int64) ([]*quiz.Option, error) {
	return nil, errRouteStub
}

func (stubQuizStore) ListBreaksByQuiz(_ context.Context, _ int64) ([]*quiz.Break, error) {
	return nil, errRouteStub
}

func (stubQuizStore) GetBreak(_ context.Context, _ int64) (*quiz.Break, error) {
	return nil, errRouteStub
}

func (stubQuizStore) CreateBreak(_ context.Context, _ *quiz.Break) error {
	return errRouteStub
}

func (stubQuizStore) UpdateBreak(_ context.Context, _ *quiz.Break) error {
	return errRouteStub
}

func (stubQuizStore) DeleteBreak(_ context.Context, _ int64) error {
	return errRouteStub
}

func (stubQuizStore) MoveBreak(_ context.Context, _, _ int64, _ string) error {
	return errRouteStub
}

type stubGameStore struct{}

func (stubGameStore) Ping(_ context.Context) error { return nil }

func (stubGameStore) GetGame(_ context.Context, _ string) (*game.Game, error) {
	return nil, errRouteStub
}

func (stubGameStore) GetGameByPlayerAndQuiz(_ context.Context, _, _ int64) (*game.Game, error) {
	return nil, game.ErrGameNotFound
}

func (stubGameStore) CreateGame(_ context.Context, _ *game.Game) error { return errRouteStub }
func (stubGameStore) StartGame(_ context.Context, _ string) error      { return errRouteStub }
func (stubGameStore) CreateParticipant(_ context.Context, _ *game.Participant) error {
	return errRouteStub
}

func (stubGameStore) CreateGameAndParticipant(_ context.Context, _ *game.Game, _ *game.Participant) error {
	return errRouteStub
}
func (stubGameStore) CreateQuestion(_ context.Context, _ *game.Question) error { return errRouteStub }
func (stubGameStore) CreateAnswer(_ context.Context, _ *game.Answer) error     { return errRouteStub }
func (stubGameStore) ListAnswersForQuizLeaderboard(
	_ context.Context, _ int64,
) ([]*game.LeaderboardAnswer, error) {
	return nil, errRouteStub
}

func (stubGameStore) ListParticipantsForQuizLeaderboard(
	_ context.Context, _ int64, _ time.Time,
) ([]*game.LeaderboardParticipant, error) {
	return nil, errRouteStub
}

func (stubGameStore) DeleteGamesForPlayerOnQuiz(_ context.Context, _, _ int64) error {
	return errRouteStub
}

func (stubGameStore) ListQuizIDsForPlayer(_ context.Context, _ int64) ([]int64, error) {
	return nil, errRouteStub
}

func (stubGameStore) MarkBreakSeen(_ context.Context, _ string, _ int64) error {
	return errRouteStub
}

func (stubGameStore) ListSeenBreakIDsByGame(_ context.Context, _ string) ([]int64, error) {
	return nil, errRouteStub
}

type stubHomeStore struct{}

func (stubHomeStore) ListPopularQuizzes(_ context.Context) ([]*home.PopularQuiz, error) {
	return nil, errRouteStub
}

func (stubHomeStore) ListMostActivePlayers(_ context.Context) ([]*home.ActivePlayer, error) {
	return nil, errRouteStub
}

func TestAddRoutes_RegisteredRoutesDoNot404(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	stores := &store.Stores{
		Quizzes: stubQuizStore{},
		Players: stubPlayerStore{},
		Home:    stubHomeStore{},
	}
	gameSvc := game.NewService(stubGameStore{}, stubQuizStore{}, logger)
	mux := http.NewServeMux()
	ExportAddRoutes(
		mux, logger, stores, gameSvc, leaderboard.NewHub(),
		&config.Config{RegistrationEnabled: true},
		mailer.NewTester(mailer.NewNoop()), mailer.StatusView{},
	)

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
		{name: "Admin Reset Player", method: http.MethodPost, path: "/admin/quizzes/1/players/2/reset"},

		{name: "API Quiz List", method: http.MethodGet, path: "/api/quizzes"},
		{name: "API Quiz Get", method: http.MethodGet, path: "/api/quizzes/1"},
		{name: "API Quiz Leaderboard", method: http.MethodGet, path: "/api/quizzes/quiz-1/leaderboard"},
		{name: "API Quiz My Game", method: http.MethodGet, path: "/api/quizzes/quiz-1/my-game"},

		{name: "Play Quiz", method: http.MethodGet, path: "/play/quiz-1"},

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

		{name: "Home Start Page", method: http.MethodGet, path: "/"},

		{name: "Auth Register GET", method: http.MethodGet, path: "/register"},
		{name: "Auth Register POST", method: http.MethodPost, path: "/register"},
		{name: "Auth Login GET", method: http.MethodGet, path: "/login"},
		{name: "Auth Login POST", method: http.MethodPost, path: "/login"},
		{name: "Auth Logout POST", method: http.MethodPost, path: "/logout"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, nil)
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if rec.Code == http.StatusNotFound {
				t.Errorf("unexpected 404 for %s %s", tc.method, tc.path)
			}
		})
	}
}

func TestAddRoutes_RegisterDisabled_Returns404(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	stores := &store.Stores{
		Quizzes: stubQuizStore{},
		Players: stubPlayerStore{},
		Home:    stubHomeStore{},
	}
	gameSvc := game.NewService(stubGameStore{}, stubQuizStore{}, logger)
	mux := http.NewServeMux()
	// Default-false RegistrationEnabled - /register routes should not be registered.
	ExportAddRoutes(
		mux, logger, stores, gameSvc, leaderboard.NewHub(), &config.Config{},
		mailer.NewTester(mailer.NewNoop()), mailer.StatusView{},
	)

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "Auth Register GET disabled", method: http.MethodGet, path: "/register"},
		{name: "Auth Register POST disabled", method: http.MethodPost, path: "/register"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, nil)
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if got, want := rec.Code, http.StatusNotFound; got != want {
				t.Errorf("status = %d, want %d for %s %s", got, want, tc.method, tc.path)
			}
		})
	}
}

func TestAddRoutes_UnknownRouteReturns404(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	stores := &store.Stores{
		Quizzes: stubQuizStore{},
		Players: stubPlayerStore{},
		Home:    stubHomeStore{},
	}
	mux := http.NewServeMux()
	ExportAddRoutes(
		mux,
		logger,
		stores,
		game.NewService(stubGameStore{}, stubQuizStore{}, logger),
		leaderboard.NewHub(),
		&config.Config{},
		mailer.NewTester(mailer.NewNoop()), mailer.StatusView{},
	)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/unknown/path", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusNotFound; got != want {
		t.Errorf("unexpected status code: got %v, want %v", got, want)
	}
}

// csrfFormPattern extracts the value of the hidden csrf_token form field from
// a rendered HTML form.
var csrfFormPattern = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

// TestAddRoutes_LoginPOST_RejectsMissingCSRF verifies the CSRF middleware
// short-circuits an unsafe request that does not carry a token, and accepts
// the same request once the token is provided. /login is convenient here
// because it does not require an admin session - only the CSRF middleware
// stands between the request and the handler.
func TestAddRoutes_LoginPOST_RejectsMissingCSRF(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	stores := &store.Stores{
		Quizzes: stubQuizStore{},
		Players: stubPlayerStore{},
		Home:    stubHomeStore{},
	}
	mux := http.NewServeMux()
	cfg := &config.Config{SessionKey: "test-session-key"}
	ExportAddRoutes(
		mux,
		logger,
		stores,
		game.NewService(stubGameStore{}, stubQuizStore{}, logger),
		leaderboard.NewHub(),
		cfg,
		mailer.NewTester(mailer.NewNoop()), mailer.StatusView{},
	)

	t.Run("missing token returns 403", func(t *testing.T) {
		t.Parallel()

		body := url.Values{
			"username": {"any"},
			"password": {"any"},
		}.Encode()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusForbidden; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("valid token reaches handler", func(t *testing.T) {
		t.Parallel()

		// Step 1: GET /login to receive the nonce cookie and the matching
		// hidden token rendered in the form.
		getReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil)
		getRec := httptest.NewRecorder()
		mux.ServeHTTP(getRec, getReq)

		if got, want := getRec.Code, http.StatusOK; got != want {
			t.Fatalf("GET /login status = %d, want %d", got, want)
		}

		match := csrfFormPattern.FindStringSubmatch(getRec.Body.String())
		if match == nil {
			t.Fatalf("no csrf_token field found in /login HTML, body=%q", getRec.Body.String())
		}
		token := match[1]

		var nonce *http.Cookie
		for _, c := range getRec.Result().Cookies() {
			if c.Name == csrf.CookieName {
				nonce = c

				break
			}
		}
		if nonce == nil {
			t.Fatalf("no %q cookie found on GET /login", csrf.CookieName)
		}

		// Step 2: POST /login with the token in the form and the cookie on
		// the request. The POST should pass CSRF validation and reach the
		// handler, which in turn returns 401 for the unknown user - that's
		// fine, the assertion here is "not 403".
		body := url.Values{
			"username":   {"ghost"},
			"password":   {"correctbatterystaple"},
			"csrf_token": {token},
		}.Encode()
		postReq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/login", strings.NewReader(body))
		postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		postReq.AddCookie(nonce)
		postRec := httptest.NewRecorder()
		mux.ServeHTTP(postRec, postReq)

		if got := postRec.Code; got == http.StatusForbidden {
			t.Errorf("status = %d (forbidden); CSRF should have passed with a valid token", got)
		}
	})
}

func TestAddRoutes_AdminRouteWithoutSession_RedirectsToLogin(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	stores := &store.Stores{
		Quizzes: stubQuizStore{},
		Players: stubPlayerStore{},
		Home:    stubHomeStore{},
	}
	mux := http.NewServeMux()
	ExportAddRoutes(
		mux,
		logger,
		stores,
		game.NewService(stubGameStore{}, stubQuizStore{}, logger),
		leaderboard.NewHub(),
		&config.Config{},
		mailer.NewTester(mailer.NewNoop()), mailer.StatusView{},
	)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	// #449: GET to a protected admin route carries the original URI as
	// ?next=<encoded> so the login flow can drop the visitor back on
	// the page they tried to reach.
	if got, want := rec.Header().Get("Location"), "/login?next=%2Fadmin%2Fquizzes"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// TestAddRoutes_AdminPOSTWithoutCSRF_Returns403_NotAuthRedirect locks in the
// middleware order on admin POST routes: csrfMW(requireAdmin(...)). An
// anonymous POST without a CSRF token should be rejected with 403 from the
// CSRF layer rather than 303-redirected to /login by the auth layer, so we
// don't leak whether a session would have been honoured.
func TestAddRoutes_AdminPOSTWithoutCSRF_Returns403_NotAuthRedirect(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	stores := &store.Stores{
		Quizzes: stubQuizStore{},
		Players: stubPlayerStore{},
		Home:    stubHomeStore{},
	}
	mux := http.NewServeMux()
	cfg := &config.Config{SessionKey: "test-session-key"}
	ExportAddRoutes(
		mux,
		logger,
		stores,
		game.NewService(stubGameStore{}, stubQuizStore{}, logger),
		leaderboard.NewHub(),
		cfg,
		mailer.NewTester(mailer.NewNoop()), mailer.StatusView{},
	)

	body := strings.NewReader(url.Values{}.Encode())
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes/1/delete", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusForbidden; got != want {
		t.Errorf("status = %d, want %d (CSRF must reject before the auth layer redirects)", got, want)
	}
}
