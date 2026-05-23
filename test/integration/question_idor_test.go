//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// TestQuestionIDOR_Integration pins the #339 fix: a question route
// scoped to one quiz must reject loads of questions that belong to a
// different quiz. Pre-fix, an admin who owned quizA could edit, save,
// or delete a question on quizB by mounting it as
// /admin/quizzes/{A-id}/questions/{B-question-id}. The cross-quiz
// check now lives in admin.questionByID and is wired into the read +
// write + delete paths; HandleQuestionMove already cross-checked via
// SwapQuestionPositions.
func TestQuestionIDOR_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_USERNAMES":      "idor-admin-a,idor-admin-b",
	})
	baseURL := srv.BaseURL

	adminA := registerAdminClient(ctx, t, baseURL, "idor-admin-a")
	adminB := registerAdminClient(ctx, t, baseURL, "idor-admin-b")

	db, err := sql.Open("sqlite", srv.DBURI)
	if err != nil {
		t.Fatalf("sql.Open err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v, want nil", cerr)
		}
	})
	stores := store.New(db, slog.Default())

	playerA, err := stores.Players.GetPlayerByUsername(ctx, "idor-admin-a")
	if err != nil {
		t.Fatalf("GetPlayerByUsername(a) err = %v, want nil", err)
	}
	playerB, err := stores.Players.GetPlayerByUsername(ctx, "idor-admin-b")
	if err != nil {
		t.Fatalf("GetPlayerByUsername(b) err = %v, want nil", err)
	}

	quizA := &quiz.Quiz{
		Title:             "IDOR Quiz A (attacker)",
		Slug:              "idor-quiz-a",
		Description:       "owned by admin A",
		CreatedByPlayerID: playerA.ID,
		Questions: []*quiz.Question{
			{
				Text:     "A's filler question",
				Position: 1,
				Options:  []*quiz.Option{{Text: "yes", Correct: true}, {Text: "no"}},
			},
		},
	}
	if cerr := stores.Quizzes.CreateQuiz(ctx, quizA); cerr != nil {
		t.Fatalf("CreateQuiz(A) err = %v, want nil", cerr)
	}

	// ASCII-only so it survives any HTML-entity encoding the admin
	// template might apply (e.g. mdash) without false negatives.
	const victimQuestionText = "B-victim-question-DO-NOT-DELETE"
	quizB := &quiz.Quiz{
		Title:             "IDOR Quiz B (victim)",
		Slug:              "idor-quiz-b",
		Description:       "owned by admin B",
		CreatedByPlayerID: playerB.ID,
		Questions: []*quiz.Question{
			{
				Text:     victimQuestionText,
				Position: 1,
				Options:  []*quiz.Option{{Text: "yes", Correct: true}, {Text: "no"}},
			},
		},
	}
	if cerr := stores.Quizzes.CreateQuiz(ctx, quizB); cerr != nil {
		t.Fatalf("CreateQuiz(B) err = %v, want nil", cerr)
	}
	victimQuestionID := quizB.Questions[0].ID

	// Path that mounts B's question under A's quiz. Every probe below
	// uses this — the gate must reject it.
	crossPath := fmt.Sprintf("/admin/quizzes/%d/questions/%d", quizA.ID, victimQuestionID)

	t.Run("GET edit form for cross-quiz question returns 404", func(t *testing.T) {
		t.Parallel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+crossPath+"/edit", nil)
		if err != nil {
			t.Fatalf("NewRequest err = %v, want nil", err)
		}
		resp, err := adminA.Do(req)
		if err != nil {
			t.Fatalf("Do err = %v, want nil", err)
		}
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("GET edit cross-quiz status = %d, want %d", got, want)
		}
	})

	t.Run("POST save on cross-quiz question returns 404", func(t *testing.T) {
		t.Parallel()
		token := fetchCSRFToken(ctx, t, adminA, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizA.ID))
		form := url.Values{
			"text":              {"Hijacked text"},
			"option[0].text":    {"yes"},
			"option[0].correct": {"on"},
			"option[1].text":    {"no"},
			"csrf_token":        {token},
		}
		req := newFormReq(ctx, t, baseURL+crossPath, form)
		resp, err := adminA.Do(req)
		if err != nil {
			t.Fatalf("Do err = %v, want nil", err)
		}
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("POST save cross-quiz status = %d, want %d", got, want)
		}
	})

	t.Run("POST delete on cross-quiz question returns 404", func(t *testing.T) {
		t.Parallel()
		token := fetchCSRFToken(ctx, t, adminA, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizA.ID))
		form := url.Values{"csrf_token": {token}}
		req := newFormReq(ctx, t, baseURL+crossPath+"/delete", form)
		resp, err := adminA.Do(req)
		if err != nil {
			t.Fatalf("Do err = %v, want nil", err)
		}
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("POST delete cross-quiz status = %d, want %d", got, want)
		}
	})

	// After all parallel probes finish, B's question must still be
	// present and unchanged. Cleanup runs after every t.Parallel
	// subtest completes, so it sees the post-probe DB state.
	// Also confirm B can still see the question via their own quiz view
	// while the server is still alive. This runs BEFORE the subtests
	// (which all defer to t.Parallel) so we touch the live server here
	// and rely on the store-level cleanup check below for the
	// post-probe state.
	verifyAdminBSeesVictimQuestion(ctx, t, adminB, baseURL, quizB.ID, victimQuestionText)

	t.Cleanup(func() {
		// Cleanup runs after the test (and its derived context) has
		// finished; using ctx here would race against the shutdown
		// cancellation. context.Background is fine — the store
		// wrapper is still alive at cleanup time.
		q, err := stores.Quizzes.GetQuestion(context.Background(), victimQuestionID)
		if err != nil {
			t.Errorf("GetQuestion(victim) err = %v — question should still exist", err)

			return
		}
		if got, want := q.Text, victimQuestionText; got != want {
			t.Errorf("victim question text = %q, want %q (cross-quiz probe should not have mutated it)", got, want)
		}
	})
}

// verifyAdminBSeesVictimQuestion fetches adminB's quiz view page and
// asserts the victim question text appears in the rendered HTML. Pulled
// out so the assertion runs against the live server before the parallel
// subtests start tearing things down.
func verifyAdminBSeesVictimQuestion(
	ctx context.Context, t *testing.T, client *http.Client, baseURL string, quizID int64, victimText string,
) {
	t.Helper()
	viewReq, err := http.NewRequestWithContext(
		ctx, http.MethodGet, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID), nil,
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	viewResp, err := client.Do(viewReq)
	if err != nil {
		t.Fatalf("Do err = %v, want nil", err)
	}
	defer closeBody(t, viewResp.Body)
	if got, want := viewResp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("victim quiz view status = %d, want %d", got, want)
	}
	body, err := io.ReadAll(viewResp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if !strings.Contains(string(body), victimText) {
		t.Errorf("victim quiz view body missing %q (adminB should still see their own question)", victimText)
	}
}
