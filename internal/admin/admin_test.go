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
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/quiz"
)

// testAdminID is the player id the admin-handler unit tests pose as
// for owner-gated routes (#281). Owner-gated handlers refuse the
// request when no Player is on context, so tests attach a player via
// withTestAdmin; stub quizzes return CreatedByPlayerID == testAdminID
// so the requireQuizOwner check passes.
const testAdminID int64 = 1

// withTestAdmin returns r with an auth.Player on its context. Drop-in
// substitute for tests that previously skipped the auth middleware —
// the owner gate added in #281 needs the player to know who's asking.
func withTestAdmin(r *http.Request) *http.Request {
	signedIn := &auth.Player{ID: testAdminID, Username: "admin", Role: auth.RoleAdmin}

	return r.WithContext(auth.WithPlayer(r.Context(), signedIn))
}

// stubGameStore satisfies game.Store for admin handler tests; only the
// methods the admin code actually exercises are wired up. The leaderboard
// fetch on the quiz view defaults to "no rows" so test cases that don't
// care about the players list keep working with no extra setup.
type stubGameStore struct {
	listAnswersForQuizLeaderboard func(ctx context.Context, quizID int64) ([]*game.LeaderboardAnswer, error)
	deleteGamesForPlayerOnQuiz    func(ctx context.Context, playerID, quizID int64) error
	listQuizIDsForPlayer          func(ctx context.Context, playerID int64) ([]int64, error)
}

func (stubGameStore) Ping(_ context.Context) error { return nil }

func (stubGameStore) GetGame(_ context.Context, _ string) (*game.Game, error) {
	return nil, errors.ErrUnsupported
}

func (stubGameStore) GetGameByPlayerAndQuiz(_ context.Context, _, _ int64) (*game.Game, error) {
	return nil, game.ErrGameNotFound
}

func (stubGameStore) CreateGame(_ context.Context, _ *game.Game) error {
	return errors.ErrUnsupported
}
func (stubGameStore) StartGame(_ context.Context, _ string) error { return errors.ErrUnsupported }
func (stubGameStore) CreateParticipant(_ context.Context, _ *game.Participant) error {
	return errors.ErrUnsupported
}

func (stubGameStore) CreateQuestion(_ context.Context, _ *game.Question) error {
	return errors.ErrUnsupported
}

func (stubGameStore) CreateAnswer(_ context.Context, _ *game.Answer) error {
	return errors.ErrUnsupported
}

func (s stubGameStore) ListAnswersForQuizLeaderboard(
	ctx context.Context, quizID int64,
) ([]*game.LeaderboardAnswer, error) {
	if s.listAnswersForQuizLeaderboard == nil {
		return nil, nil
	}

	return s.listAnswersForQuizLeaderboard(ctx, quizID)
}

func (s stubGameStore) DeleteGamesForPlayerOnQuiz(
	ctx context.Context, playerID, quizID int64,
) error {
	if s.deleteGamesForPlayerOnQuiz == nil {
		return nil
	}

	return s.deleteGamesForPlayerOnQuiz(ctx, playerID, quizID)
}

func (s stubGameStore) ListQuizIDsForPlayer(ctx context.Context, playerID int64) ([]int64, error) {
	if s.listQuizIDsForPlayer == nil {
		return nil, nil
	}

	return s.listQuizIDsForPlayer(ctx, playerID)
}

// newGameService wires the supplied stubs into a [game.Service] so
// admin handler tests can call the constructor with the real type.
func newGameService(gs stubGameStore, qs stubQuizStore) *game.Service {
	return game.NewService(gs, qs, slog.New(slog.DiscardHandler))
}

type errReader struct{ err error }

func (e errReader) Read(_ []byte) (int, error) { return 0, e.err }
func (errReader) Close() error                 { return nil }

type stubQuizStore struct {
	listQuizzes           func(ctx context.Context) ([]*quiz.Quiz, error)
	questionCountsByQuiz  func(ctx context.Context) (map[int64]int, error)
	getQuizByID           func(ctx context.Context, id int64) (*quiz.Quiz, error)
	quizExists            func(ctx context.Context, id int64) (bool, error)
	createQuiz            func(ctx context.Context, qz *quiz.Quiz) error
	updateQuiz            func(ctx context.Context, qz *quiz.Quiz) error
	deleteQuiz            func(ctx context.Context, id int64) error
	getQuestionByID       func(ctx context.Context, id int64) (*quiz.Question, error)
	createQuestion        func(ctx context.Context, qs *quiz.Question) error
	updateQuestion        func(ctx context.Context, qs *quiz.Question) error
	deleteQuestion        func(ctx context.Context, id int64) error
	listQuestions         func(ctx context.Context, quizID int64) ([]*quiz.Question, error)
	nextQuestionPosition  func(ctx context.Context, quizID int64) (int, error)
	swapQuestionPositions func(ctx context.Context, quizID, questionID int64, direction string) error
}

func (stubQuizStore) Ping(_ context.Context) error {
	return nil
}

func (s stubQuizStore) GetQuiz(ctx context.Context, id int64) (*quiz.Quiz, error) {
	if s.getQuizByID == nil {
		return nil, errors.New("getQuizByID not supplied in stub")
	}

	return s.getQuizByID(ctx, id)
}

func (s stubQuizStore) QuizExists(ctx context.Context, id int64) (bool, error) {
	if s.quizExists == nil {
		return false, errors.ErrUnsupported
	}

	return s.quizExists(ctx, id)
}

