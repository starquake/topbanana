package server_test

import (
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/leaderboard"
	"github.com/starquake/topbanana/internal/livesession"
	"github.com/starquake/topbanana/internal/mailer"
	"github.com/starquake/topbanana/internal/quiz"
	. "github.com/starquake/topbanana/internal/server"
	"github.com/starquake/topbanana/internal/store"
)

// seededAdminID is the player row the migrations insert; a seeded quiz needs
// a creator and this id is guaranteed present on a freshly migrated DB.
const seededAdminID int64 = 1

// newRouter builds the real router over real stores on the given DB. These
// tests assert routing and middleware behavior, so most cases run against an
// empty migrated DB: an unmatched route 404s from the mux, and a matched
// route reaches its handler regardless of whether a row exists.
func newRouter(t *testing.T, db *sql.DB, cfg *config.Config) *http.ServeMux {
	t.Helper()

	logger := slog.New(slog.DiscardHandler)
	stores := store.New(db, logger)
	gameSvc := game.NewService(stores.Games, stores.Quizzes, logger)
	sessionSvc := livesession.NewService(stores.LiveSessions, stores.Quizzes, logger)
	sessionHub := livesession.NewHub()
	sessionSvc.SetPublisher(sessionHub)
	mux := http.NewServeMux()
	realtime := Realtime{
		LeaderboardHub: leaderboard.NewHub(),
		SessionService: sessionSvc,
		SessionHub:     sessionHub,
	}
	ExportAddRoutes(
		mux, logger, stores, gameSvc, realtime, cfg,
		Mail{Tester: mailer.NewTester(mailer.NewNoop())},
	)

	return mux
}

// seedQuiz inserts a public, one-question quiz and returns it with its id
// populated. The first quiz on a fresh DB gets id 1, so its slugID path
// segment is "<slug>-1".
func seedQuiz(t *testing.T, quizzes quiz.Store) *quiz.Quiz {
	t.Helper()

	qz := &quiz.Quiz{
		Title:             "Routes Quiz",
		Slug:              "routes-quiz",
		Description:       "for the routing test",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "A", Correct: true}, {Text: "B"}}},
		},
	}
	if err := quizzes.CreateQuiz(t.Context(), qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}

	return qz
}

// createGame POSTs /api/games as the cookie-jar player, which mints the
// anonymous player row (persisting its session cookie in the jar) and starts
// a game on the quiz. It returns the new game's id so the game-scoped routes
// resolve to a game the same player participates in.
func createGame(t *testing.T, client *http.Client, baseURL string, quizID int64) string {
	t.Helper()

	body := strings.NewReader(`{"quizId":` + strconv.FormatInt(quizID, 10) + `}`)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+"/api/games", body)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/games err = %v, want nil", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("resp.Body.Close err = %v, want nil", cerr)
		}
	}()

	if got, want := resp.StatusCode, http.StatusCreated; got != want {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/games status = %d, want %d, body=%q", got, want, raw)
	}

	var created struct {
		ID string `json:"id"`
	}
	if derr := json.NewDecoder(resp.Body).Decode(&created); derr != nil {
		t.Fatalf("decode create-game response err = %v, want nil", derr)
	}
	if created.ID == "" {
		t.Fatal("create-game response carried an empty game id")
	}

	return created.ID
}

