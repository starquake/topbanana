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

	// The live quiz card carries the accent-dot pill, the solo quiz card the
	// default pill. Pin the class and label together so a restyle that drops
	// the play-mode badge (#829) fails here.
	body := rr.Body.String()
	if got, want := body, `class="pill pill-live">Live`; !strings.Contains(got, want) {
		t.Errorf("body = %q, should contain live badge %q", got, want)
	}
	if got, want := body, `class="pill">Solo`; !strings.Contains(got, want) {
		t.Errorf("body = %q, should contain solo badge %q", got, want)
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

		handler := HandleQuizView(logger, nil, env.quizzes, env.newGameService())
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

		handler := HandleQuizView(logger, nil, env.quizzes, env.newGameService())
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

func TestHandleQuizView_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("parsing id fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		env := newAdminEnv(t)

		handler := HandleQuizView(logger, nil, env.quizzes, env.newGameService())
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

		handler := HandleQuizView(logger, nil, env.quizzes, env.newGameService())
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

	handler := HandleQuestionCreate(logger, nil, env.quizzes)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1/questions/new", nil)
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

		handler := HandleQuestionCreate(logger, nil, env.quizzes)
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

		handler := HandleQuestionCreate(logger, nil, env.quizzes)

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

		handler := HandleQuestionEdit(logger, nil, env.quizzes)

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

		handler := HandleQuestionEdit(logger, nil, env.quizzes)

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

		handler := HandleQuestionEdit(logger, nil, env.quizzes)

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

		handler := HandleQuestionEdit(logger, nil, env.quizzes)

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
	HandleQuestionEdit(logger, nil, env.quizzes).ServeHTTP(editRec, withTestAdmin(editReq))

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
	HandleQuestionSave(logger, nil, env.quizzes).ServeHTTP(saveRec, withTestAdmin(saveReq))

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

		handler := HandleQuestionEdit(logger, nil, env.quizzes)
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

		handler := HandleQuestionEdit(logger, nil, env.quizzes)
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

		handler := HandleQuestionEdit(logger, nil, env.quizzes)
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

		handler := HandleQuestionSave(logger, nil, env.quizzes)

		form := url.Values{
			"text":      {"Question Four"},
			"image_url": {"https://example.com/image.png"},
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

		handler := HandleQuestionSave(logger, nil, env.quizzes)

		// Update the text and image, keep the two existing options (by id)
		// with their text changed, and append a brand-new option.
		form := url.Values{
			"text":      {question.Text + " Updated"},
			"image_url": {"https://example.com/updated.png"},
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
		// Position is no longer driven by the form (#16); it stays put.
		if got, want := stored.Position, question.Position; got != want {
			t.Errorf("stored position = %d, want %d", got, want)
		}
		if got, want := len(stored.Options), 3; got != want {
			t.Fatalf("got %d options, want %d", got, want)
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

		handler := HandleQuestionSave(logger, nil, env.quizzes)
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

		handler := HandleQuestionSave(logger, nil, env.quizzes)
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

		handler := HandleQuestionSave(logger, nil, env.quizzes)
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

		handler := HandleQuestionSave(logger, nil, env.quizzes)

		form := url.Values{
			"text":           {""},
			"image_url":      {"http://example.com/image.png"},
			"position":       {"10"},
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

		handler := HandleQuestionSave(logger, nil, env.quizzes)

		form := url.Values{
			"text":      {""},
			"image_url": {"http://example.com/image.png"},
			"position":  {"10"},
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
		env.closeStore(t)

		form := url.Values{
			"text":      {"Question One"},
			"image_url": {"https://example.com/image.png"},
		}
		form.Add("option[0].text", "Option 1")
		form.Add("option[1].text", "Option 2")
		form.Add("option[1].correct", "on")
		form.Add("option[2].text", "Option 3")

		handler := HandleQuestionSave(logger, nil, env.quizzes)
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
			"id":        {strconv.FormatInt(question.ID, 10)},
			"text":      {question.Text},
			"image_url": {"https://example.com/image.png"},
			"position":  {strconv.Itoa(question.Position)},
		}
		form.Add("option[0].text", "Option 1")
		form.Add("option[1].text", "Option 2")
		form.Add("option[1].correct", "on")

		handler := HandleQuestionSave(logger, nil, env.quizzes)
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

		handler := HandleQuestionSave(logger, nil, env.quizzes)
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

		handler := HandleQuestionSave(logger, nil, env.quizzes)
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

		handler := HandleQuizView(logger, nil, env.quizzes, env.newGameService())
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

		handler := HandleQuizView(logger, nil, env.quizzes, env.newGameService())
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
	// With no active room the resume link is absent.
	if got := body; strings.Contains(got, "Resume hosting") {
		t.Errorf("body shows the resume link with no active room: %q", got)
	}
}

// TestHandleIndex_ResumeLink pins the "Resume hosting" link (#836): when the
// signed-in host has an active room, the dashboard links back to it; the link
// carries the join code so the host can return to a room they browsed away from.
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
		"Resume hosting",
		`href="/host/ABC123"`,
		"ABC123",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
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

func TestHumanizeTime(t *testing.T) {
	t.Parallel()

	// A fixed reference time keeps this deterministic: humanizeSince takes
	// "now" rather than reading the clock, so there is no scheduling-jitter
	// window for a paused subtest to cross a bucket boundary (#666).
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{"just now (5s ago)", now.Add(-5 * time.Second), "just now"},
		{"1 minute ago", now.Add(-1 * time.Minute), "1 min ago"},
		{"5 minutes ago", now.Add(-5 * time.Minute), "5 min ago"},
		{"1 hour ago", now.Add(-1 * time.Hour), "1 hr ago"},
		{"3 hours ago", now.Add(-3 * time.Hour), "3 hr ago"},
		{"1 day ago", now.Add(-24 * time.Hour), "1 day ago"},
		{"5 days ago", now.Add(-5 * 24 * time.Hour), "5 days ago"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got, want := HumanizeSince(now, tc.t), tc.want; got != want {
				t.Errorf("HumanizeSince(now, %v) = %q, want %q", tc.t, got, want)
			}
		})
	}
}
