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

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/livesession"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/quiz"
)

// newGameService wires the env's real stores into a fresh [game.Service]
// so the handler constructors that take a *game.Service can be called
// with the real type.
func (e *adminEnv) newGameService() *game.Service {
	return game.NewService(e.games, e.quizzes, e.logger)
}

func TestHandleQuizList(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	t.Run("list quizzes", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		one := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))
		two := env.seedQuiz(t, ownedQuiz("Quiz Two", "quiz-two"))
		// Backdate so the relative-time rendering has a stable "2 hr ago"
		// bucket for one row and "just now" for the other.
		now := time.Now()
		env.backdateQuizUpdatedAt(t, one.ID, now.Add(-2*time.Hour))
		env.backdateQuizUpdatedAt(t, two.ID, now.Add(-30*time.Second))

		handler := HandleQuizList(logger, nil, env.quizzes)

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
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
		// card's <time> element by humanizeTime - see quizlist.gohtml.
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

		// Quiz One carries five questions; Empty Quiz carries none. The
		// missing-key contract renders the empty quiz's count as "0".
		env := newAdminEnv(t)
		withFive := ownedQuiz("Quiz With Five", "quiz-1")
		for i := range 5 {
			withFive.Questions = append(withFive.Questions, &quiz.Question{
				Text:     fmt.Sprintf("Q%d", i+1),
				Position: i + 1,
				Options: []*quiz.Option{
					{Text: "yes", Correct: true},
					{Text: "no"},
				},
			})
		}
		env.seedQuiz(t, withFive)
		env.seedQuiz(t, ownedQuiz("Empty Quiz", "quiz-2"))

		handler := HandleQuizList(logger, nil, env.quizzes)
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
			t.Errorf("body got %q, should contain zero-count strong %q (missing key -> 0)", got, want)
		}
	})

	t.Run("returns 500 when QuestionCountsByQuiz fails", func(t *testing.T) {
		t.Parallel()

		// A closed DB fails the list page's store reads, so the handler
		// renders 500. ListQuizzes happens to fail first against a closed
		// connection; the assertion pins the 5xx the page returns when a
		// backing query errors, which is what this case guards.
		env := newAdminEnv(t)
		env.seedQuiz(t, ownedQuiz("Q", "q"))
		env.closeStore(t)

		handler := HandleQuizList(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("no quizzes", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)

		handler := HandleQuizList(logger, nil, env.quizzes)

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
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
		// Pin the dashed-border empty-state container - its border-dashed
		// utility is unique to the Tailwind reskin.
		if got, want := body, `border-dashed`; !strings.Contains(got, want) {
			t.Errorf("body got %q, should contain Tailwind empty-state class %q", got, want)
		}
	})
}

func TestHandleQuizList_RendersNavbarLogout(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	handler := HandleQuizList(slog.New(slog.DiscardHandler), nil, env.quizzes)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
	// Pose as a distinct admin to pin the navbar's signed-in display name.
	signedIn := &auth.Player{ID: testAdminID, DisplayName: "alice", Role: auth.RoleAdmin}
	req = req.WithContext(auth.WithPlayer(req.Context(), signedIn))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("status code = %v, want %v", got, want)
	}

	body := rr.Body.String()
	if got, want := body, "alice"; !strings.Contains(got, want) {
		t.Errorf("body should contain signed-in displayName %q, got %q", want, got)
	}
	if got, want := body, `action="/logout"`; !strings.Contains(got, want) {
		t.Errorf("body should contain logout form action %q, got %q", want, got)
	}
	if got, want := body, "Log out"; !strings.Contains(got, want) {
		t.Errorf("body should contain logout button label %q, got %q", want, got)
	}
}

func TestHandleQuizList_RendersPlayModeBadges(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	env := newAdminEnv(t)

	env.seedQuiz(t, ownedQuiz("Self-paced Quiz", "solo-quiz"))
	live := ownedQuiz("Hosted Quiz", "live-quiz")
	live.Mode = quiz.ModeLive
	env.seedQuiz(t, live)

	handler := HandleQuizList(logger, nil, env.quizzes)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, withTestAdmin(req))

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}

	// Each play-mode badge carries its own modifier class plus an inline
	// Lucide icon before the label (#890). Match the open tag through the
	// label so a restyle that drops either the class or the label fails
	// here, while tolerating the SVG markup in between.
	body := rr.Body.String()
	liveRe := regexp.MustCompile(`(?s)class="pill pill-live">.*?Live`)
	if !liveRe.MatchString(body) {
		t.Errorf("body = %q, should contain live badge matching %s", body, liveRe)
	}
	soloRe := regexp.MustCompile(`(?s)class="pill pill-solo">.*?Solo`)
	if !soloRe.MatchString(body) {
		t.Errorf("body = %q, should contain solo badge matching %s", body, soloRe)
	}
}

func TestHandleQuizList_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("list error", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)
		env.closeStore(t)

		handler := HandleQuizList(logger, nil, env.quizzes)

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("status code = %v, want %v", got, want)
		}
		if got, want := buf.String(), "error retrieving quizzes from store"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})
}

func TestHandleQuizView(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	t.Run("get quiz", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz One", "quiz-one"))

		handler := HandleQuizView(logger, nil, env.quizzes, env.newGameService(), runningGameLookup{}, mediaLister{})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1", nil)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
		if got, want := rr.Body.String(), "Quiz One"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("quiz not found", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)

		handler := HandleQuizView(logger, nil, env.quizzes, env.newGameService(), runningGameLookup{}, mediaLister{})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/999", nil)
		req.SetPathValue("quizID", "999")
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

// TestHandleQuizView_RestartModalGating pins the confirm-and-restart gating
// (#853): on a live quiz, the restart modal and its hidden restart=true field
// render only when the host already has a game in flight; otherwise the plain
// Host live form has no restart field and no modal.
func TestHandleQuizView_RestartModalGating(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	seedLive := func(t *testing.T, env *adminEnv) *quiz.Quiz {
		t.Helper()
		live := twoQuestionQuiz("Hostable Quiz", "hostable-quiz")
		live.Mode = quiz.ModeLive

		return env.seedQuiz(t, live)
	}

	viewBody := func(t *testing.T, env *adminEnv, qz *quiz.Quiz, running RunningGameLookup) string {
		t.Helper()
		handler := HandleQuizView(logger, nil, env.quizzes, env.newGameService(), running, mediaLister{})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1", nil)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))
		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("quiz view status = %d, want %d", got, want)
		}

		return rr.Body.String()
	}

	t.Run("running game shows the restart modal and field", func(t *testing.T) {
		t.Parallel()
		env := newAdminEnv(t)
		qz := seedLive(t, env)

		body := viewBody(t, env, qz, runningGameLookup{running: true})
		for _, want := range []string{
			"modal-restart-host-" + strconv.FormatInt(qz.ID, 10),
			`name="restart"`,
			`value="true"`,
			"End and start",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("running-game quiz view missing %q", want)
			}
		}
	})

	t.Run("no running game shows the plain form without restart", func(t *testing.T) {
		t.Parallel()
		env := newAdminEnv(t)
		qz := seedLive(t, env)

		body := viewBody(t, env, qz, runningGameLookup{running: false})
		// The plain Host live form still renders, but with no restart field and no
		// restart modal.
		if !strings.Contains(body, "Host live") {
			t.Error("no-running-game quiz view missing the Host live control")
		}
		if strings.Contains(body, `name="restart"`) {
			t.Error("no-running-game quiz view rendered the restart hidden field")
		}
		if strings.Contains(body, "modal-restart-host-") {
			t.Error("no-running-game quiz view rendered the restart modal")
		}
	})
}

