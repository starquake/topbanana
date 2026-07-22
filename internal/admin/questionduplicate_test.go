package admin_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/admin"
)

func TestHandleQuestionDuplicate(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	newRequest := func(t *testing.T, quizID, questionID int64, htmxReq bool) *http.Request {
		t.Helper()

		target := "/admin/quizzes/" + strconv.FormatInt(quizID, 10) +
			"/questions/" + strconv.FormatInt(questionID, 10) + "/duplicate"
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, nil)
		r.SetPathValue("quizID", strconv.FormatInt(quizID, 10))
		r.SetPathValue("questionID", strconv.FormatInt(questionID, 10))
		if htmxReq {
			r.Header.Set("Hx-Request", "true")
		}

		return withTestAdmin(r)
	}

	t.Run("copies the question directly after its source", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Dupe Quiz", "dupe-quiz"))
		source := qz.Questions[0]

		rr := httptest.NewRecorder()
		HandleQuestionDuplicate(logger, nil, env.quizzes, env.media).
			ServeHTTP(rr, newRequest(t, qz.ID, source.ID, false))

		if got, want := rr.Code, http.StatusSeeOther; got != want {
			t.Fatalf("duplicate status = %d, want %d", got, want)
		}

		full, err := env.quizzes.GetQuiz(t.Context(), qz.ID)
		if err != nil {
			t.Fatalf("GetQuiz err = %v, want nil", err)
		}
		if got, want := len(full.Questions), len(qz.Questions)+1; got != want {
			t.Fatalf("question count = %d, want %d", got, want)
		}

		// The copy sits immediately behind its source, not at the end.
		var copied *struct {
			Text     string
			Position int
			Options  int
		}
		for _, q := range full.Questions {
			if q.ID != source.ID && q.Text == source.Text {
				copied = &struct {
					Text     string
					Position int
					Options  int
				}{q.Text, q.Position, len(q.Options)}
			}
		}
		if copied == nil {
			t.Fatal("no duplicate found in the quiz")
		}
		if got, want := copied.Position, source.Position+1; got != want {
			t.Errorf("duplicate position = %d, want %d (directly after its source)", got, want)
		}
		if got, want := copied.Options, len(source.Options); got != want {
			t.Errorf("duplicate carried %d options, want %d", got, want)
		}
	})

	// Duplicating is a content edit, so the publish lock applies (#1192).
	t.Run("refuses a published quiz", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Locked Dupe", "locked-dupe"))
		if err := env.quizzes.SetQuizPublished(t.Context(), qz.ID, true); err != nil {
			t.Fatalf("SetQuizPublished err = %v, want nil", err)
		}

		rr := httptest.NewRecorder()
		HandleQuestionDuplicate(logger, nil, env.quizzes, env.media).
			ServeHTTP(rr, newRequest(t, qz.ID, qz.Questions[0].ID, false))

		if got, want := rr.Code, http.StatusConflict; got != want {
			t.Errorf("published duplicate status = %d, want %d", got, want)
		}
	})

	// From the editor the response fills the pane with the copy and refreshes
	// the rail out of band, since a brand new row has nothing to graft onto.
	t.Run("htmx gets the copy's form plus the rail", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		qz := env.seedQuiz(t, twoQuestionQuiz("Dupe Pane", "dupe-pane"))

		rr := httptest.NewRecorder()
		HandleQuestionDuplicate(logger, nil, env.quizzes, env.media).
			ServeHTTP(rr, newRequest(t, qz.ID, qz.Questions[0].ID, true))

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("htmx duplicate status = %d, want %d", got, want)
		}

		body := rr.Body.String()
		for _, want := range []string{"<form", `id="questions-list"`, `hx-swap-oob="true"`} {
			if !strings.Contains(body, want) {
				t.Errorf("htmx duplicate response should contain %q", want)
			}
		}
	})
}
