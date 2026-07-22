package admin_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/admin"
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
		admin.HandleQuizEditor(logger, nil, env.quizzes).
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
		admin.HandleQuizEditor(logger, nil, env.quizzes).
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
		admin.HandleQuizView(
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
		admin.HandleQuizEditor(logger, nil, env.quizzes).
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
			admin.HandleQuizEditor(logger, nil, env.quizzes).
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
			if got := admin.SelectedQuestionID(r); got != tc.want {
				t.Errorf("selectedQuestionID(%q) = %d, want %d", tc.query, got, tc.want)
			}
		})
	}
}