func TestHandleQuizView_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("parsing id fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)

		handler := HandleQuizView(logger, nil, env.quizzes, env.newGameService(), runningGameLookup{}, mediaLister{})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/abc", nil)
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

		env := newAdminEnv(t)
		env.seedQuiz(t, ownedQuiz("Q", "q"))
		env.closeStore(t)

		handler := HandleQuizView(logger, nil, env.quizzes, env.newGameService(), runningGameLookup{}, mediaLister{})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1", nil)
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
	})
}

func TestHandleQuizSetMode(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	t.Run("flips solo to live", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))

		handler := HandleQuizSetMode(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes/1/mode/live", nil)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("mode", quiz.ModeLive)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if got, want := rr.Header().Get("Location"), "/admin/quizzes/"+strconv.FormatInt(qz.ID, 10); got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}

		updated, err := env.quizzes.GetQuiz(t.Context(), qz.ID)
		if err != nil {
			t.Fatalf("GetQuiz err = %v, want nil", err)
		}
		if got, want := updated.Mode, quiz.ModeLive; got != want {
			t.Errorf("Mode = %q, want %q", got, want)
		}
	})

	t.Run("rejects an invalid mode without persisting it", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))

		handler := HandleQuizSetMode(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes/1/mode/sideways", nil)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("mode", "sideways")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		updated, err := env.quizzes.GetQuiz(t.Context(), qz.ID)
		if err != nil {
			t.Fatalf("GetQuiz err = %v, want nil", err)
		}
		if got, want := updated.Mode, quiz.ModeSolo; got != want {
			t.Errorf("Mode = %q, want %q (unchanged)", got, want)
		}
	})

	t.Run("missing quiz renders 404", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)

		handler := HandleQuizSetMode(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes/999/mode/live", nil)
		req.SetPathValue("quizID", "999")
		req.SetPathValue("mode", quiz.ModeLive)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusNotFound; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
	})
}

func TestHandleQuizEdit(t *testing.T) {
	t.Parallel()

	t.Run("quiz found", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))

		handler := HandleQuizEdit(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1/edit", nil)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
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

		env := newAdminEnv(t)

		handler := HandleQuizEdit(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/999/edit", nil)
		req.SetPathValue("quizID", "999")
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

		env := newAdminEnv(t)

		handler := HandleQuizEdit(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/abc/edit", nil)
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

		env := newAdminEnv(t)
		env.seedQuiz(t, ownedQuiz("Q", "q"))
		env.closeStore(t)

		handler := HandleQuizEdit(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1/edit", nil)
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

		env := newAdminEnv(t)

		handler := HandleQuizSave(logger, nil, env.quizzes)

		form := url.Values{
			"title":       {"Quiz One"},
			"description": {"First"},
		}
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/admin/quizzes",
			strings.NewReader(form.Encode()),
		)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		log := buf.String()
		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, log)
		}
		// The new quiz is the only row, so it has id 1; the handler
		// redirects to its view.
		quizzes, err := env.quizzes.ListQuizzes(t.Context())
		if err != nil {
			t.Fatalf("ListQuizzes err = %v, want nil", err)
		}
		if got, want := len(quizzes), 1; got != want {
			t.Fatalf("got %v quizzes, want %v", got, want)
		}
		stored := quizzes[0]
		if got, want := rr.Header().Get("Location"), fmt.Sprintf("/admin/quizzes/%d", stored.ID); got != want {
			t.Fatalf("got Location header %q, want %q, log:\n%v", got, want, log)
		}
		if got, want := stored.Title, "Quiz One"; got != want {
			t.Errorf("stored title = %q, want %q", got, want)
		}
		if got, want := stored.Description, "First"; got != want {
			t.Errorf("stored description = %q, want %q", got, want)
		}
		// #99 / #103: a blank time_limit_seconds defaults to the project
		// default and a missing visibility defaults to public.
		if got, want := stored.TimeLimitSeconds, quiz.DefaultTimeLimitSeconds; got != want {
			t.Errorf("stored time limit = %d, want %d", got, want)
		}
		if got, want := stored.Visibility, quiz.VisibilityPublic; got != want {
			t.Errorf("stored visibility = %q, want %q", got, want)
		}
		if got, want := stored.CreatedByPlayerID, testAdminID; got != want {
			t.Errorf("stored creator = %d, want %d", got, want)
		}
	})

	t.Run("existing quiz", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)
		original := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))

		form := url.Values{
			"title":       {"Quiz One Updated"},
			"description": {"First Updated"},
		}

		handler := HandleQuizSave(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d", original.ID),
			strings.NewReader(form.Encode()),
		)
		req.SetPathValue("quizID", strconv.FormatInt(original.ID, 10))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Header().Get("Location"), fmt.Sprintf("/admin/quizzes/%d", original.ID); got != want {
			t.Fatalf("got Location header %q, want %q", got, want)
		}
		stored, err := env.quizzes.GetQuiz(t.Context(), original.ID)
		if err != nil {
			t.Fatalf("GetQuiz err = %v, want nil", err)
		}
		if got, want := stored.Title, "Quiz One Updated"; got != want {
			t.Errorf("stored title = %q, want %q", got, want)
		}
		if got, want := stored.Description, "First Updated"; got != want {
			t.Errorf("stored description = %q, want %q", got, want)
		}
	})
}

