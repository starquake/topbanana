package admin_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/admin"
)

func TestHandleQuizEditor(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	newRequest := func(t *testing.T, quizID int64, query string) *http.Request {
		t.Helper()

		target := "/admin/quizzes/" + strconv.FormatInt(quizID, 10) + "/questions" + query
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
		r.SetPathValue("quizID", strconv.FormatInt(quizID, 10))

		return r
	}

	t.Run("renders the rail and an empty editor pane", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Editor Quiz", "editor-quiz"))

		rr := httptest.NewRecorder()
		HandleQuizEditor(logger, nil, env.quizzes).
			ServeHTTP(rr, withTestAdmin(newRequest(t, qz.ID, "")))

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("editor status = %d, want %d", got, want)
		}

		body := rr.Body.String()
		for _, want := range []string{
			`data-testid="editor-rail"`,
			`id="question-editor"`,
			"What is the capital of France?",
			"Editor Quiz",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("editor body should contain %q", want)
			}
		}
	})

	// The rail is the quiz view's questions_list partial. In the editor its
	// rows select into the pane; on the quiz view they stay plain links.
	t.Run("wires the rail rows for htmx selection", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Wired Quiz", "wired-quiz"))

		rr := httptest.NewRecorder()
		HandleQuizEditor(logger, nil, env.quizzes).
			ServeHTTP(rr, withTestAdmin(newRequest(t, qz.ID, "")))

		body := rr.Body.String()
		for _, want := range []string{
			`hx-target="#question-editor"`,
			"data-editor-row",
			`/questions/`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("editor rail should contain %q", want)
			}
		}

		viewRR := httptest.NewRecorder()
		viewReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/1", nil)
		viewReq.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
		HandleQuizView(
			logger, nil, env.quizzes, env.newGameService(), runningGameLookup{}, mediaLister{}, testUploadLimits(),
		).ServeHTTP(viewRR, withTestAdmin(viewReq))

		if notWant := "data-editor-row"; strings.Contains(viewRR.Body.String(), notWant) {
			t.Errorf("quiz view rows should not carry %q", notWant)
		}
	})

	// The editor is owner-only, via the same guard as the quiz view.
	t.Run("404s a signed-in non-owner", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Guarded Quiz", "guarded-quiz"))

		rr := httptest.NewRecorder()
		HandleQuizEditor(logger, nil, env.quizzes).
			ServeHTTP(rr, withTestViewer(newRequest(t, qz.ID, "")))

		if got, want := rr.Code, http.StatusNotFound; got != want {
			t.Errorf("non-owner editor status = %d, want %d", got, want)
		}
	})

	// A junk deep link opens the editor with nothing selected rather than
	// erroring - the same state as after deleting the selected question.
	t.Run("tolerates an unusable q parameter", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Deep Link Quiz", "deep-link-quiz"))

		for _, query := range []string{"", "?q=", "?q=nonsense", "?q=-1", "?q=0"} {
			rr := httptest.NewRecorder()
			HandleQuizEditor(logger, nil, env.quizzes).
				ServeHTTP(rr, withTestAdmin(newRequest(t, qz.ID, query)))

			if got, want := rr.Code, http.StatusOK; got != want {
				t.Errorf("editor status for %q = %d, want %d", query, got, want)
			}
		}
	})
}

func TestSelectedQuestionID(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		query string
		want  int64
	}{
		"absent":     {query: "", want: 0},
		"empty":      {query: "?q=", want: 0},
		"valid":      {query: "?q=42", want: 42},
		"negative":   {query: "?q=-1", want: 0},
		"zero":       {query: "?q=0", want: 0},
		"non-number": {query: "?q=abc", want: 0},
		"overflow":   {query: "?q=99999999999999999999", want: 0},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(
				t.Context(), http.MethodGet, "/admin/quizzes/1/questions"+tc.query, nil,
			)
			if got := SelectedQuestionID(r); got != tc.want {
				t.Errorf("selectedQuestionID(%q) = %d, want %d", tc.query, got, tc.want)
			}
		})
	}
}