func (s stubQuizStore) GetQuestion(ctx context.Context, id int64) (*quiz.Question, error) {
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

func (s stubQuizStore) QuestionCountsByQuiz(ctx context.Context) (map[int64]int, error) {
	if s.questionCountsByQuiz == nil {
		return map[int64]int{}, nil
	}

	return s.questionCountsByQuiz(ctx)
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

func (s stubQuizStore) DeleteQuiz(ctx context.Context, id int64) error {
	if s.deleteQuiz == nil {
		return errors.New("deleteQuiz not supplied in stub")
	}

	return s.deleteQuiz(ctx, id)
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

func (s stubQuizStore) NextQuestionPosition(ctx context.Context, quizID int64) (int, error) {
	if s.nextQuestionPosition == nil {
		return 0, errors.New("nextQuestionPosition not supplied in stub")
	}

	return s.nextQuestionPosition(ctx, quizID)
}

func (s stubQuizStore) SwapQuestionPositions(
	ctx context.Context, quizID, questionID int64, direction string,
) error {
	if s.swapQuestionPositions == nil {
		return errors.New("swapQuestionPositions not supplied in stub")
	}

	return s.swapQuestionPositions(ctx, quizID, questionID, direction)
}

func (s stubQuizStore) DeleteQuestion(ctx context.Context, id int64) error {
	if s.deleteQuestion == nil {
		return errors.New("deleteQuestion not supplied in stub")
	}

	return s.deleteQuestion(ctx, id)
}

func (s stubQuizStore) ListQuestions(ctx context.Context, quizID int64) ([]*quiz.Question, error) {
	if s.listQuestions == nil {
		return nil, errors.New("listQuestions not supplied in stub")
	}

	return s.listQuestions(ctx, quizID)
}

func (stubQuizStore) GetOption(_ context.Context, _ int64) (*quiz.Option, error) {
	return nil, errors.ErrUnsupported
}

func (stubQuizStore) GetOptionsByIDs(_ context.Context, _ []int64) ([]*quiz.Option, error) {
	return nil, errors.New("GetOptionsByIDs not implemented in stub")
}

func TestTemplateRenderer_Render_LogsError(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	renderer := NewTemplateRenderer(logger, nil, "admin/pages/quizview.gohtml")

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

		now := time.Now()
		store := stubQuizStore{
			listQuizzes: func(_ context.Context) ([]*quiz.Quiz, error) {
				return []*quiz.Quiz{
					{
						ID:          1,
						Title:       "Quiz One",
						Slug:        "quiz-one",
						Description: "First",
						UpdatedAt:   now.Add(-2 * time.Hour),
					},
					{
						ID:          2,
						Title:       "Quiz Two",
						Slug:        "quiz-two",
						Description: "Second",
						UpdatedAt:   now.Add(-30 * time.Second),
					},
				}, nil
			},
		}

		handler := HandleQuizList(logger, nil, store)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

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
		// Quiz One: 2 hr ago. Quiz Two: just now. Rendered inside the
		// card's <time> element by humanizeTime — see quizlist.gohtml.
		if got, want := body, "2 hr ago"; !strings.Contains(got, want) {
			t.Errorf("body should contain relative time %q, got: %q", want, got)
		}
		if got, want := body, "just now"; !strings.Contains(got, want) {
			t.Errorf("body should contain relative time %q, got: %q", want, got)
		}
		// Pin a Tailwind utility that's structurally tied to the navbar
		// shell. max-w-shell is a custom theme token from tailwind-src.css
		// (only generated when a class uses it) so its presence proves the
		// reskinned navbar rendered.
		if got, want := body, `class="max-w-shell`; !strings.Contains(got, want) {
			t.Errorf("body got %q, should contain Tailwind shell class %q", got, want)
		}
		// And pin the dark-theme body class flipped on in base.gohtml.
		if got, want := body, `class="bg-bg`; !strings.Contains(got, want) {
			t.Errorf("body got %q, should contain Tailwind dark-bg class %q", got, want)
		}
	})

	t.Run("renders question counts merged from QuestionCountsByQuiz", func(t *testing.T) {
		t.Parallel()

		// Two quizzes; only Quiz One is in the counts map. Quiz Two is
		// absent (zero questions) and should render as "0", proving the
		// missing-key contract holds at the rendering boundary.
		store := stubQuizStore{
			listQuizzes: func(_ context.Context) ([]*quiz.Quiz, error) {
				return []*quiz.Quiz{
					{ID: 1, Title: "Quiz With Five", Slug: "quiz-1", Description: "x"},
					{ID: 2, Title: "Empty Quiz", Slug: "quiz-2", Description: "y"},
				}, nil
			},
			questionCountsByQuiz: func(_ context.Context) (map[int64]int, error) {
				return map[int64]int{1: 5}, nil
			},
		}

		handler := HandleQuizList(logger, nil, store)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}

		body := rr.Body.String()
		// The reskinned card renders the question count inside a <strong>
		// element nested under an /admin/quizzes/{id} link. The count
		// substring is bracketed by ">{count}</strong>" so we don't
		// accidentally match unrelated digits elsewhere on the page.
		if got, want := body, `>5</strong>`; !strings.Contains(got, want) {
			t.Errorf("body got %q, should contain question-count strong %q", got, want)
		}
		if got, want := body, `>0</strong>`; !strings.Contains(got, want) {
			t.Errorf("body got %q, should contain zero-count strong %q (missing key → 0)", got, want)
		}
	})

	t.Run("returns 500 when QuestionCountsByQuiz fails", func(t *testing.T) {
		t.Parallel()

		store := stubQuizStore{
			listQuizzes: func(_ context.Context) ([]*quiz.Quiz, error) {
				return []*quiz.Quiz{{ID: 1, Title: "Q", Slug: "q", Description: ""}}, nil
			},
			questionCountsByQuiz: func(_ context.Context) (map[int64]int, error) {
				return nil, errors.New("count boom")
			},
		}

		handler := HandleQuizList(logger, nil, store)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("no quizzes", func(t *testing.T) {
		t.Parallel()

		store := stubQuizStore{
			listQuizzes: func(_ context.Context) ([]*quiz.Quiz, error) {
				return []*quiz.Quiz{}, nil
			},
		}

		handler := HandleQuizList(logger, nil, store)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

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
		// Pin the dashed-border empty-state container — its border-dashed
		// utility is unique to the Tailwind reskin.
		if got, want := body, `border-dashed`; !strings.Contains(got, want) {
			t.Errorf("body got %q, should contain Tailwind empty-state class %q", got, want)
		}
	})
}

func TestHandleQuizList_RendersNavbarLogout(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	store := stubQuizStore{
		listQuizzes: func(_ context.Context) ([]*quiz.Quiz, error) {
			return []*quiz.Quiz{}, nil
		},
	}
	handler := HandleQuizList(logger, nil, store)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
	// Unit tests don't go through RequireAdmin, so attach the player directly.
	signedIn := &auth.Player{ID: 1, Username: "alice", Role: auth.RoleAdmin}
	req = req.WithContext(auth.WithPlayer(req.Context(), signedIn))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("status code = %v, want %v", got, want)
	}

	body := rr.Body.String()
	if got, want := body, "alice"; !strings.Contains(got, want) {
		t.Errorf("body should contain signed-in username %q, got %q", want, got)
	}
	if got, want := body, `action="/logout"`; !strings.Contains(got, want) {
		t.Errorf("body should contain logout form action %q, got %q", want, got)
	}
	if got, want := body, "Log out"; !strings.Contains(got, want) {
		t.Errorf("body should contain logout button label %q, got %q", want, got)
	}
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

		handler := HandleQuizList(logger, nil, store)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

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
	handler := HandleIndex(logger, nil)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	if err != nil {
		t.Fatalf("http.NewRequest error: %v", err)
	}
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, withTestAdmin(req))

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Errorf("got status code %v, want %v", got, want)
	}
	// invariant pinned by #316: the landing page surfaces a tile for
	// each of the three top-level admin entry points so a fresh admin
	// can discover them without typing URLs.
	body := rr.Body.String()
	for _, want := range []string{
		"Admin Dashboard",
		`href="/admin/quizzes"`,
		`href="/admin/quizzes/new"`,
		`href="/admin/quizzes/import"`,
		"Import quiz",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
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
			quizExists: func(_ context.Context, _ int64) (bool, error) {
				return true, nil
			},
		}

		handler := HandleQuizView(logger, nil, quizStore, newGameService(stubGameStore{}, quizStore))
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		handler := HandleQuizView(logger, nil, quizStore, newGameService(stubGameStore{}, quizStore))
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		handler := HandleQuizView(logger, nil, quizStore, newGameService(stubGameStore{}, quizStore))
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/abc", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "abc")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		handler := HandleQuizView(logger, nil, quizStore, newGameService(stubGameStore{}, quizStore))
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

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

	handler := HandleQuizCreate(logger, nil)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/create", nil)
	if err != nil {
		t.Fatalf("http.NewRequest error: %v", err)
	}
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, withTestAdmin(req))

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
				return &quiz.Quiz{
					ID:                1,
					Title:             "Quiz One",
					Slug:              "quiz-one",
					Description:       "First",
					CreatedByPlayerID: testAdminID,
				}, nil
			},
		}

		handler := HandleQuizEdit(logger, nil, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1/edit", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		handler := HandleQuizEdit(logger, nil, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1/edit", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		handler := HandleQuizEdit(logger, nil, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/abc/edit", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "not-an-int")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		handler := HandleQuizEdit(logger, nil, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1/edit", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		// #99: handler defaults blank time_limit_seconds to the
		// project-wide default so the persisted struct carries 10 even
		// though the form omits the field.
		testQuiz := quiz.Quiz{
			Title:             "Quiz One",
			Slug:              "quiz-one",
			Description:       "First",
			CreatedByPlayerID: testAdminID,
			TimeLimitSeconds:  quiz.DefaultTimeLimitSeconds,
		}

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

		handler := HandleQuizSave(logger, nil, quizStore)

		form := url.Values{
			"title":       {testQuiz.Title},
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

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		originalQuiz := quiz.Quiz{
			ID:                123456789,
			Title:             "Quiz One",
			Slug:              "quiz-one",
			Description:       "First",
			CreatedByPlayerID: testAdminID,
		}
		updatedQuiz := quiz.Quiz{
			ID:                originalQuiz.ID,
			Title:             originalQuiz.Title + " Updated",
			Slug:              "quiz-one-updated",
			Description:       originalQuiz.Description + " Updated",
			CreatedByPlayerID: originalQuiz.CreatedByPlayerID,
			// #99: handler defaults blank time_limit_seconds to the
			// project-wide value.
			TimeLimitSeconds: quiz.DefaultTimeLimitSeconds,
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
			"description": {updatedQuiz.Description},
		}

		handler := HandleQuizSave(logger, nil, quizStore)
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

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		handler := HandleQuizSave(logger, nil, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, "/admin/quizzes/not-an-int/edit", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "not-an-int")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		handler := HandleQuizSave(logger, nil, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1/edit", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		handler := HandleQuizSave(logger, nil, quizStore)
		body := errReader{err: errors.New("simulated read error")}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes", body)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		handler := HandleQuizSave(logger, nil, quizStore)

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

		handler.ServeHTTP(rr, withTestAdmin(req))

		// #32: a validation failure now re-renders the form at 400
		// with per-field error messages instead of a generic
		// "validation errors" page. Assert the messages from the
		// domain Valid map surface inline.
		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		body := rr.Body.String()
		for _, want := range []string{
			"Title is required",
			"Description is required",
			`name="title"`,
			`name="description"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
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

		handler := HandleQuizSave(logger, nil, quizStore)
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

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}

		log := buf.String()
		if got, want := log, "error storing quiz"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
		// The wrapped error carries "create quiz:" so the per-op path is
		// still distinguishable in the log even after the unified
		// "error storing quiz" message (#293).
		if got, want := log, "create quiz: test error"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("storing existing quiz fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		originalQuiz := quiz.Quiz{
			ID:                123456789,
			Title:             "Quiz One",
			Slug:              "quiz-one",
			Description:       "First",
			CreatedByPlayerID: testAdminID,
		}
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

		handler := HandleQuizSave(logger, nil, quizStore)
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

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}

		log := buf.String()
		if got, want := log, "error storing quiz"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
		// The wrapped error carries "update quiz:" so the per-op path is
		// still distinguishable in the log even after the unified
		// "error storing quiz" message (#293).
		if got, want := log, "update quiz: test error"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})
}

func TestHandleQuestionCreate(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	testQuiz := quiz.Quiz{
		ID:                123456789,
		Title:             "Quiz One",
		Slug:              "quiz-one",
		Description:       "First",
		CreatedByPlayerID: testAdminID,
	}

	quizStore := stubQuizStore{
		getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
			if id != testQuiz.ID {
				return nil, quiz.ErrQuizNotFound
			}

			return &testQuiz, nil
		},
	}

	handler := HandleQuestionCreate(logger, nil, quizStore)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1/questions/new", nil)
	if err != nil {
		t.Fatalf("http.NewRequest error: %v", err)
	}
	req.SetPathValue("quizID", strconv.FormatInt(testQuiz.ID, 10))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, withTestAdmin(req))

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
	}
	// The Tailwind navbar doesn't render a "List of Quizzes" link (the
	// brand mark already points at /admin). Pin the navbar by its
	// aria-label, which is the stable accessibility contract for tests
	// (also relied on by the Playwright e2e suite).
	if got, want := rr.Body.String(), `aria-label="Top Banana!"`; !strings.Contains(got, want) {
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

		handler := HandleQuestionCreate(logger, nil, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, "/admin/quizzes/not-an-int/edit", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "not-an-int")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		handler := HandleQuestionCreate(logger, nil, quizStore)

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

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		testQuiz := quiz.Quiz{
			ID:                1234,
			Title:             "Quiz One",
			Slug:              "quiz-one",
			Description:       "First",
			CreatedByPlayerID: testAdminID,
		}

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				if id != testQuiz.ID {
					return nil, quiz.ErrQuizNotFound
				}

				return &testQuiz, nil
			},
		}

		handler := HandleQuestionEdit(logger, nil, quizStore)

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

		handler.ServeHTTP(rr, withTestAdmin(req))

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
			CreatedByPlayerID: testAdminID,
			Questions:         []*quiz.Question{&testQuestion},
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

		handler := HandleQuestionEdit(logger, nil, quizStore)

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

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		handler := HandleQuestionEdit(logger, nil, quizStore)

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

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		testQuiz := quiz.Quiz{
			ID:                1234,
			Title:             "Quiz One",
			Slug:              "quiz-one",
			Description:       "First",
			CreatedByPlayerID: testAdminID,
		}

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

		handler := HandleQuestionEdit(logger, nil, quizStore)

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

		handler.ServeHTTP(rr, withTestAdmin(req))

		log := buf.String()
		if got, want := rr.Code, http.StatusNotFound; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, log)
		}
		if got, want := log, fmt.Sprintf("err=%q", quiz.ErrQuestionNotFound); !strings.Contains(got, want) {
			t.Fatalf("got: %v, should contain: %q, log:\n%v", got, want, log)
		}
	})
}

// TestQuestionEditSave_OptionIDsRoundTrip exercises the full edit-then-save
// loop: render the edit form for a question with four options, scrape the
// resulting <input> name/value pairs the way a browser would, POST them to
// the save handler, and assert that every option's ID survives unchanged.
//
// This catches the class of bug where a form field name in the template
// drifts away from what the handler reads — e.g. "option[2]id" without the
// dot — because the round-trip silently drops those values and the save
// handler then sees option C as an unsaved (ID=0) option.
func TestQuestionEditSave_OptionIDsRoundTrip(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	originalIDs := []int64{11, 22, 33, 44}
	originalTexts := []string{"A", "B", "C", "D"}
	originalCorrect := []bool{true, false, false, false}
	original := quiz.Question{
		ID: 5678, QuizID: 1234, Text: "Q?", Position: 1,
		Options: []*quiz.Option{
			{ID: originalIDs[0], Text: originalTexts[0], Correct: originalCorrect[0]},
			{ID: originalIDs[1], Text: originalTexts[1], Correct: originalCorrect[1]},
			{ID: originalIDs[2], Text: originalTexts[2], Correct: originalCorrect[2]},
			{ID: originalIDs[3], Text: originalTexts[3], Correct: originalCorrect[3]},
		},
	}
	q := quiz.Quiz{
		ID:                1234,
		Title:             "Q1",
		Slug:              "q-1",
		CreatedByPlayerID: testAdminID,
		Questions:         []*quiz.Question{&original},
	}

	// Snapshot the option fields the handler sees at update-time. The handler
	// mutates the loaded question's options in place, so we have to capture
	// values rather than pointers.
	type savedOption struct {
		ID      int64
		Text    string
		Correct bool
	}
	var saved []savedOption
	store := stubQuizStore{
		getQuizByID:     func(_ context.Context, _ int64) (*quiz.Quiz, error) { return &q, nil },
		getQuestionByID: func(_ context.Context, _ int64) (*quiz.Question, error) { return &original, nil },
		updateQuestion: func(_ context.Context, qs *quiz.Question) error {
			saved = make([]savedOption, len(qs.Options))
			for i, o := range qs.Options {
				saved[i] = savedOption{ID: o.ID, Text: o.Text, Correct: o.Correct}
			}

			return nil
		},
	}

	// Step 1: render the edit form.
	editReq := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		fmt.Sprintf("/admin/quizzes/%d/questions/%d/edit", q.ID, original.ID),
		nil,
	)
	editReq.SetPathValue("quizID", strconv.FormatInt(q.ID, 10))
	editReq.SetPathValue("questionID", strconv.FormatInt(original.ID, 10))
	editRec := httptest.NewRecorder()
	HandleQuestionEdit(logger, nil, store).ServeHTTP(editRec, withTestAdmin(editReq))

	if got, want := editRec.Code, http.StatusOK; got != want {
		t.Fatalf("edit form status = %d, want %d", got, want)
	}

	// Step 2: scrape the rendered form's name=value pairs the way a browser
	// would on submit, then POST them to the save handler.
	formBody := scrapeFormFields(t, editRec.Body.String()).Encode()
	saveReq := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		fmt.Sprintf("/admin/quizzes/%d/questions/%d", q.ID, original.ID),
		strings.NewReader(formBody),
	)
	saveReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	saveReq.SetPathValue("quizID", strconv.FormatInt(q.ID, 10))
	saveReq.SetPathValue("questionID", strconv.FormatInt(original.ID, 10))
	saveRec := httptest.NewRecorder()
	HandleQuestionSave(logger, nil, store).ServeHTTP(saveRec, withTestAdmin(saveReq))

	if got, want := saveRec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("save status = %d, want %d (body=%q)", got, want, saveRec.Body.String())
	}

	// Step 3: assert every option field round-tripped. A typo on any
	// option[N].<field> name would drop that field's value during the POST,
	// so the saved value would not match the loaded one.
	if saved == nil {
		t.Fatal("UpdateQuestion was not called")
	}
	if got, want := len(saved), len(originalIDs); got != want {
		t.Fatalf("saved %d options, want %d", got, want)
	}
	for i, opt := range saved {
		if got, want := opt.ID, originalIDs[i]; got != want {
			t.Errorf("option %d ID = %d, want %d (round-trip dropped the ID)", i, got, want)
		}
		if got, want := opt.Text, originalTexts[i]; got != want {
			t.Errorf("option %d Text = %q, want %q (round-trip dropped the text)", i, got, want)
		}
		if got, want := opt.Correct, originalCorrect[i]; got != want {
			t.Errorf("option %d Correct = %v, want %v (round-trip dropped the checkbox)", i, got, want)
		}
	}
}

// scrapeFormFields extracts (name, value) pairs from <input> and <textarea>
// elements in body the way a browser would when submitting the surrounding
// form: disabled fields are skipped, and unchecked checkboxes are excluded.
//
// Limitation: every <input type="submit"> is included. A real browser only
// sends the submit button that was actually clicked, so callers must ensure
// the rendered form has at most one submit input — otherwise the resulting
// POST will include both and the handler may not behave like production.
var (
	inputElementRe    = regexp.MustCompile(`<input\b([^>]*)>`)
	textareaElementRe = regexp.MustCompile(`(?s)<textarea\b([^>]*)>(.*?)</textarea>`)
	inputNameRe       = regexp.MustCompile(`\bname="([^"]+)"`)
	inputValueRe      = regexp.MustCompile(`\bvalue="([^"]*)"`)
	inputTypeRe       = regexp.MustCompile(`\btype="([^"]+)"`)
	disabledAttrRe    = regexp.MustCompile(`\bdisabled\b`)
	checkedAttrRe     = regexp.MustCompile(`\bchecked\b`)
)

func scrapeFormFields(t *testing.T, body string) url.Values {
	t.Helper()

	values := url.Values{}
	for _, match := range inputElementRe.FindAllStringSubmatch(body, -1) {
		attrs := match[1]
		// Browsers do not submit values from disabled fields.
		if disabledAttrRe.MatchString(attrs) {
			continue
		}
		nameMatch := inputNameRe.FindStringSubmatch(attrs)
		if nameMatch == nil {
			continue
		}
		name := nameMatch[1]

		var value string
		if v := inputValueRe.FindStringSubmatch(attrs); v != nil {
			value = v[1]
		}

		// Checkboxes: only included when checked.
		if typeMatch := inputTypeRe.FindStringSubmatch(attrs); typeMatch != nil && typeMatch[1] == "checkbox" {
			if !checkedAttrRe.MatchString(attrs) {
				continue
			}
		}

		values.Add(name, value)
	}

	// Textareas — browsers submit their inner text as the field value.
	for _, match := range textareaElementRe.FindAllStringSubmatch(body, -1) {
		attrs := match[1]
		if disabledAttrRe.MatchString(attrs) {
			continue
		}
		nameMatch := inputNameRe.FindStringSubmatch(attrs)
		if nameMatch == nil {
			continue
		}
		values.Add(nameMatch[1], match[2])
	}

	return values
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

		handler := HandleQuestionEdit(logger, nil, quizStore)
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

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		handler := HandleQuestionEdit(logger, nil, quizStore)
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

		handler.ServeHTTP(rr, withTestAdmin(req))

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
				return &quiz.Quiz{CreatedByPlayerID: testAdminID}, nil
			},
			getQuestionByID: func(_ context.Context, _ int64) (*quiz.Question, error) {
				return nil, testError
			},
		}

		quizID := "1234"
		questionID := "5678"

		handler := HandleQuestionEdit(logger, nil, quizStore)
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

		handler.ServeHTTP(rr, withTestAdmin(req))

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
			CreatedByPlayerID: testAdminID,
			Questions: []*quiz.Question{
				{
					ID:       5678,
					QuizID:   1234,
					Text:     "Question One",
					ImageURL: "https://example.com/image.png",
					Position: 10,
					Options: []*quiz.Option{
						{Text: "Option 1-1", Correct: true},
						{Text: "Option 1-2"},
						{Text: "Option 1-3"},
					},
				},
				{
					ID:       9012,
					QuizID:   1234,
					Text:     "Question Two",
					ImageURL: "https://example.com/image2.png",
					Position: 20,
					Options: []*quiz.Option{
						{Text: "Option 2-1"},
						{Text: "Option 2-2", Correct: true},
						{Text: "Option 2-3"},
					},
				},
				{
					ID:       3456,
					QuizID:   1234,
					Text:     "Question Three",
					ImageURL: "https://example.com/image3.png",
					Position: 30,
					Options: []*quiz.Option{
						{Text: "Option 3-1"},
						{Text: "Option 3-2"},
						{Text: "Option 3-3", Correct: true},
					},
				},
			},
		}
		// Position is auto-assigned by the handler (#16); the
		// existing quiz has questions at positions 10, 20, 30 so the
		// next-position stub returns 40.
		const autoAssignedPosition = 40
		testQuestion := quiz.Question{
			QuizID:   testQuiz.ID,
			Text:     "Question Four",
			ImageURL: "https://example.com/image.png",
			Position: autoAssignedPosition,
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
			nextQuestionPosition: func(_ context.Context, _ int64) (int, error) {
				return autoAssignedPosition, nil
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

		handler := HandleQuestionSave(logger, nil, quizStore)

		form := url.Values{
			"text":      {testQuestion.Text},
			"image_url": {testQuestion.ImageURL},
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

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		originalQuiz := quiz.Quiz{
			ID:                1234,
			Title:             "Quiz One",
			Slug:              "quiz-one",
			Description:       "First",
			CreatedByPlayerID: testAdminID,
		}
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
			// Position no longer comes from the form (#16); the
			// stored position is whatever was loaded from the DB.
			Position: originalQuestion.Position,
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

		handler := HandleQuestionSave(logger, nil, quizStore)

		form := url.Values{
			"text":      {updatedQuestion.Text},
			"image_url": {updatedQuestion.ImageURL},
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

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		handler := HandleQuestionSave(logger, nil, quizStore)
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

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		handler := HandleQuestionSave(logger, nil, quizStore)
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

		handler.ServeHTTP(rr, withTestAdmin(req))

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
				return &quiz.Quiz{CreatedByPlayerID: testAdminID}, nil
			},
		}

		handler := HandleQuestionSave(logger, nil, quizStore)
		body := errReader{err: errors.New("simulated read error")}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes/1234/questions", body)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

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
				return &quiz.Quiz{CreatedByPlayerID: testAdminID}, nil
			},
		}

		handler := HandleQuestionSave(logger, nil, quizStore)

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

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Body.String(), "error parsing optionID"; !strings.Contains(got, want) {
			t.Fatalf("got: %v, should contain: %q", got, want)
		}
	})

	t.Run("form is invalid", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{CreatedByPlayerID: testAdminID}, nil
			},
		}

		handler := HandleQuestionSave(logger, nil, quizStore)

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

		handler.ServeHTTP(rr, withTestAdmin(req))

		// #32: a validation failure re-renders the question form at
		// 400 with the per-field error message inline. Asserting on
		// the message + form field name pins both the FieldErrors map
		// and the template wiring in one shot.
		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		body := rr.Body.String()
		for _, want := range []string{
			"Text is required",
			"Options are required",
			`name="text"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
	})

	t.Run("storing new question fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		testQuiz := quiz.Quiz{
			ID:                1234,
			Title:             "Quiz One",
			Slug:              "quiz-one",
			Description:       "First",
			CreatedByPlayerID: testAdminID,
		}
		testQuestion := quiz.Question{
			Text:     "Question One",
			ImageURL: "https://example.com/image.png",
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
			nextQuestionPosition: func(_ context.Context, _ int64) (int, error) {
				return 10, nil
			},
			createQuestion: func(_ context.Context, _ *quiz.Question) error {
				return testError
			},
		}

		form := url.Values{
			"text":      {testQuestion.Text},
			"image_url": {testQuestion.ImageURL},
		}
		for i, option := range testQuestion.Options {
			form.Add(fmt.Sprintf("option[%d].text", i), option.Text)
			if option.Correct {
				form.Add(fmt.Sprintf("option[%d].correct", i), "on")
			}
		}

		handler := HandleQuestionSave(logger, nil, quizStore)
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

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		testQuiz := quiz.Quiz{
			ID:                1234,
			Title:             "Quiz One",
			Slug:              "quiz-one",
			Description:       "First",
			CreatedByPlayerID: testAdminID,
		}
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

		handler := HandleQuestionSave(logger, nil, quizStore)
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

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		handler := HandleQuestionSave(logger, nil, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes/1234/questions", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

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

		testQuiz := quiz.Quiz{
			ID:                1234,
			Title:             "Quiz One",
			Slug:              "quiz-one",
			Description:       "First",
			CreatedByPlayerID: testAdminID,
		}
		testQuestionID := int64(1)

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return &testQuiz, nil
			},
			getQuestionByID: func(_ context.Context, _ int64) (*quiz.Question, error) {
				return nil, quiz.ErrQuestionNotFound
			},
		}

		handler := HandleQuestionSave(logger, nil, quizStore)
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

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusNotFound; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
	})
}

func TestHandleQuizDelete(t *testing.T) {
	t.Parallel()

	t.Run("delete quiz", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		var deletedID int64
		quizStore := stubQuizStore{
			// Owned by the test admin so requireQuizOwner (#281)
			// lets the delete path through.
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{ID: id, Title: "Q", CreatedByPlayerID: testAdminID}, nil
			},
			deleteQuiz: func(_ context.Context, id int64) error {
				deletedID = id

				return nil
			},
		}

		handler := HandleQuizDelete(logger, nil, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes/1/delete", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Header().Get("Location"), "/admin/quizzes"; got != want {
			t.Fatalf("got Location header %q, want %q", got, want)
		}
		if got, want := deletedID, int64(1); got != want {
			t.Fatalf("deletedID = %d, want %d", got, want)
		}
	})
}

func TestHandleQuizDelete_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("parsing id fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		handler := HandleQuizDelete(logger, nil, stubQuizStore{})
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes/not-an-int/delete", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "not-an-int")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
	})

	t.Run("quiz not found", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		// requireQuizOwner runs first now (#281); a missing quiz
		// surfaces from GetQuiz, not from the DeleteQuiz return path.
		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return nil, quiz.ErrQuizNotFound
			},
		}

		handler := HandleQuizDelete(logger, nil, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes/999/delete", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "999")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusNotFound; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
	})

	t.Run("delete fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		testError := errors.New("test error")

		quizStore := stubQuizStore{
			// Owned by the test admin so requireQuizOwner (#281)
			// passes and the handler reaches DeleteQuiz.
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{ID: id, Title: "Q", CreatedByPlayerID: testAdminID}, nil
			},
			deleteQuiz: func(_ context.Context, _ int64) error {
				return testError
			},
		}

		handler := HandleQuizDelete(logger, nil, quizStore)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes/1/delete", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := buf.String(), fmt.Sprintf("err=%q", testError); !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})
}

func TestHandleQuestionMove(t *testing.T) {
	t.Parallel()

	t.Run("swap succeeds and redirects to quiz view", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		var seen struct {
			quizID, questionID int64
			direction          string
		}
		store := stubQuizStore{
			// Owned by the test admin so requireQuizOwner (#281)
			// passes and the handler reaches SwapQuestionPositions.
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{ID: id, Title: "Q", CreatedByPlayerID: testAdminID}, nil
			},
			swapQuestionPositions: func(_ context.Context, quizID, questionID int64, direction string) error {
				seen.quizID, seen.questionID, seen.direction = quizID, questionID, direction

				return nil
			},
		}

		handler := HandleQuestionMove(logger, nil, store)
		req, err := http.NewRequestWithContext(
			t.Context(), http.MethodPost,
			"/admin/quizzes/7/questions/42/move/up", nil,
		)
		if err != nil {
			t.Fatalf("NewRequest err = %v, want nil", err)
		}
		req.SetPathValue("quizID", "7")
		req.SetPathValue("questionID", "42")
		req.SetPathValue("direction", "up")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("status = %d, want %d, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Header().Get("Location"), "/admin/quizzes/7"; got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
		if got, want := seen.quizID, int64(7); got != want {
			t.Errorf("seen.quizID = %d, want %d", got, want)
		}
		if got, want := seen.questionID, int64(42); got != want {
			t.Errorf("seen.questionID = %d, want %d", got, want)
		}
		if got, want := seen.direction, "up"; got != want {
			t.Errorf("seen.direction = %q, want %q", got, want)
		}
	})

	t.Run("boundary error redirects without surfacing the failure", func(t *testing.T) {
		t.Parallel()
		// ErrQuestionAtTop / ErrQuestionAtBottom happen when the button
		// should already have been disabled in the UI. Treat as a
		// silent no-op: redirect back so the page re-renders.

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		store := stubQuizStore{
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{ID: id, Title: "Q", CreatedByPlayerID: testAdminID}, nil
			},
			swapQuestionPositions: func(_ context.Context, _, _ int64, _ string) error {
				return quiz.ErrQuestionAtTop
			},
		}

		handler := HandleQuestionMove(logger, nil, store)
		req, err := http.NewRequestWithContext(
			t.Context(), http.MethodPost,
			"/admin/quizzes/7/questions/42/move/up", nil,
		)
		if err != nil {
			t.Fatalf("NewRequest err = %v, want nil", err)
		}
		req.SetPathValue("quizID", "7")
		req.SetPathValue("questionID", "42")
		req.SetPathValue("direction", "up")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("status = %d, want %d, log:\n%v", got, want, buf.String())
		}
	})

	t.Run("invalid direction renders 400", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		store := stubQuizStore{
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{ID: id, Title: "Q", CreatedByPlayerID: testAdminID}, nil
			},
			swapQuestionPositions: func(_ context.Context, _, _ int64, _ string) error {
				return quiz.ErrInvalidDirection
			},
		}

		handler := HandleQuestionMove(logger, nil, store)
		req, err := http.NewRequestWithContext(
			t.Context(), http.MethodPost,
			"/admin/quizzes/7/questions/42/move/sideways", nil,
		)
		if err != nil {
			t.Fatalf("NewRequest err = %v, want nil", err)
		}
		req.SetPathValue("quizID", "7")
		req.SetPathValue("questionID", "42")
		req.SetPathValue("direction", "sideways")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d, log:\n%v", got, want, buf.String())
		}
	})
}

func TestHandleQuestionDelete(t *testing.T) {
	t.Parallel()

	t.Run("delete question", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		var deletedID int64
		quizStore := stubQuizStore{
			// Owned by the test admin so requireQuizOwner (#281)
			// passes and the handler reaches DeleteQuestion.
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{ID: id, Title: "Q", CreatedByPlayerID: testAdminID}, nil
			},
			deleteQuestion: func(_ context.Context, id int64) error {
				deletedID = id

				return nil
			},
		}

		handler := HandleQuestionDelete(logger, nil, quizStore)
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/admin/quizzes/1/questions/5/delete",
			nil,
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		req.SetPathValue("questionID", "5")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Header().Get("Location"), "/admin/quizzes/1"; got != want {
			t.Fatalf("got Location header %q, want %q", got, want)
		}
		if got, want := deletedID, int64(5); got != want {
			t.Fatalf("deletedID = %d, want %d", got, want)
		}
	})
}

func TestHandleQuestionDelete_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("parsing quizID fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		handler := HandleQuestionDelete(logger, nil, stubQuizStore{})
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/admin/quizzes/not-an-int/questions/5/delete",
			nil,
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "not-an-int")
		req.SetPathValue("questionID", "5")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
	})

	t.Run("parsing questionID fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		// requireQuizOwner (#281) runs first; supply a legacy quiz so
		// the path reaches the questionID parse failure under test.
		store := stubQuizStore{
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{ID: id, Title: "Q", CreatedByPlayerID: testAdminID}, nil
			},
		}
		handler := HandleQuestionDelete(logger, nil, store)
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/admin/quizzes/1/questions/not-an-int/delete",
			nil,
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		req.SetPathValue("questionID", "not-an-int")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
	})

	t.Run("question not found", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{ID: id, Title: "Q", CreatedByPlayerID: testAdminID}, nil
			},
			deleteQuestion: func(_ context.Context, _ int64) error {
				return quiz.ErrDeletingQuestionNoRowsAffected
			},
		}

		handler := HandleQuestionDelete(logger, nil, quizStore)
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/admin/quizzes/1/questions/999/delete",
			nil,
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		req.SetPathValue("questionID", "999")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusNotFound; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
	})

	t.Run("delete fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		testError := errors.New("test error")

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{ID: id, Title: "Q", CreatedByPlayerID: testAdminID}, nil
			},
			deleteQuestion: func(_ context.Context, _ int64) error {
				return testError
			},
		}

		handler := HandleQuestionDelete(logger, nil, quizStore)
		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/admin/quizzes/1/questions/5/delete",
			nil,
		)
		if err != nil {
			t.Fatalf("http.NewRequest error: %v", err)
		}
		req.SetPathValue("quizID", "1")
		req.SetPathValue("questionID", "5")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := buf.String(), fmt.Sprintf("err=%q", testError); !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})
}

func TestHandleQuizView_RendersPlayedBy(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	t.Run("renders a row per player with reset button", func(t *testing.T) {
		t.Parallel()

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{ID: id, Title: "Q1", Slug: "q-1", CreatedByPlayerID: testAdminID}, nil
			},
			quizExists: func(_ context.Context, _ int64) (bool, error) {
				return true, nil
			},
		}
		// Two leaderboard answers for two distinct players, both correct,
		// so each player accumulates a non-zero score and shows up in the
		// "Played by" table.
		now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
		gameStore := stubGameStore{
			listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*game.LeaderboardAnswer, error) {
				return []*game.LeaderboardAnswer{
					{
						PlayerID: 1, Username: "alice",
						QuestionStartedAt: now, QuestionExpiredAt: now.Add(10 * time.Second),
						AnsweredAt: now, Correct: true,
					},
					{
						PlayerID: 2, Username: "bob",
						QuestionStartedAt: now, QuestionExpiredAt: now.Add(10 * time.Second),
						AnsweredAt: now, Correct: true,
					},
				}, nil
			},
		}

		handler := HandleQuizView(logger, nil, quizStore, newGameService(gameStore, quizStore))
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1", nil)
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}

		body := rr.Body.String()
		if got, want := body, "Played by"; !strings.Contains(got, want) {
			t.Errorf("body should contain section header %q, got %q", want, got)
		}
		if got, want := body, "alice"; !strings.Contains(got, want) {
			t.Errorf("body should contain player name %q, got %q", want, got)
		}
		if got, want := body, "bob"; !strings.Contains(got, want) {
			t.Errorf("body should contain player name %q, got %q", want, got)
		}
		// Anchor on the form action so we know the reset button targets
		// the right URL with the player ID and quiz ID interpolated.
		if got, want := body, `action="/admin/quizzes/1/players/1/reset"`; !strings.Contains(got, want) {
			t.Errorf("body should contain reset form for alice, got %q", got)
		}
		if got, want := body, `action="/admin/quizzes/1/players/2/reset"`; !strings.Contains(got, want) {
			t.Errorf("body should contain reset form for bob, got %q", got)
		}
	})

	t.Run("renders 'No plays yet.' when nobody has played", func(t *testing.T) {
		t.Parallel()

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{ID: id, Title: "Q1", Slug: "q-1", CreatedByPlayerID: testAdminID}, nil
			},
			quizExists: func(_ context.Context, _ int64) (bool, error) {
				return true, nil
			},
		}
		gameStore := stubGameStore{} // no leaderboard rows

		handler := HandleQuizView(logger, nil, quizStore, newGameService(gameStore, quizStore))
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1", nil)
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if got, want := rr.Body.String(), "No plays yet."; !strings.Contains(got, want) {
			t.Errorf("body should contain placeholder %q, got %q", want, got)
		}
	})
}

func TestHandleResetGameForPlayer(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	t.Run("303 redirects back to the quiz page on success", func(t *testing.T) {
		t.Parallel()

		var (
			seenPlayerID int64
			seenQuizID   int64
		)
		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{ID: id, Title: "Q1", CreatedByPlayerID: testAdminID}, nil
			},
			quizExists: func(_ context.Context, _ int64) (bool, error) {
				return true, nil
			},
		}
		gameStore := stubGameStore{
			deleteGamesForPlayerOnQuiz: func(_ context.Context, playerID, quizID int64) error {
				seenPlayerID = playerID
				seenQuizID = quizID

				return nil
			},
		}

		handler := HandleResetGameForPlayer(logger, nil, quizStore, newGameService(gameStore, quizStore))
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost,
			"/admin/quizzes/42/players/7/reset", nil,
		)
		req.SetPathValue("quizID", "42")
		req.SetPathValue("playerID", "7")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if got, want := rr.Header().Get("Location"), "/admin/quizzes/42"; got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
		if got, want := seenPlayerID, int64(7); got != want {
			t.Errorf("delete called with playerID = %d, want %d", got, want)
		}
		if got, want := seenQuizID, int64(42); got != want {
			t.Errorf("delete called with quizID = %d, want %d", got, want)
		}
	})

	t.Run("404 when quiz does not exist", func(t *testing.T) {
		t.Parallel()

		// requireQuizOwner now runs first (#281); a missing quiz is
		// reported by GetQuiz, not by the gameService.ResetGames path.
		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return nil, quiz.ErrQuizNotFound
			},
		}

		handler := HandleResetGameForPlayer(logger, nil, quizStore, newGameService(stubGameStore{}, quizStore))
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost,
			"/admin/quizzes/99/players/7/reset", nil,
		)
		req.SetPathValue("quizID", "99")
		req.SetPathValue("playerID", "7")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("500 when delete fails", func(t *testing.T) {
		t.Parallel()

		quizStore := stubQuizStore{
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{ID: id, CreatedByPlayerID: testAdminID}, nil
			},
			quizExists: func(_ context.Context, _ int64) (bool, error) {
				return true, nil
			},
		}
		gameStore := stubGameStore{
			deleteGamesForPlayerOnQuiz: func(_ context.Context, _, _ int64) error {
				return errors.New("delete boom")
			},
		}

		handler := HandleResetGameForPlayer(logger, nil, quizStore, newGameService(gameStore, quizStore))
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost,
			"/admin/quizzes/1/players/2/reset", nil,
		)
		req.SetPathValue("quizID", "1")
		req.SetPathValue("playerID", "2")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("400 when playerID path value is non-numeric", func(t *testing.T) {
		t.Parallel()

		// Owner gate runs first now (#281); stub a legacy quiz so the
		// 400-on-playerID assertion reflects the real handler path.
		quizStoreLegacy := stubQuizStore{
			getQuizByID: func(_ context.Context, id int64) (*quiz.Quiz, error) {
				return &quiz.Quiz{ID: id, Title: "Legacy", CreatedByPlayerID: testAdminID}, nil
			},
		}
		handler := HandleResetGameForPlayer(
			logger,
			nil,
			quizStoreLegacy,
			newGameService(stubGameStore{}, quizStoreLegacy),
		)
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost,
			"/admin/quizzes/1/players/abc/reset", nil,
		)
		req.SetPathValue("quizID", "1")
		req.SetPathValue("playerID", "abc")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})
}

func TestHumanizeTime(t *testing.T) {
	t.Parallel()

	// Pad each delta a few seconds inside its bucket so test scheduling
	// jitter can't push us across a boundary.
	now := time.Now()
	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{"just now (5s ago)", now.Add(-5 * time.Second), "just now"},
		{"1 minute ago", now.Add(-1*time.Minute - 5*time.Second), "1 min ago"},
		{"5 minutes ago", now.Add(-5*time.Minute - 5*time.Second), "5 min ago"},
		{"1 hour ago", now.Add(-1*time.Hour - 5*time.Second), "1 hr ago"},
		{"3 hours ago", now.Add(-3*time.Hour - 5*time.Second), "3 hr ago"},
		{"1 day ago", now.Add(-24*time.Hour - 5*time.Second), "1 day ago"},
		{"5 days ago", now.Add(-5*24*time.Hour - 5*time.Second), "5 days ago"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got, want := HumanizeTime(tc.t), tc.want; got != want {
				t.Errorf("HumanizeTime() = %q, want %q", got, want)
			}
		})
	}
}