func TestHandleQuizSave_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("parsing id fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)

		handler := HandleQuizSave(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/admin/quizzes/not-an-int/edit", nil)
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

		env := newAdminEnv(t)
		env.seedQuiz(t, ownedQuiz("Q", "q"))
		env.closeStore(t)

		handler := HandleQuizSave(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1/edit", nil)
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
	})

	t.Run("parsing form fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)

		handler := HandleQuizSave(logger, nil, env.quizzes)
		body := errReader{err: errors.New("simulated read error")}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes", body)
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

		env := newAdminEnv(t)

		handler := HandleQuizSave(logger, nil, env.quizzes)

		form := url.Values{}

		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/admin/quizzes",
			strings.NewReader(form.Encode()),
		)
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

		env := newAdminEnv(t)
		env.closeStore(t)

		form := url.Values{
			"title":       {"Quiz One"},
			"slug":        {"quiz-one"},
			"description": {"First"},
		}

		handler := HandleQuizSave(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/admin/quizzes",
			strings.NewReader(form.Encode()),
		)
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
		if got, want := log, "create quiz:"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})

	t.Run("slug conflict re-renders the form at 409", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)
		// Seed a quiz whose title derives slug "quiz-one"; creating a new
		// quiz with the same title derives the same slug, so the UNIQUE
		// slug index trips and maps to quiz.ErrSlugTaken.
		env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))

		form := url.Values{
			"title":       {"Quiz One"},
			"description": {"Duplicate"},
		}
		handler := HandleQuizSave(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/admin/quizzes",
			strings.NewReader(form.Encode()),
		)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusConflict; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Body.String(), "A quiz with this title already exists"; !strings.Contains(got, want) {
			t.Errorf("body should contain %q; body=%q", want, got)
		}
	})

	t.Run("storing existing quiz fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)
		original := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))
		env.closeStore(t)

		form := url.Values{
			"title":       {"Quiz One Updated"},
			"slug":        {"quiz-one-updated"},
			"description": {"First Updated"},
		}

		handler := HandleQuizSave(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d", original.ID),
			strings.NewReader(form.Encode()),
		)
		req.SetPathValue("quizID", strconv.FormatInt(original.ID, 10))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}

		log := buf.String()
		// GetQuiz runs before UpdateQuiz; against a closed DB the load
		// fails first, so the handler reports the storing-quiz path.
		if got, want := log, "error"; !strings.Contains(got, want) {
			t.Fatalf("got: %q, should contain: %q", got, want)
		}
	})
}

func TestHandleQuestionCreate(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	env := newAdminEnv(t)
	qz := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))
	roundID := env.defaultRoundID(t, qz.ID)

	handler := HandleQuestionCreate(logger, nil, env.quizzes, env.media)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		fmt.Sprintf("/admin/quizzes/%d/questions/new?round_id=%d", qz.ID, roundID),
		nil,
	)
	req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
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

		env := newAdminEnv(t)

		handler := HandleQuestionCreate(logger, nil, env.quizzes, env.media)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/admin/quizzes/not-an-int/edit", nil)
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

		env := newAdminEnv(t)
		env.seedQuiz(t, ownedQuiz("Q", "q"))
		env.closeStore(t)

		handler := HandleQuestionCreate(logger, nil, env.quizzes, env.media)

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1/questions/new", nil)
		req.SetPathValue("quizID", "1")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
	})
}

func TestHandleQuestionEdit(t *testing.T) {
	t.Parallel()

	t.Run("new question", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))

		handler := HandleQuestionEdit(logger, nil, env.quizzes, env.media)

		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%d/questions/new", qz.ID),
			nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
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

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz One", "quiz-one"))
		question := qz.Questions[0]

		handler := HandleQuestionEdit(logger, nil, env.quizzes, env.media)

		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%d/questions/%d/edit", qz.ID, question.ID),
			nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("questionID", strconv.FormatInt(question.ID, 10))
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

		env := newAdminEnv(t)

		handler := HandleQuestionEdit(logger, nil, env.quizzes, env.media)

		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			"/admin/quizzes/999/questions/5678/edit",
			nil,
		)
		req.SetPathValue("quizID", "999")
		req.SetPathValue("questionID", "5678")
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

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))

		handler := HandleQuestionEdit(logger, nil, env.quizzes, env.media)

		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%d/questions/5678/edit", qz.ID),
			nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("questionID", "5678")
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
// loop: render the edit form for a question with options, scrape the
// resulting <input> name/value pairs the way a browser would, POST them to
// the save handler, and assert that every option's ID survives unchanged.
//
// This catches the class of bug where a form field name in the template
// drifts away from what the handler reads - e.g. "option[2]id" without the
// dot - because the round-trip silently drops those values and the save
// handler then sees an option as an unsaved (ID=0) option.
func TestQuestionEditSave_OptionIDsRoundTrip(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	env := newAdminEnv(t)
	qz := ownedQuiz("Q1", "q-1")
	qz.Questions = []*quiz.Question{
		{
			Text: "Q?", Position: 1,
			Options: []*quiz.Option{
				{Text: "A", Correct: true},
				{Text: "B"},
				{Text: "C"},
				{Text: "D"},
			},
		},
	}
	env.seedQuiz(t, qz)
	original := qz.Questions[0]
	wantIDs := make([]int64, len(original.Options))
	wantTexts := make([]string, len(original.Options))
	wantCorrect := make([]bool, len(original.Options))
	for i, o := range original.Options {
		wantIDs[i], wantTexts[i], wantCorrect[i] = o.ID, o.Text, o.Correct
	}

	// Step 1: render the edit form.
	editReq := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		fmt.Sprintf("/admin/quizzes/%d/questions/%d/edit", qz.ID, original.ID),
		nil,
	)
	editReq.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
	editReq.SetPathValue("questionID", strconv.FormatInt(original.ID, 10))
	editRec := httptest.NewRecorder()
	HandleQuestionEdit(logger, nil, env.quizzes, env.media).ServeHTTP(editRec, withTestAdmin(editReq))

	if got, want := editRec.Code, http.StatusOK; got != want {
		t.Fatalf("edit form status = %d, want %d", got, want)
	}

	// Step 2: scrape the rendered form's name=value pairs the way a browser
	// would on submit, then POST them to the save handler.
	formBody := scrapeFormFields(t, editRec.Body.String()).Encode()
	saveReq := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		fmt.Sprintf("/admin/quizzes/%d/questions/%d", qz.ID, original.ID),
		strings.NewReader(formBody),
	)
	saveReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	saveReq.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
	saveReq.SetPathValue("questionID", strconv.FormatInt(original.ID, 10))
	saveRec := httptest.NewRecorder()
	HandleQuestionSave(logger, nil, env.quizzes, env.media).ServeHTTP(saveRec, withTestAdmin(saveReq))

	if got, want := saveRec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("save status = %d, want %d (body=%q)", got, want, saveRec.Body.String())
	}

	// Step 3: reload the question and assert every option field
	// round-tripped. A typo on any option[N].<field> name would drop that
	// field's value during the POST, so the reloaded value would not match.
	saved, err := env.quizzes.GetQuestion(t.Context(), original.ID)
	if err != nil {
		t.Fatalf("GetQuestion err = %v, want nil", err)
	}
	if got, want := len(saved.Options), len(wantIDs); got != want {
		t.Fatalf("saved %d options, want %d", got, want)
	}
	for i, opt := range saved.Options {
		if got, want := opt.ID, wantIDs[i]; got != want {
			t.Errorf("option %d ID = %d, want %d (round-trip dropped the ID)", i, got, want)
		}
		if got, want := opt.Text, wantTexts[i]; got != want {
			t.Errorf("option %d Text = %q, want %q (round-trip dropped the text)", i, got, want)
		}
		if got, want := opt.Correct, wantCorrect[i]; got != want {
			t.Errorf("option %d Correct = %v, want %v (round-trip dropped the checkbox)", i, got, want)
		}
	}
}