// The editor pane asks for the form alone; a direct visit still gets the full
// page, which is also the no-JS path (#1244 slice 2).
func TestHandleQuestionEditPartial(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	newRequest := func(t *testing.T, quizID, questionID int64, htmxReq bool) *http.Request {
		t.Helper()

		target := "/admin/quizzes/" + strconv.FormatInt(quizID, 10) +
			"/questions/" + strconv.FormatInt(questionID, 10) + "/edit"
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
		r.SetPathValue("quizID", strconv.FormatInt(quizID, 10))
		r.SetPathValue("questionID", strconv.FormatInt(questionID, 10))
		if htmxReq {
			r.Header.Set("Hx-Request", "true")
		}

		return withTestAdmin(r)
	}

	env := newAdminEnv(t)
	qz := env.seedQuiz(t, twoQuestionQuiz("Partial Quiz", "partial-quiz"))
	questionID := qz.Questions[0].ID

	handler := HandleQuestionEdit(logger, nil, env.quizzes, env.media)

	t.Run("HX-Request returns the bare form", func(t *testing.T) {
		t.Parallel()

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, newRequest(t, qz.ID, questionID, true))

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("partial status = %d, want %d", got, want)
		}

		body := rr.Body.String()
		if want := "<form"; !strings.Contains(body, want) {
			t.Errorf("partial should contain the form %q", want)
		}
		// No layout: the page chrome must not land inside the editor pane.
		for _, notWant := range []string{"<!DOCTYPE", "<html", `class="app-bar"`} {
			if strings.Contains(body, notWant) {
				t.Errorf("partial should not contain page chrome %q", notWant)
			}
		}
	})

	t.Run("a direct visit still returns the full page", func(t *testing.T) {
		t.Parallel()

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, newRequest(t, qz.ID, questionID, false))

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("page status = %d, want %d", got, want)
		}
		if want := "<html"; !strings.Contains(rr.Body.String(), want) {
			t.Errorf("direct visit should return the full page containing %q", want)
		}
	})
}

// A save from the editor stays on the page: the form comes back for the pane
// and the rail's row follows out-of-band, so the row's text and flags refresh
// without re-rendering the list (#1244 slice 2). Re-rendering the whole list
// would destroy and rebuild every SortableJS instance mid-session.
func TestHandleQuestionSaveOutOfBand(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	env := newAdminEnv(t)
	qz := env.seedQuiz(t, twoQuestionQuiz("OOB Quiz", "oob-quiz"))
	question := qz.Questions[0]

	form := url.Values{
		"id":                {strconv.FormatInt(question.ID, 10)},
		"text":              {"Edited in the pane"},
		"option[0].text":    {"Yes"},
		"option[0].correct": {"on"},
		"option[1].text":    {"No"},
	}

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/admin/quizzes/"+strconv.FormatInt(qz.ID, 10)+"/questions/"+strconv.FormatInt(question.ID, 10),
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Hx-Request", "true")
	req.SetPathValue("quizID", strconv.FormatInt(qz.ID, 10))
	req.SetPathValue("questionID", strconv.FormatInt(question.ID, 10))

	rr := httptest.NewRecorder()
	HandleQuestionSave(logger, nil, env.quizzes, env.media).ServeHTTP(rr, withTestAdmin(req))

	// 200 with a body, not the 303 the non-htmx path returns.
	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("htmx save status = %d, want %d (a redirect would bounce the pane)", got, want)
	}

	body := rr.Body.String()

	if want := "<form"; !strings.Contains(body, want) {
		t.Errorf("save response should re-render the form for the pane, missing %q", want)
	}
	if want := `hx-swap-oob="true"`; !strings.Contains(body, want) {
		t.Errorf("save response should carry the out-of-band row marker %q", want)
	}
	if want := `id="q-row-` + strconv.FormatInt(question.ID, 10) + `"`; !strings.Contains(body, want) {
		t.Errorf("out-of-band row should target %q", want)
	}
	// The edited text must reach the rail row, not just the form.
	if want := "Edited in the pane"; strings.Count(body, want) < 2 {
		t.Errorf("edited text %q should appear in both the form and the swapped row", want)
	}
	// The whole list must NOT come back: that is what would rebind Sortable.
	if notWant := `id="questions-list"`; strings.Contains(body, notWant) {
		t.Errorf("save response should not re-render the whole list %q", notWant)
	}
}
