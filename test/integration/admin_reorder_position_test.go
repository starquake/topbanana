package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// reorderFixture bundles the seeded quiz tree and the owner client for
// the position-reorder integration tests (#199). All ids belong to the
// signed-in owner so the requireGameHost / requireQuizOwner gates pass.
type reorderFixture struct {
	client      *http.Client
	baseURL     string
	quizViewURL string
	quizID      int64
	rounds      map[string]int64
	questions   map[string]int64
}

// seedReorderQuiz boots a server, registers an owning admin, and seeds a
// quiz with two rounds (R1: Q1, Q2; R2: Q3) so the position endpoints
// have a non-trivial layout to rearrange. Each test seeds its own so they
// stay independent and can run in parallel.
func seedReorderQuiz(t *testing.T) (context.Context, reorderFixture) {
	t.Helper()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

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

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "reorder-admin", "reorder-pass-123")

	owner, err := stores.Players.GetPlayerByDisplayName(ctx, "reorder-admin")
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
	}

	qz := &quiz.Quiz{
		Title:             "Reorder Position Quiz",
		Slug:              "reorder-position",
		Description:       "seed for the position reorder integration test",
		CreatedByPlayerID: owner.ID,
	}
	if cerr := stores.Quizzes.CreateQuiz(ctx, qz); cerr != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", cerr)
	}

	r1, err := stores.Quizzes.GetDefaultRound(ctx, qz.ID)
	if err != nil {
		t.Fatalf("GetDefaultRound err = %v, want nil", err)
	}
	r2 := &quiz.Round{QuizID: qz.ID, Position: 1, Title: "Round Two"}
	if cerr := stores.Quizzes.CreateRound(ctx, r2); cerr != nil {
		t.Fatalf("CreateRound err = %v, want nil", cerr)
	}

	questions := seedReorderQuestions(ctx, t, stores, qz.ID, r1.ID, r2.ID)

	return ctx, reorderFixture{
		client:      client,
		baseURL:     srv.BaseURL,
		quizViewURL: fmt.Sprintf("%s/admin/quizzes/%d", srv.BaseURL, qz.ID),
		quizID:      qz.ID,
		rounds:      map[string]int64{"R1": r1.ID, "R2": r2.ID},
		questions:   questions,
	}
}

// seedReorderQuestions creates the three fixture questions (Q1, Q2 in R1;
// Q3 in R2) and returns their ids keyed by text.
func seedReorderQuestions(
	ctx context.Context, t *testing.T, stores *store.Stores, quizID, r1ID, r2ID int64,
) map[string]int64 {
	t.Helper()

	questions := map[string]int64{}
	specs := []struct {
		text    string
		roundID int64
		pos     int
	}{
		{"Q1 What river runs through Prague?", r1ID, 1},
		{"Q2 Capital of Portugal?", r1ID, 2},
		{"Q3 Tallest mountain?", r2ID, 3},
	}
	for _, s := range specs {
		q := &quiz.Question{
			QuizID:   quizID,
			RoundID:  s.roundID,
			Text:     s.text,
			Position: s.pos,
			Options:  []*quiz.Option{{Text: "A", Correct: true}, {Text: "B"}},
		}
		if cerr := stores.Quizzes.CreateQuestion(ctx, q); cerr != nil {
			t.Fatalf("CreateQuestion %q err = %v, want nil", s.text, cerr)
		}
		questions[s.text] = q.ID
	}

	return questions
}

// postFormStatus posts form to target and returns the response status
// code. Folded out so the failure-mode subtests share their shape.
func postFormStatus(
	ctx context.Context, t *testing.T, client *http.Client, target string, form url.Values,
) int {
	t.Helper()

	req := newFormReq(ctx, t, target, form)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do err = %v, want nil", err)
	}
	defer closeBody(t, resp.Body)

	return resp.StatusCode
}

// TestAdminPosition_QuestionMoveWithinRound drives the drag-and-drop
// question position endpoint for a within-round reorder (#199): the HX
// fragment swap returns 200 with the new order.
func TestAdminPosition_QuestionMoveWithinRound(t *testing.T) {
	t.Parallel()

	ctx, f := seedReorderQuiz(t)
	target := fmt.Sprintf("%s/admin/quizzes/%d/questions/%d/position",
		f.baseURL, f.quizID, f.questions["Q2 Capital of Portugal?"])

	token := fetchCSRFToken(ctx, t, f.client, f.quizViewURL)
	form := url.Values{
		"csrf_token":   {token},
		"new_position": {"1"},
		"round_id":     {strconv.FormatInt(f.rounds["R1"], 10)},
	}
	body := postHXForm(ctx, t, f.client, target, form)

	if !strings.Contains(body, `id="questions-list"`) {
		t.Errorf("HX body should keep id=\"questions-list\", got %q", body)
	}
	// Q2 moved to the top of R1, so it now renders before Q1.
	q2at := strings.Index(body, "Q2 Capital of Portugal?")
	q1at := strings.Index(body, "Q1 What river runs through Prague?")
	if q2at < 0 || q1at < 0 || q2at > q1at {
		t.Errorf("expected Q2 before Q1 in fragment, got Q2@%d Q1@%d", q2at, q1at)
	}
}