func TestHandleQuestionEdit_HandleError(t *testing.T) {
	t.Parallel()

	t.Run("parsing quizID fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)

		handler := HandleQuestionEdit(logger, nil, env.quizzes, env.media)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			"/admin/quizzes/not-an-int/questions/5678/edit",
			nil,
		)
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

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))

		handler := HandleQuestionEdit(logger, nil, env.quizzes, env.media)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%d/questions/not-an-int/edit", qz.ID),
			nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("questionID", "not-an-int")
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

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz One", "quiz-one"))
		question := qz.Questions[0]
		env.closeStore(t)

		handler := HandleQuestionEdit(logger, nil, env.quizzes, env.media)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%d/questions/%d/edit", qz.ID, question.ID),
			nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("questionID", strconv.FormatInt(question.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
	})
}

func TestHandleQuestionSave(t *testing.T) {
	t.Parallel()

	t.Run("new question", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)
		// An existing question occupies position 1, so the handler
		// auto-assigns the new question the next position (#16).
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz One", "quiz-one"))
		roundID := env.defaultRoundID(t, qz.ID)
		mediaID := env.seedMedia(t, qz.ID)

		handler := HandleQuestionSave(logger, nil, env.quizzes, env.media)

		form := url.Values{
			"text":           {"Question Four"},
			"image_media_id": {strconv.FormatInt(mediaID, 10)},
			"round_id":       {strconv.FormatInt(roundID, 10)},
		}
		options := []struct {
			text    string
			correct bool
		}{
			{"Option 1", false},
			{"Option 2", true},
			{"Option 3", false},
		}
		for i, option := range options {
			form.Add(fmt.Sprintf("option[%d].text", i), option.text)
			if option.correct {
				form.Add(fmt.Sprintf("option[%d].correct", i), "on")
			}
		}

		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions", qz.ID),
			strings.NewReader(form.Encode()),
		)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		log := buf.String()
		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, log)
		}
		if got, want := rr.Header().Get("Location"), fmt.Sprintf("/admin/quizzes/%d", qz.ID); got != want {
			t.Fatalf("got Location header %q, want %q", got, want)
		}
		// Reload the quiz: the new question lands after the two seeded
		// ones, at position 3, with its options persisted.
		stored, err := env.quizzes.GetQuiz(t.Context(), qz.ID)
		if err != nil {
			t.Fatalf("GetQuiz err = %v, want nil", err)
		}
		if got, want := len(stored.Questions), 3; got != want {
			t.Fatalf("got %d questions, want %d", got, want)
		}
		created := stored.Questions[2]
		if got, want := created.Text, "Question Four"; got != want {
			t.Errorf("created question text = %q, want %q", got, want)
		}
		if created.ImageMediaID == nil {
			t.Errorf("created question ImageMediaID = nil, want %d", mediaID)
		} else if got, want := *created.ImageMediaID, mediaID; got != want {
			t.Errorf("created question ImageMediaID = %d, want %d", got, want)
		}
		if got, want := created.RoundID, roundID; got != want {
			t.Errorf("created question RoundID = %d, want %d", got, want)
		}
		if created.Position <= stored.Questions[1].Position {
			t.Errorf("created position = %d, want greater than %d", created.Position, stored.Questions[1].Position)
		}
		if got, want := len(created.Options), 3; got != want {
			t.Fatalf("got %d options, want %d", got, want)
		}
		var correctCount int
		for _, o := range created.Options {
			if o.ID == 0 {
				t.Error("option ID should not be zero")
			}
			if o.Correct {
				correctCount++
			}
		}
		if got, want := correctCount, 1; got != want {
			t.Errorf("correct option count = %d, want %d", got, want)
		}
	})

	t.Run("existing question", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz One", "quiz-one"))
		question := qz.Questions[0]
		mediaID := env.seedMedia(t, qz.ID)

		handler := HandleQuestionSave(logger, nil, env.quizzes, env.media)

		// Update the text and attach an image, keep the two existing options
		// (by id) with their text changed, and append a brand-new option.
		form := url.Values{
			"text":           {question.Text + " Updated"},
			"image_media_id": {strconv.FormatInt(mediaID, 10)},
		}
		form.Add("option[0].id", strconv.FormatInt(question.Options[0].ID, 10))
		form.Add("option[0].text", question.Options[0].Text+" Updated")
		form.Add("option[0].correct", "on")
		form.Add("option[1].id", strconv.FormatInt(question.Options[1].ID, 10))
		form.Add("option[1].text", question.Options[1].Text+" Updated")
		form.Add("option[2].text", "Option Added")

		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions/%d", qz.ID, question.ID),
			strings.NewReader(form.Encode()),
		)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("questionID", strconv.FormatInt(question.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		log := buf.String()
		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, log)
		}
		if got, want := rr.Header().Get("Location"), fmt.Sprintf("/admin/quizzes/%d", qz.ID); got != want {
			t.Fatalf("got Location header %q, want %q", got, want)
		}
		stored, err := env.quizzes.GetQuestion(t.Context(), question.ID)
		if err != nil {
			t.Fatalf("GetQuestion err = %v, want nil", err)
		}
		if got, want := stored.Text, question.Text+" Updated"; got != want {
			t.Errorf("stored text = %q, want %q", got, want)
		}
		if stored.ImageMediaID == nil {
			t.Errorf("stored ImageMediaID = nil, want %d", mediaID)
		} else if got, want := *stored.ImageMediaID, mediaID; got != want {
			t.Errorf("stored ImageMediaID = %d, want %d", got, want)
		}
		// Position is no longer driven by the form (#16); it stays put.
		if got, want := stored.Position, question.Position; got != want {
			t.Errorf("stored position = %d, want %d", got, want)
		}
		if got, want := len(stored.Options), 3; got != want {
			t.Fatalf("got %d options, want %d", got, want)
		}
	})
}