// TestAddRoutes_RegisteredRoutesDoNot404 drives every registered route through
// the real router and asserts none falls through to the mux's not-found
// handler. The quiz- and game-scoped routes look a resource up and would 404
// from inside the handler on an empty DB, so the test seeds a quiz and starts
// a game owned by the request's anonymous player and threads the real ids into
// those paths.
func TestAddRoutes_RegisteredRoutesDoNot404(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	stores := store.New(db, slog.New(slog.DiscardHandler))
	mux := newRouter(t, db, &config.Config{RegistrationEnabled: true})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	client := &http.Client{Jar: jar}

	qz := seedQuiz(t, stores.Quizzes)
	slugID := qz.Slug + "-" + strconv.FormatInt(qz.ID, 10)
	gameID := createGame(t, client, srv.URL, qz.ID)

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
		{name: "API Quiz Leaderboard", method: http.MethodGet, path: "/api/quizzes/" + slugID + "/leaderboard"},
		{name: "API Quiz My Game", method: http.MethodGet, path: "/api/quizzes/" + slugID + "/my-game"},

		{name: "Play Quiz", method: http.MethodGet, path: "/play/" + slugID},

		{name: "API Game Create", method: http.MethodPost, path: "/api/games"},
		{name: "API Question Next", method: http.MethodGet, path: "/api/games/" + gameID + "/questions/next"},
		{name: "API Answer Post", method: http.MethodPost, path: "/api/games/" + gameID + "/questions/1/answers"},
		{name: "API Game Results", method: http.MethodGet, path: "/api/games/" + gameID + "/results"},

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
			// Drive through the cookie-jar client so every request carries
			// the anonymous session minted by createGame; the game- and
			// quiz-scoped routes resolve to the seeded game's owner.
			req, err := http.NewRequestWithContext(t.Context(), tc.method, srv.URL+tc.path, nil)
			if err != nil {
				t.Fatalf("NewRequest err = %v, want nil", err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("client.Do err = %v, want nil", err)
			}
			defer func() {
				if cerr := resp.Body.Close(); cerr != nil {
					t.Errorf("resp.Body.Close err = %v, want nil", cerr)
				}
			}()

			if resp.StatusCode == http.StatusNotFound {
				t.Errorf("unexpected 404 for %s %s", tc.method, tc.path)
			}
		})
	}
}

func TestAddRoutes_RegisterDisabled_Returns404(t *testing.T) {
	t.Parallel()

	// Default-false RegistrationEnabled - /register routes should not be registered.
	mux := newRouter(t, dbtest.Open(t), &config.Config{})

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

// TestAddRoutes_ForgotPassword_GatedOnSMTP: the forgot/reset routes are
// mounted only when SMTP is configured, else they 404 (#1170).
func TestAddRoutes_ForgotPassword_GatedOnSMTP(t *testing.T) {
	t.Parallel()

	paths := []struct {
		name   string
		method string
		path   string
	}{
		{name: "Forgot GET", method: http.MethodGet, path: "/forgot-password"},
		{name: "Forgot POST", method: http.MethodPost, path: "/forgot-password"},
		{name: "Reset GET", method: http.MethodGet, path: "/reset-password"},
		{name: "Reset POST", method: http.MethodPost, path: "/reset-password"},
	}

	t.Run("unconfigured 404s", func(t *testing.T) {
		t.Parallel()

		mux := newRouter(t, dbtest.Open(t), &config.Config{})
		for _, tc := range paths {
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
	})

	t.Run("configured is mounted", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			SessionKey: "test-session-key",
			SMTPHost:   "smtp.example.test",
			SMTPPort:   587,
			SMTPFrom:   "noreply@example.test",
		}
		mux := newRouter(t, dbtest.Open(t), cfg)
		for _, tc := range paths {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, nil)
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)

				if got := rec.Code; got == http.StatusNotFound {
					t.Errorf("unexpected 404 for %s %s when SMTP is configured", tc.method, tc.path)
				}
			})
		}
	})
}

func TestAddRoutes_UnknownRouteReturns404(t *testing.T) {
	t.Parallel()

	mux := newRouter(t, dbtest.Open(t), &config.Config{})

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

	mux := newRouter(t, dbtest.Open(t), &config.Config{SessionKey: "test-session-key"})

	t.Run("missing token returns 403", func(t *testing.T) {
		t.Parallel()

		body := url.Values{
			"displayName": {"any"},
			"password":    {"any"},
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
			"displayName": {"ghost"},
			"password":    {"correctbatterystaple"},
			"csrf_token":  {token},
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

	mux := newRouter(t, dbtest.Open(t), &config.Config{})

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

	mux := newRouter(t, dbtest.Open(t), &config.Config{SessionKey: "test-session-key"})

	body := strings.NewReader(url.Values{}.Encode())
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes/1/delete", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusForbidden; got != want {
		t.Errorf("status = %d, want %d (CSRF must reject before the auth layer redirects)", got, want)
	}
}