// TestAdminPosition_QuestionMoveCrossRound drives a cross-round question
// move: Q1 moves from R1 into R2; the endpoint returns the 200 partial.
func TestAdminPosition_QuestionMoveCrossRound(t *testing.T) {
	t.Parallel()

	ctx, f := seedReorderQuiz(t)
	target := fmt.Sprintf("%s/admin/quizzes/%d/questions/%d/position",
		f.baseURL, f.quizID, f.questions["Q1 What river runs through Prague?"])

	token := fetchCSRFToken(ctx, t, f.client, f.quizViewURL)
	form := url.Values{
		"csrf_token":   {token},
		"new_position": {"1"},
		"round_id":     {strconv.FormatInt(f.rounds["R2"], 10)},
	}
	body := postHXForm(ctx, t, f.client, target, form)
	if !strings.Contains(body, `id="questions-list"`) {
		t.Errorf("HX body should keep id=\"questions-list\", got %q", body)
	}
	if !strings.Contains(body, "Q1 What river runs through Prague?") {
		t.Errorf("fragment should still contain the moved question, got %q", body)
	}
}

// TestAdminPosition_QuestionInputFailures covers the question endpoint's
// input/auth failure modes (#199): missing CSRF -> 403, non-integer
// position -> 400, foreign id -> 404.
func TestAdminPosition_QuestionInputFailures(t *testing.T) {
	t.Parallel()

	ctx, f := seedReorderQuiz(t)
	positionURL := func(questionID int64) string {
		return fmt.Sprintf("%s/admin/quizzes/%d/questions/%d/position", f.baseURL, f.quizID, questionID)
	}
	r1 := strconv.FormatInt(f.rounds["R1"], 10)

	t.Run("missing CSRF token returns 403", func(t *testing.T) {
		t.Parallel()
		form := url.Values{"new_position": {"1"}, "round_id": {r1}}
		got := postFormStatus(ctx, t, f.client, positionURL(f.questions["Q3 Tallest mountain?"]), form)
		if want := http.StatusForbidden; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("invalid position returns 400", func(t *testing.T) {
		t.Parallel()
		token := fetchCSRFToken(ctx, t, f.client, f.quizViewURL)
		form := url.Values{"csrf_token": {token}, "new_position": {"not-a-number"}, "round_id": {r1}}
		got := postFormStatus(ctx, t, f.client, positionURL(f.questions["Q3 Tallest mountain?"]), form)
		if want := http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("foreign question id returns 404", func(t *testing.T) {
		t.Parallel()
		token := fetchCSRFToken(ctx, t, f.client, f.quizViewURL)
		form := url.Values{"csrf_token": {token}, "new_position": {"1"}, "round_id": {r1}}
		got := postFormStatus(ctx, t, f.client, positionURL(999999), form)
		if want := http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})
}

// TestAdminPosition_RoundMove drives the drag-and-drop round position
// endpoint end to end (#199): moving R2 to the front returns the 200
// partial with the rounds in their new order.
func TestAdminPosition_RoundMove(t *testing.T) {
	t.Parallel()

	ctx, f := seedReorderQuiz(t)
	target := fmt.Sprintf("%s/admin/quizzes/%d/rounds/%d/position", f.baseURL, f.quizID, f.rounds["R2"])

	token := fetchCSRFToken(ctx, t, f.client, f.quizViewURL)
	form := url.Values{"csrf_token": {token}, "new_position": {"1"}}
	body := postHXForm(ctx, t, f.client, target, form)

	if !strings.Contains(body, `id="questions-list"`) {
		t.Errorf("HX body should keep id=\"questions-list\", got %q", body)
	}
	// R2 ("Round Two") moved ahead of the default round, so its Q3 now
	// renders before R1's Q1.
	q3at := strings.Index(body, "Q3 Tallest mountain?")
	q1at := strings.Index(body, "Q1 What river runs through Prague?")
	if q3at < 0 || q1at < 0 || q3at > q1at {
		t.Errorf("expected Q3 before Q1 after round move, got Q3@%d Q1@%d", q3at, q1at)
	}
}

// TestAdminPosition_RoundInputFailures covers the round endpoint's
// input/auth failure modes (#199).
func TestAdminPosition_RoundInputFailures(t *testing.T) {
	t.Parallel()

	ctx, f := seedReorderQuiz(t)
	positionURL := func(roundID int64) string {
		return fmt.Sprintf("%s/admin/quizzes/%d/rounds/%d/position", f.baseURL, f.quizID, roundID)
	}

	t.Run("missing CSRF token returns 403", func(t *testing.T) {
		t.Parallel()
		form := url.Values{"new_position": {"1"}}
		got := postFormStatus(ctx, t, f.client, positionURL(f.rounds["R1"]), form)
		if want := http.StatusForbidden; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("invalid position returns 400", func(t *testing.T) {
		t.Parallel()
		token := fetchCSRFToken(ctx, t, f.client, f.quizViewURL)
		form := url.Values{"csrf_token": {token}, "new_position": {"abc"}}
		got := postFormStatus(ctx, t, f.client, positionURL(f.rounds["R1"]), form)
		if want := http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("foreign round id returns 404", func(t *testing.T) {
		t.Parallel()
		token := fetchCSRFToken(ctx, t, f.client, f.quizViewURL)
		form := url.Values{"csrf_token": {token}, "new_position": {"1"}}
		got := postFormStatus(ctx, t, f.client, positionURL(999999), form)
		if want := http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})
}

// TestAdminPosition_NonOwnerGated covers the requireQuizOwner gate on the
// position endpoints: a signed-in Host who does not own the quiz gets a
// 403 from both endpoints (#199 mirrors the move-route gating).
func TestAdminPosition_NonOwnerGated(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_EMAILS":         "reorder-boss@example.test",
	})
	baseURL := srv.BaseURL

	registerAdminClient(ctx, t, baseURL, srv.DBURI, "reorder-boss")
	adminA := registerAdminClient(ctx, t, baseURL, srv.DBURI, "reorder-owner")
	adminB := registerAdminClient(ctx, t, baseURL, srv.DBURI, "reorder-intruder")
	makeHost(ctx, t, srv.DBURI, "reorder-owner")
	makeHost(ctx, t, srv.DBURI, "reorder-intruder")

	quizID := createQuizAs(ctx, t, adminA, baseURL, "Non-owner Reorder Quiz")

	db, stores := openStores(t, srv.DBURI)
	defer closeBody(t, db)
	defaultRound, err := stores.Quizzes.GetDefaultRound(ctx, quizID)
	if err != nil {
		t.Fatalf("GetDefaultRound err = %v, want nil", err)
	}

	t.Run("non-owner POST round position returns 404", func(t *testing.T) {
		t.Parallel()
		token := fetchCSRFToken(ctx, t, adminB, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
		form := url.Values{"csrf_token": {token}, "new_position": {"1"}}
		target := fmt.Sprintf("%s/admin/quizzes/%d/rounds/%d/position", baseURL, quizID, defaultRound.ID)
		if got, want := postFormStatus(ctx, t, adminB, target, form), http.StatusNotFound; got != want {
			t.Errorf("round position status = %d, want %d", got, want)
		}
	})

	t.Run("non-owner POST question position returns 404", func(t *testing.T) {
		t.Parallel()
		token := fetchCSRFToken(ctx, t, adminB, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
		form := url.Values{
			"csrf_token":   {token},
			"new_position": {"1"},
			"round_id":     {strconv.FormatInt(defaultRound.ID, 10)},
		}
		target := fmt.Sprintf("%s/admin/quizzes/%d/questions/%d/position", baseURL, quizID, 1)
		if got, want := postFormStatus(ctx, t, adminB, target, form), http.StatusNotFound; got != want {
			t.Errorf("question position status = %d, want %d", got, want)
		}
	})
}

// postHXForm submits an HTMX form POST and returns the body, asserting a
// 200 text/html fragment. Shared by the position-reorder HX paths.
func postHXForm(ctx context.Context, t *testing.T, client *http.Client, target string, form url.Values) string {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Hx-Request", "true")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("HX status = %d, want %d, body = %q", got, want, body)
	}
	if got, want := resp.Header.Get("Content-Type"), "text/html"; !strings.HasPrefix(got, want) {
		t.Errorf("HX Content-Type = %q, want prefix %q", got, want)
	}

	return string(body)
}