// TestHandleQuestionSave_Media covers the image-picker save paths (#937):
// a same-quiz image attaches, a foreign image is rejected with a field error,
// and an empty image_media_id detaches (NULL).
func TestHandleQuestionSave_Media(t *testing.T) {
	t.Parallel()

	t.Run("foreign media id is rejected", func(t *testing.T) {
		t.Parallel()

		logger := slog.New(slog.DiscardHandler)
		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz One", "quiz-one"))
		// This quiz has its own library image (so the picker renders), but
		// the submitted id is one uploaded to a DIFFERENT quiz, which must
		// not be attachable here.
		env.seedMedia(t, qz.ID)
		other := env.seedQuiz(t, ownedQuiz("Other Quiz", "other-quiz"))
		foreignMediaID := env.seedMedia(t, other.ID)
		question := qz.Questions[0]

		handler := HandleQuestionSave(logger, nil, env.quizzes, env.media)

		form := url.Values{
			"text":           {question.Text},
			"image_media_id": {strconv.FormatInt(foreignMediaID, 10)},
		}
		form.Add("option[0].id", strconv.FormatInt(question.Options[0].ID, 10))
		form.Add("option[0].text", question.Options[0].Text)
		form.Add("option[0].correct", "on")
		form.Add("option[1].id", strconv.FormatInt(question.Options[1].ID, 10))
		form.Add("option[1].text", question.Options[1].Text)

		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions/%d", qz.ID, question.ID),
			strings.NewReader(form.Encode()),
		)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("questionID", strconv.FormatInt(question.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
		if got, want := rr.Body.String(), "not in this quiz&#39;s library"; !strings.Contains(got, want) {
			t.Errorf("body should contain the field error %q, got %q", want, got)
		}
		// The cross-quiz reference must NOT have been persisted.
		stored, err := env.quizzes.GetQuestion(t.Context(), question.ID)
		if err != nil {
			t.Fatalf("GetQuestion err = %v, want nil", err)
		}
		if stored.ImageMediaID != nil {
			t.Errorf("stored ImageMediaID = %d, want nil (foreign media rejected)", *stored.ImageMediaID)
		}
	})

	t.Run("empty media id detaches the image", func(t *testing.T) {
		t.Parallel()

		logger := slog.New(slog.DiscardHandler)
		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz One", "quiz-one"))
		mediaID := env.seedMedia(t, qz.ID)
		question := qz.Questions[0]

		// Pre-attach an image so the detach is observable.
		question.ImageMediaID = &mediaID
		if err := env.quizzes.UpdateQuestion(t.Context(), question); err != nil {
			t.Fatalf("seed attach err = %v, want nil", err)
		}

		handler := HandleQuestionSave(logger, nil, env.quizzes, env.media)

		form := url.Values{
			"text":           {question.Text},
			"image_media_id": {""},
		}
		form.Add("option[0].id", strconv.FormatInt(question.Options[0].ID, 10))
		form.Add("option[0].text", question.Options[0].Text)
		form.Add("option[0].correct", "on")
		form.Add("option[1].id", strconv.FormatInt(question.Options[1].ID, 10))
		form.Add("option[1].text", question.Options[1].Text)

		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions/%d", qz.ID, question.ID),
			strings.NewReader(form.Encode()),
		)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("questionID", strconv.FormatInt(question.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
		stored, err := env.quizzes.GetQuestion(t.Context(), question.ID)
		if err != nil {
			t.Fatalf("GetQuestion err = %v, want nil", err)
		}
		if stored.ImageMediaID != nil {
			t.Errorf("stored ImageMediaID = %d, want nil after detach", *stored.ImageMediaID)
		}
	})
}

// TestHandleQuestionEdit_Picker covers the image-picker rendering (#937): the
// library thumbnails render when the quiz has images, the attached image is
// pre-checked, and the empty-state hint shows when the quiz has none.
func TestHandleQuestionEdit_Picker(t *testing.T) {
	t.Parallel()

	t.Run("renders the picker with the quiz library and pre-checks the attached image", func(t *testing.T) {
		t.Parallel()

		logger := slog.New(slog.DiscardHandler)
		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz One", "quiz-one"))
		mediaID := env.seedMedia(t, qz.ID)
		question := qz.Questions[0]
		question.ImageMediaID = &mediaID
		if err := env.quizzes.UpdateQuestion(t.Context(), question); err != nil {
			t.Fatalf("seed attach err = %v, want nil", err)
		}

		handler := HandleQuestionEdit(logger, nil, env.quizzes, env.media)

		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%d/questions/%d/edit", qz.ID, question.ID), nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("questionID", strconv.FormatInt(question.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
		body := rr.Body.String()
		if want := fmt.Sprintf("/media/%d/thumb", mediaID); !strings.Contains(body, want) {
			t.Errorf("body should render the library thumbnail %q", want)
		}
		// The attached image's radio must be pre-checked.
		if want := fmt.Sprintf(`value="%d" class="sr-only peer"`, mediaID); !strings.Contains(body, want) {
			t.Errorf("body should contain the picker radio for media %d", mediaID)
		}
		if !strings.Contains(body, "checked") {
			t.Error("body should pre-check the attached image's radio")
		}
	})

	t.Run("shows the upload-first hint when the quiz has no images", func(t *testing.T) {
		t.Parallel()

		logger := slog.New(slog.DiscardHandler)
		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz One", "quiz-one"))
		question := qz.Questions[0]

		handler := HandleQuestionEdit(logger, nil, env.quizzes, env.media)

		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%d/questions/%d/edit", qz.ID, question.ID), nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("questionID", strconv.FormatInt(question.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
		body := rr.Body.String()
		if want := "Upload images on the quiz page first"; !strings.Contains(body, want) {
			t.Errorf("body should contain the empty-state hint %q", want)
		}
		if strings.Contains(body, "/thumb") {
			t.Error("body should not render any thumbnails when the library is empty")
		}
	})
}

// seedRoundID creates an extra round on the quiz and returns its id, so a
// round-scoped create test can target a round that is NOT the quiz
// default (which seeded questions land in) and prove the new question
// follows the chosen round (#929).
func seedRoundID(t *testing.T, env *adminEnv, quizID int64, position int, title string) int64 {
	t.Helper()

	rnd := &quiz.Round{QuizID: quizID, Position: position, Title: title}
	if err := env.quizzes.CreateRound(t.Context(), rnd); err != nil {
		t.Fatalf("CreateRound err = %v, want nil", err)
	}

	return rnd.ID
}

// TestHandleQuestionCreate_Round pins the round-scoped create form (#929):
// the round_id query parameter must name a round of this quiz, and the
// rendered form carries it forward as a hidden field.
func TestHandleQuestionCreate_Round(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	t.Run("renders hidden round_id for a valid round", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))
		roundID := seedRoundID(t, env, qz.ID, 1, "Second Round")

		handler := HandleQuestionCreate(logger, nil, env.quizzes, env.media)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%d/questions/new?round_id=%d", qz.ID, roundID),
			nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
		want := fmt.Sprintf(`name="round_id" value="%d"`, roundID)
		if got := rr.Body.String(); !strings.Contains(got, want) {
			t.Errorf("body missing hidden field %q", want)
		}
	})

	t.Run("missing round_id is a 400", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))

		handler := HandleQuestionCreate(logger, nil, env.quizzes, env.media)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%d/questions/new", qz.ID),
			nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
	})

	t.Run("round from another quiz is a 404", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))
		other := env.seedQuiz(t, ownedQuiz("Quiz Two", "quiz-two"))
		foreignRound := env.defaultRoundID(t, other.ID)

		handler := HandleQuestionCreate(logger, nil, env.quizzes, env.media)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			fmt.Sprintf("/admin/quizzes/%d/questions/new?round_id=%d", qz.ID, foreignRound),
			nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusNotFound; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
	})
}

// TestHandleQuestionSave_Round pins the round-scoped create save (#929): a
// new question lands in the round named by the hidden round_id, not the
// quiz default; a missing or foreign round id is rejected before any
// question is written.
func TestHandleQuestionSave_Round(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	newQuestionForm := func(roundID string) url.Values {
		form := url.Values{
			"text": {"Round-scoped question"},
		}
		if roundID != "" {
			form.Set("round_id", roundID)
		}
		form.Add("option[0].text", "Option A")
		form.Add("option[0].correct", "on")
		form.Add("option[1].text", "Option B")

		return form
	}

	postCreate := func(t *testing.T, env *adminEnv, quizID int64, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		handler := HandleQuestionSave(logger, nil, env.quizzes, env.media)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions", quizID),
			strings.NewReader(form.Encode()),
		)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("quizID", strconv.FormatInt(quizID, 10))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		return rr
	}

	t.Run("lands in the specified non-default round", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		// twoQuestionQuiz seeds two questions in the default round; the new
		// question targets a second round, so a default-round fallback
		// would put it in the wrong place.
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz One", "quiz-one"))
		defaultRound := env.defaultRoundID(t, qz.ID)
		targetRound := seedRoundID(t, env, qz.ID, 1, "Second Round")

		rr := postCreate(t, env, qz.ID, newQuestionForm(strconv.FormatInt(targetRound, 10)))

		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}

		stored, err := env.quizzes.GetQuiz(t.Context(), qz.ID)
		if err != nil {
			t.Fatalf("GetQuiz err = %v, want nil", err)
		}
		var created *quiz.Question
		for _, q := range stored.Questions {
			if q.Text == "Round-scoped question" {
				created = q

				break
			}
		}
		if created == nil {
			t.Fatalf("created question not found among %d questions", len(stored.Questions))
		}
		if got, want := created.RoundID, targetRound; got != want {
			t.Errorf("created question RoundID = %d, want %d (default round %d)", got, want, defaultRound)
		}
		// It is the only question in the target round, so it takes the
		// first slot there.
		inTarget := 0
		for _, q := range stored.Questions {
			if q.RoundID == targetRound {
				inTarget++
			}
		}
		if got, want := inTarget, 1; got != want {
			t.Errorf("questions in target round = %d, want %d", got, want)
		}
	})

	t.Run("missing round_id is a 400 and writes nothing", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))

		rr := postCreate(t, env, qz.ID, newQuestionForm(""))

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
		stored, err := env.quizzes.GetQuiz(t.Context(), qz.ID)
		if err != nil {
			t.Fatalf("GetQuiz err = %v, want nil", err)
		}
		if got, want := len(stored.Questions), 0; got != want {
			t.Errorf("question count = %d, want %d", got, want)
		}
	})

	t.Run("foreign round_id is a 404 and writes nothing", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))
		other := env.seedQuiz(t, ownedQuiz("Quiz Two", "quiz-two"))
		foreignRound := env.defaultRoundID(t, other.ID)

		rr := postCreate(t, env, qz.ID, newQuestionForm(strconv.FormatInt(foreignRound, 10)))

		if got, want := rr.Code, http.StatusNotFound; got != want {
			t.Fatalf("got status code %v, want %v", got, want)
		}
		stored, err := env.quizzes.GetQuiz(t.Context(), qz.ID)
		if err != nil {
			t.Fatalf("GetQuiz err = %v, want nil", err)
		}
		if got, want := len(stored.Questions), 0; got != want {
			t.Errorf("question count on target quiz = %d, want %d", got, want)
		}
	})
}

func TestHandleQuestionSave_HandleError(t *testing.T) {
	t.Parallel()

	t.Run("parsing quizID fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)

		handler := HandleQuestionSave(logger, nil, env.quizzes, env.media)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/admin/quizzes/not-an-int/questions",
			strings.NewReader("text=Question One"),
		)
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

	t.Run("parsing questionID fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))

		handler := HandleQuestionSave(logger, nil, env.quizzes, env.media)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions/not-an-int", qz.ID),
			strings.NewReader("text=Question One"),
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("questionID", "not-an-int")
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

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))

		handler := HandleQuestionSave(logger, nil, env.quizzes, env.media)
		body := errReader{err: errors.New("simulated read error")}
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions", qz.ID),
			body,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
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

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))
		roundID := env.defaultRoundID(t, qz.ID)

		handler := HandleQuestionSave(logger, nil, env.quizzes, env.media)

		form := url.Values{
			"text":           {""},
			"position":       {"10"},
			"round_id":       {strconv.FormatInt(roundID, 10)},
			"option[0].id":   {"not-an-int"},
			"option[0].text": {"Option 1"},
		}

		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions", qz.ID),
			strings.NewReader(form.Encode()),
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
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

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))
		roundID := env.defaultRoundID(t, qz.ID)

		handler := HandleQuestionSave(logger, nil, env.quizzes, env.media)

		form := url.Values{
			"text":     {""},
			"position": {"10"},
			"round_id": {strconv.FormatInt(roundID, 10)},
		}

		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions", qz.ID),
			strings.NewReader(form.Encode()),
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
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

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))
		roundID := env.defaultRoundID(t, qz.ID)
		env.closeStore(t)

		form := url.Values{
			"text":     {"Question One"},
			"round_id": {strconv.FormatInt(roundID, 10)},
		}
		form.Add("option[0].text", "Option 1")
		form.Add("option[1].text", "Option 2")
		form.Add("option[1].correct", "on")
		form.Add("option[2].text", "Option 3")

		handler := HandleQuestionSave(logger, nil, env.quizzes, env.media)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions", qz.ID),
			strings.NewReader(form.Encode()),
		)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
	})

	t.Run("storing existing question fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Quiz One", "quiz-one"))
		question := qz.Questions[0]
		env.closeStore(t)

		form := url.Values{
			"id":       {strconv.FormatInt(question.ID, 10)},
			"text":     {question.Text},
			"position": {strconv.Itoa(question.Position)},
		}
		form.Add("option[0].text", "Option 1")
		form.Add("option[1].text", "Option 2")
		form.Add("option[1].correct", "on")

		handler := HandleQuestionSave(logger, nil, env.quizzes, env.media)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions/%d", qz.ID, question.ID),
			strings.NewReader(form.Encode()),
		)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("questionID", strconv.FormatInt(question.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
	})

	t.Run("quiz not found", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)

		handler := HandleQuestionSave(logger, nil, env.quizzes, env.media)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes/999/questions", nil)
		req.SetPathValue("quizID", "999")
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

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Quiz One", "quiz-one"))

		handler := HandleQuestionSave(logger, nil, env.quizzes, env.media)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions/999", qz.ID),
			nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("questionID", "999")
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

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Q", "q"))

		handler := HandleQuizDelete(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/delete", qz.ID), nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Header().Get("Location"), "/admin/quizzes"; got != want {
			t.Fatalf("got Location header %q, want %q", got, want)
		}
		if _, err := env.quizzes.GetQuiz(t.Context(), qz.ID); err == nil {
			t.Fatal("GetQuiz err = nil after delete, want ErrQuizNotFound")
		}
	})
}

func TestHandleQuizDelete_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("parsing id fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)

		handler := HandleQuizDelete(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost, "/admin/quizzes/not-an-int/delete", nil,
		)
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

		env := newAdminEnv(t)

		// requireQuizOwner runs first now (#281); a missing quiz
		// surfaces from GetQuiz, not from the DeleteQuiz return path.
		handler := HandleQuizDelete(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost, "/admin/quizzes/999/delete", nil,
		)
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

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Q", "q"))
		env.closeStore(t)

		handler := HandleQuizDelete(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/delete", qz.ID), nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
	})
}

func TestHandleQuestionMove(t *testing.T) {
	t.Parallel()

	t.Run("swap succeeds and redirects to quiz view", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)
		// Two questions so the first can move down past the second.
		qz := env.seedQuiz(t, twoQuestionQuiz("Q", "q"))
		first := qz.Questions[0]

		handler := HandleQuestionMove(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions/%d/move/down", qz.ID, first.ID), nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("questionID", strconv.FormatInt(first.ID, 10))
		req.SetPathValue("direction", "down")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("status = %d, want %d, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Header().Get("Location"), fmt.Sprintf("/admin/quizzes/%d", qz.ID); got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
		// The first question now sits below the second.
		stored, err := env.quizzes.GetQuiz(t.Context(), qz.ID)
		if err != nil {
			t.Fatalf("GetQuiz err = %v, want nil", err)
		}
		if got, want := stored.Questions[len(stored.Questions)-1].ID, first.ID; got != want {
			t.Errorf("last question id = %d, want %d (moved down)", got, want)
		}
	})

	t.Run("boundary error redirects without surfacing the failure", func(t *testing.T) {
		t.Parallel()
		// ErrQuestionAtTop / ErrQuestionAtBottom happen when the button
		// should already have been disabled in the UI. Treat as a
		// silent no-op: redirect back so the page re-renders.

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Q", "q"))
		first := qz.Questions[0]

		handler := HandleQuestionMove(logger, nil, env.quizzes)
		// Moving the first question up is already at the top boundary.
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions/%d/move/up", qz.ID, first.ID), nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("questionID", strconv.FormatInt(first.ID, 10))
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

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Q", "q"))
		first := qz.Questions[0]

		handler := HandleQuestionMove(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions/%d/move/sideways", qz.ID, first.ID), nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("questionID", strconv.FormatInt(first.ID, 10))
		req.SetPathValue("direction", "sideways")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d, log:\n%v", got, want, buf.String())
		}
	})

	t.Run("unknown question renders 404", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Q", "q"))

		handler := HandleQuestionMove(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions/999999/move/down", qz.ID), nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("questionID", "999999")
		req.SetPathValue("direction", "down")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusNotFound; got != want {
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

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Q", "q"))
		question := qz.Questions[0]

		handler := HandleQuestionDelete(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions/%d/delete", qz.ID, question.ID),
			nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("questionID", strconv.FormatInt(question.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
		if got, want := rr.Header().Get("Location"), fmt.Sprintf("/admin/quizzes/%d", qz.ID); got != want {
			t.Fatalf("got Location header %q, want %q", got, want)
		}
		if _, err := env.quizzes.GetQuestion(t.Context(), question.ID); err == nil {
			t.Fatal("GetQuestion err = nil after delete, want ErrQuestionNotFound")
		}
	})
}

func TestHandleQuestionDelete_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("parsing quizID fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)

		handler := HandleQuestionDelete(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/admin/quizzes/not-an-int/questions/5/delete",
			nil,
		)
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

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Q", "q"))

		handler := HandleQuestionDelete(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions/not-an-int/delete", qz.ID),
			nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
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

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Q", "q"))

		// #339: HandleQuestionDelete loads the question first via
		// questionByID to enforce the cross-quiz check, so a missing
		// question surfaces from the load path rather than the delete path.
		handler := HandleQuestionDelete(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions/999/delete", qz.ID),
			nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
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

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Q", "q"))
		question := qz.Questions[0]
		env.closeStore(t)

		handler := HandleQuestionDelete(logger, nil, env.quizzes)
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/questions/%d/delete", qz.ID, question.ID),
			nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("questionID", strconv.FormatInt(question.ID, 10))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
		}
	})
}

func TestHandleQuizView_RendersPlayedBy(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	t.Run("renders a row per player with reset button", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Q1", "q-1"))
		alice := env.seedPlayer(t, "alice")
		bob := env.seedPlayer(t, "bob")
		env.playThrough(t, qz, alice)
		env.playThrough(t, qz, bob)

		handler := HandleQuizView(logger, nil, env.quizzes, env.newGameService(), runningGameLookup{}, mediaLister{})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1", nil)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
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
		if got, want := body, fmt.Sprintf(
			`action="/admin/quizzes/%d/players/%d/reset"`,
			qz.ID,
			alice,
		); !strings.Contains(
			got,
			want,
		) {
			t.Errorf("body should contain reset form for alice, got %q", got)
		}
		if got, want := body, fmt.Sprintf(
			`action="/admin/quizzes/%d/players/%d/reset"`,
			qz.ID,
			bob,
		); !strings.Contains(
			got,
			want,
		) {
			t.Errorf("body should contain reset form for bob, got %q", got)
		}
	})

	t.Run("renders 'No plays yet.' when nobody has played", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Q1", "q-1"))

		handler := HandleQuizView(logger, nil, env.quizzes, env.newGameService(), runningGameLookup{}, mediaLister{})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1", nil)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
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

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Q1", "q-1"))
		player := env.seedPlayer(t, "alice")
		env.playThrough(t, qz, player)

		handler := HandleResetGameForPlayer(logger, nil, env.quizzes, env.newGameService())
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/players/%d/reset", qz.ID, player), nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("playerID", strconv.FormatInt(player, 10))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if got, want := rr.Header().Get("Location"), fmt.Sprintf("/admin/quizzes/%d", qz.ID); got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
		// The player's game is gone, so a fresh quiz view shows no plays.
		if _, err := env.games.GetGameByPlayerAndQuiz(t.Context(), player, qz.ID); err == nil {
			t.Error("GetGameByPlayerAndQuiz err = nil after reset, want ErrGameNotFound")
		}
	})

	t.Run("404 when quiz does not exist", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)

		// requireQuizOwner now runs first (#281); a missing quiz is
		// reported by GetQuiz, not by the gameService.ResetGames path.
		handler := HandleResetGameForPlayer(logger, nil, env.quizzes, env.newGameService())
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost,
			"/admin/quizzes/999/players/7/reset", nil,
		)
		req.SetPathValue("quizID", "999")
		req.SetPathValue("playerID", "7")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("500 when delete fails", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Q1", "q-1"))
		env.closeStore(t)

		handler := HandleResetGameForPlayer(logger, nil, env.quizzes, env.newGameService())
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/players/2/reset", qz.ID), nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("playerID", "2")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("400 when playerID path value is non-numeric", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, ownedQuiz("Legacy", "legacy"))

		// Owner gate runs first now (#281); seed an owned quiz so the
		// 400-on-playerID assertion reflects the real handler path.
		handler := HandleResetGameForPlayer(logger, nil, env.quizzes, env.newGameService())
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost,
			fmt.Sprintf("/admin/quizzes/%d/players/abc/reset", qz.ID), nil,
		)
		req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		req.SetPathValue("playerID", "abc")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, withTestAdmin(req))

		if got, want := rr.Code, http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})
}

// testAdminID is the player id the admin-handler tests pose as for
// owner-gated routes (#281). It matches the admin row seeded by
// migration 20260111110308_add_admin_player.sql, so quiz fixtures
// attributed to this id satisfy both the created_by_player_id foreign
// key and the requireQuizOwner gate. The handler request carries an
// auth.Player with this id and RoleAdmin via withTestAdmin.
const testAdminID int64 = 1

// withTestAdmin returns r with an auth.Player on its context. Owner-gated
// routes (#281) refuse the request when no Player is on context, so tests
// that bypass the auth middleware attach the seeded admin (testAdminID)
// directly; quiz fixtures attribute themselves to that id so the
// requireQuizOwner check passes.
//
// Defined in the untagged file so both the integration-tagged handler
// tests and the untagged render tests below share one helper.
func withTestAdmin(r *http.Request) *http.Request {
	signedIn := &auth.Player{ID: testAdminID, DisplayName: "admin", Role: auth.RoleAdmin}

	return r.WithContext(auth.WithPlayer(r.Context(), signedIn))
}

// errReader is an io.ReadCloser whose Read always fails, used to drive
// the "error parsing form" branch of the save handlers.
type errReader struct{ err error }

func (e errReader) Read(_ []byte) (int, error) { return 0, e.err }
func (errReader) Close() error                 { return nil }

// scrapeFormFields extracts (name, value) pairs from <input> and <textarea>
// elements in body the way a browser would when submitting the surrounding
// form: disabled fields are skipped, and unchecked checkboxes are excluded.
//
// Limitation: every <input type="submit"> is included. A real browser only
// sends the submit button that was actually clicked, so callers must ensure
// the rendered form has at most one submit input - otherwise the resulting
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

	// Textareas - browsers submit their inner text as the field value.
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

func TestHandleIndex(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	handler := HandleIndex(logger, nil, noActiveSessionLookup{})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	if err != nil {
		t.Fatalf("http.NewRequest error: %v", err)
	}
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, withTestAdmin(req))

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Errorf("got status code %v, want %v", got, want)
	}
	// invariant pinned by #517: the landing page surfaces a tile for
	// each of the three top-level admin sections (matching the nav) so a
	// fresh admin can discover them without typing URLs. New/Import quiz
	// moved into the Quizzes page, so they are no longer dashboard tiles.
	// The "Host a session" entry (#836) opens an empty live room.
	body := rr.Body.String()
	for _, want := range []string{
		"Admin Dashboard",
		`href="/admin/quizzes"`,
		`href="/admin/players"`,
		`href="/admin/email"`,
		`action="/host"`,
		"Host a session",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	// The host control is a single adaptive slot (#850): with no active room
	// the dashboard shows the "Host a session" submit and NOT the resume link.
	if got := body; strings.Contains(got, `data-testid="resume-hosting"`) {
		t.Errorf("body shows the resume control with no active room: %q", got)
	}
}

// TestHandleIndex_ResumeLink pins the resume control (#836, #850): when the
// signed-in host has an active room, the dashboard collapses the single host
// slot to a "Resume session" link back to it; the link carries the join code so
// the host can return to a room they browsed away from, and the "Host a session"
// submit is replaced rather than shown alongside it.
func TestHandleIndex_ResumeLink(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	handler := HandleIndex(logger, nil, activeSessionLookup{code: "ABC123"})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	if err != nil {
		t.Fatalf("http.NewRequest error: %v", err)
	}
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, withTestAdmin(req))

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Errorf("got status code %v, want %v", got, want)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Resume session",
		`href="/host/ABC123"`,
		"ABC123",
		`data-testid="resume-hosting"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	// The two controls are mutually exclusive: with an active room the
	// "Host a session" submit is gone.
	if got := body; strings.Contains(got, `data-testid="host-session-submit"`) {
		t.Errorf("body shows the Host a session submit alongside the resume link: %q", got)
	}
}

// noActiveSessionLookup is an ActiveSessionLookup that reports no active room,
// the common case for a host who is not currently hosting.
type noActiveSessionLookup struct{}

//nolint:nilnil // (nil, nil) is the deliberate "no active room" result the dashboard handles.
func (noActiveSessionLookup) GetActiveSessionForHost(_ context.Context, _ int64) (*livesession.Session, error) {
	return nil, nil
}

// activeSessionLookup is an ActiveSessionLookup that reports one active room by
// its join code, so the dashboard renders the resume link.
type activeSessionLookup struct{ code string }

func (a activeSessionLookup) GetActiveSessionForHost(_ context.Context, _ int64) (*livesession.Session, error) {
	return &livesession.Session{JoinCode: a.code}, nil
}

// runningGameLookup is a RunningGameLookup that reports the host's running-game
// state by a fixed bool, so the quiz view test can toggle the confirm-and-restart
// prompt (#853) without standing up a live session.
type runningGameLookup struct{ running bool }

func (l runningGameLookup) HostHasRunningGame(_ context.Context, _ int64) (bool, error) {
	return l.running, nil
}

// mediaLister is a MediaLister stub for the quiz-view unit tests, returning the
// configured library (nil for an empty grid). The rich render of the upload
// control + thumbnail grid is pinned end-to-end in test/integration; these unit
// tests only need a satisfying lister so the handler runs.
type mediaLister struct{ items []*media.Media }

func (l mediaLister) ListMediaByQuiz(_ context.Context, _ int64) ([]*media.Media, error) {
	return l.items, nil
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

func TestMediaCardDataDurationLabel(t *testing.T) {
	t.Parallel()
	ptr := func(n int) *int { return &n }
	cases := []struct {
		name string
		ms   *int
		want string
	}{
		{name: "nil duration", ms: nil, want: ""},
		{name: "zero duration", ms: ptr(0), want: ""},
		{name: "negative duration", ms: ptr(-1), want: ""},
		{name: "under a second rounds down to 0:00", ms: ptr(400), want: "0:00"},
		{name: "exactly one second", ms: ptr(1000), want: "0:01"},
		{name: "pads single-digit seconds", ms: ptr(9000), want: "0:09"},
		{name: "one minute", ms: ptr(60000), want: "1:00"},
		{name: "minutes and seconds", ms: ptr(95000), want: "1:35"},
		{name: "drops sub-second remainder", ms: ptr(95999), want: "1:35"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			card := MediaCardData{DurationMs: tc.ms}
			if got, want := card.DurationLabel(), tc.want; got != want {
				t.Errorf("DurationLabel() = %q, want %q", got, want)
			}
		})
	}
}
