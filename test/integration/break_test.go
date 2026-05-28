//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// postForm is a small wrapper around client.Do that always defers a
// body close so the bodyclose linter stays happy. Returns the status
// code, Location header, and (best-effort) body bytes - enough for the
// break CRUD assertions without leaking the response.
func postForm(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	target string,
	form url.Values,
) (int, string, []byte) {
	t.Helper()
	req := newFormReq(ctx, t, target, form)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do %q err = %v, want nil", target, err)
	}
	defer closeBody(t, resp.Body)

	body, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, resp.Header.Get("Location"), body
}

// addQuestion posts a one-option question to the given quiz so the
// break-form dropdown has something to point at. Returns nothing
// because the only field downstream tests care about is the question's
// auto-assigned position, which the admin form increments from
// max(position)+1 (#352).
func addQuestion(ctx context.Context, t *testing.T, client *http.Client, baseURL string, quizID int64, text string) {
	t.Helper()
	token := fetchCSRFToken(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/questions/new", quizID),
	)
	form := url.Values{
		"text":              {text},
		"option[0].text":    {"option A"},
		"option[0].correct": {"on"},
		"option[1].text":    {"option B"},
		"csrf_token":        {token},
	}
	status, _, body := postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/questions", quizID),
		form,
	)
	if got, want := status, http.StatusSeeOther; got != want {
		t.Fatalf("add question %q status = %d, want %d; body=%q", text, got, want, body)
	}
}

// TestBreaks_CRUD covers the admin routes for the break entity (#167).
// The flow creates two questions then drives a break through create
// (at the beginning), edit (move to "after Q1"), and delete, checking
// the rendered quiz view picks up each transition.
func TestBreaks_CRUD(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_EMAILS":         "break-admin@example.test",
	})
	baseURL := srv.BaseURL

	client := registerAdminClient(ctx, t, baseURL, srv.DBURI, "break-admin")
	quizID := createQuizAs(ctx, t, client, baseURL, "Quiz With Breaks")

	// The break form's "Insert after" dropdown lists each question's
	// position, plus a (Beginning) entry. Add two questions so the
	// edit step has a non-zero slot to move the break onto.
	addQuestion(ctx, t, client, baseURL, quizID, "Q1 - capital of France?")
	addQuestion(ctx, t, client, baseURL, quizID, "Q2 - capital of Spain?")

	// --- Create a break at the beginning ------------------------------------
	createToken := fetchCSRFToken(
		ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks/new", quizID),
	)
	status, location, body := postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks", quizID),
		url.Values{
			"text":       {"Welcome, take a breath"},
			"position":   {"0"},
			"csrf_token": {createToken},
		},
	)
	if got, want := status, http.StatusSeeOther; got != want {
		t.Fatalf("create break status = %d, want %d; body=%q", got, want, body)
	}
	if got, want := location, fmt.Sprintf("/admin/quizzes/%d", quizID); got != want {
		t.Errorf("create Location = %q, want %q", got, want)
	}

	breakID := readFirstBreakID(ctx, t, client, baseURL, quizID)

	viewBody := readBody(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	if got, want := viewBody, "Welcome, take a breath"; !strings.Contains(got, want) {
		t.Errorf("quiz view should contain break text %q", want)
	}
	// Position=0 break must precede the first question in the rendered
	// sequence. Use the substring order as a cheap pin: break text
	// appears before Q1 text in the body.
	if breakIdx, q1Idx := strings.Index(viewBody, "Welcome, take a breath"),
		strings.Index(viewBody, "Q1 - capital of France?"); breakIdx == -1 || q1Idx == -1 || breakIdx > q1Idx {
		t.Errorf("expected break (idx=%d) to render before Q1 (idx=%d) in the sequence",
			breakIdx, q1Idx)
	}

	// --- Edit the break - move it to "after Q1" -----------------------------
	editToken := fetchCSRFToken(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks/%d/edit", quizID, breakID),
	)
	status, _, body = postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks/%d", quizID, breakID),
		url.Values{
			"text":       {"Almost done!"},
			"position":   {"1"},
			"csrf_token": {editToken},
		},
	)
	if got, want := status, http.StatusSeeOther; got != want {
		t.Fatalf("edit break status = %d, want %d; body=%q", got, want, body)
	}

	viewBody = readBody(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	if got, want := viewBody, "Almost done!"; !strings.Contains(got, want) {
		t.Errorf("quiz view should contain updated break text %q", want)
	}
	if got, want := viewBody, "Welcome, take a breath"; strings.Contains(got, want) {
		t.Errorf("quiz view still contains the stale break text %q", want)
	}
	// After the edit, the break should sit between Q1 and Q2 in the
	// sequence.
	q1Idx := strings.Index(viewBody, "Q1 - capital of France?")
	q2Idx := strings.Index(viewBody, "Q2 - capital of Spain?")
	breakIdx := strings.Index(viewBody, "Almost done!")
	if q1Idx == -1 || q2Idx == -1 || breakIdx == -1 || (q1Idx >= breakIdx || breakIdx >= q2Idx) {
		t.Errorf(
			"expected sequence order Q1(%d) < break(%d) < Q2(%d) after moving the break to position=1",
			q1Idx, breakIdx, q2Idx,
		)
	}

	// --- Delete the break ---------------------------------------------------
	deleteToken := fetchCSRFToken(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	status, _, body = postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks/%d/delete", quizID, breakID),
		url.Values{"csrf_token": {deleteToken}},
	)
	if got, want := status, http.StatusSeeOther; got != want {
		t.Fatalf("delete break status = %d, want %d; body=%q", got, want, body)
	}

	viewBody = readBody(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	if got, want := viewBody, "Almost done!"; strings.Contains(got, want) {
		t.Errorf("quiz view still contains deleted break text %q", want)
	}
}

// TestBreaks_Move drives the per-row up/down arrows on break rows
// through the admin route (#167). The break starts after Q1; one
// click on the down arrow lands it after Q2, then a click on the up
// arrow brings it back to position 1 - checked each step against the
// rendered quiz view's substring order.
func TestBreaks_Move(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_EMAILS":         "break-move-admin@example.test",
	})
	baseURL := srv.BaseURL

	client := registerAdminClient(ctx, t, baseURL, srv.DBURI, "break-move-admin")
	quizID := createQuizAs(ctx, t, client, baseURL, "Quiz With Break Moves")

	addQuestion(ctx, t, client, baseURL, quizID, "Q1 - first")
	addQuestion(ctx, t, client, baseURL, quizID, "Q2 - second")
	addQuestion(ctx, t, client, baseURL, quizID, "Q3 - third")

	// Create a break at position 1 (after Q1) - the middle slot, so
	// both directions are valid for the first click.
	createToken := fetchCSRFToken(
		ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks/new", quizID),
	)
	status, _, body := postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks", quizID),
		url.Values{
			"text":       {"middle break"},
			"position":   {"1"},
			"csrf_token": {createToken},
		},
	)
	if got, want := status, http.StatusSeeOther; got != want {
		t.Fatalf("create break status = %d, want %d; body=%q", got, want, body)
	}
	breakID := readFirstBreakID(ctx, t, client, baseURL, quizID)

	// Sanity check: rendered order is Q1, break, Q2, Q3.
	viewBody := readBody(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	assertOrder(t, viewBody, "initial",
		"Q1 - first", "middle break", "Q2 - second", "Q3 - third")

	// Move down once - break shifts to position 2 (after Q2). Use the
	// move-page CSRF token mounted on the quiz view itself.
	moveToken := fetchCSRFToken(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	status, _, body = postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks/%d/move/down", quizID, breakID),
		url.Values{"csrf_token": {moveToken}},
	)
	if got, want := status, http.StatusSeeOther; got != want {
		t.Fatalf("move-down status = %d, want %d; body=%q", got, want, body)
	}

	viewBody = readBody(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	assertOrder(t, viewBody, "after move-down",
		"Q1 - first", "Q2 - second", "middle break", "Q3 - third")

	// Move up - break settles back to position 1.
	moveToken = fetchCSRFToken(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	status, _, body = postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks/%d/move/up", quizID, breakID),
		url.Values{"csrf_token": {moveToken}},
	)
	if got, want := status, http.StatusSeeOther; got != want {
		t.Fatalf("move-up status = %d, want %d; body=%q", got, want, body)
	}

	viewBody = readBody(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	assertOrder(t, viewBody, "after move-up",
		"Q1 - first", "middle break", "Q2 - second", "Q3 - third")
}

// assertOrder pins the substring order of needles in haystack and
// reports the first violation with a stage label so failures point at
// which click broke the sequence. Inline rather than parsing HTML
// because the rendered admin view's substring order is the same
// invariant the existing break tests use.
func assertOrder(t *testing.T, haystack, stage string, needles ...string) {
	t.Helper()
	positions := make([]int, len(needles))
	for i, n := range needles {
		idx := strings.Index(haystack, n)
		if idx == -1 {
			t.Fatalf("%s: needle %q not found in body", stage, n)
		}
		positions[i] = idx
	}
	for i := 1; i < len(needles); i++ {
		if positions[i-1] >= positions[i] {
			t.Errorf(
				"%s: expected %q (idx=%d) to appear before %q (idx=%d)",
				stage, needles[i-1], positions[i-1], needles[i], positions[i],
			)
		}
	}
}

// TestBreaks_PositionCollision pins the inline-form-error rendered when
// an admin tries to insert a break on a slot that already has one
// (#167). Two breaks cannot share the same (quiz_id, position) slot.
func TestBreaks_PositionCollision(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_EMAILS":         "break-collision-admin@example.test",
	})
	baseURL := srv.BaseURL

	client := registerAdminClient(ctx, t, baseURL, srv.DBURI, "break-collision-admin")
	quizID := createQuizAs(ctx, t, client, baseURL, "Quiz Collision")
	addQuestion(ctx, t, client, baseURL, quizID, "Sole question")

	// First break at position 1 (after the only question).
	createToken := fetchCSRFToken(
		ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks/new", quizID),
	)
	status, _, body := postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks", quizID),
		url.Values{
			"text":       {"first"},
			"position":   {"1"},
			"csrf_token": {createToken},
		},
	)
	if got, want := status, http.StatusSeeOther; got != want {
		t.Fatalf("first create status = %d, want %d; body=%q", got, want, body)
	}

	// Second break submitting the same position - the unique index
	// rejects this and the handler re-renders the form with the
	// inline error.
	secondToken := fetchCSRFToken(
		ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks/new", quizID),
	)
	status, _, body = postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks", quizID),
		url.Values{
			"text":       {"second"},
			"position":   {"1"},
			"csrf_token": {secondToken},
		},
	)
	if got, want := status, http.StatusConflict; got != want {
		t.Fatalf("collision status = %d, want %d; body=%q", got, want, body)
	}
	if got, want := string(body), "A break already exists at that slot"; !strings.Contains(got, want) {
		t.Errorf("collision body should contain %q; got=%q", want, body)
	}
}

// TestBreaks_NonOwnerForbidden exercises the requireQuizOwner gate on
// every mutating break route. Mirrors TestQuizOwnership_Integration's
// shape so a future creator-only-edit rule change touches one spot.
func TestBreaks_NonOwnerForbidden(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_EMAILS":         "breaks-owner-a@example.test,breaks-owner-b@example.test",
	})
	baseURL := srv.BaseURL

	adminA := registerAdminClient(ctx, t, baseURL, srv.DBURI, "breaks-owner-a")
	adminB := registerAdminClient(ctx, t, baseURL, srv.DBURI, "breaks-owner-b")
	quizID := createQuizAs(ctx, t, adminA, baseURL, "Owned Quiz With Breaks")

	t.Run("non-owner GET new break form returns 403", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, adminB, baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks/new", quizID))
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusForbidden; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("non-owner POST create returns 403", func(t *testing.T) {
		t.Parallel()
		token := fetchCSRFToken(ctx, t, adminB, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
		status, _, _ := postForm(
			ctx, t, adminB,
			baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks", quizID),
			url.Values{"text": {"hijacked"}, "position": {"0"}, "csrf_token": {token}},
		)
		if got, want := status, http.StatusForbidden; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})
}

// TestBreaks_DeleteQuizCascadesBreaks confirms the FK ON DELETE
// CASCADE on breaks(quiz_id). The store-level cascade is unit-tested in
// internal/store; this integration probe exercises the same invariant
// end-to-end through the admin delete-quiz route.
func TestBreaks_DeleteQuizCascadesBreaks(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_EMAILS":         "break-cascade-admin@example.test",
	})
	baseURL := srv.BaseURL

	client := registerAdminClient(ctx, t, baseURL, srv.DBURI, "break-cascade-admin")
	quizID := createQuizAs(ctx, t, client, baseURL, "Quiz To Delete With Breaks")

	createToken := fetchCSRFToken(
		ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks/new", quizID),
	)
	status, _, body := postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks", quizID),
		url.Values{
			"text":       {"breaks must cascade"},
			"position":   {"0"},
			"csrf_token": {createToken},
		},
	)
	if got, want := status, http.StatusSeeOther; got != want {
		t.Fatalf("create break status = %d, want %d; body=%q", got, want, body)
	}
	breakID := readFirstBreakID(ctx, t, client, baseURL, quizID)

	deleteToken := fetchCSRFToken(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	status, _, body = postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/delete", quizID),
		url.Values{"csrf_token": {deleteToken}},
	)
	if got, want := status, http.StatusSeeOther; got != want {
		t.Fatalf("delete quiz status = %d, want %d; body=%q", got, want, body)
	}

	// Open the same DB the server is using and probe the break via the
	// sqlc-backed store. ErrBreakNotFound is the cascade signal - the
	// row is gone with the parent quiz.
	db, err := sql.Open("sqlite", srv.DBURI)
	if err != nil {
		t.Fatalf("sql.Open err = %v, want nil", err)
	}
	defer func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db close err = %v", cerr)
		}
	}()

	stores := store.New(db, slog.Default())
	if _, err := stores.Quizzes.GetBreak(ctx, breakID); !errors.Is(err, quiz.ErrBreakNotFound) {
		t.Errorf("GetBreak after quiz delete err = %v, want %v", err, quiz.ErrBreakNotFound)
	}
}

// breakIDPattern reads the first break id off the quiz view. The view
// renders one delete-break modal per break with id="modal-delete-break-
// {id}"; that's a stable selector to scrape an id without parsing HTML.
var breakIDPattern = regexp.MustCompile(`modal-delete-break-(\d+)`)

func readFirstBreakID(
	ctx context.Context, t *testing.T, client *http.Client, baseURL string, quizID int64,
) int64 {
	t.Helper()
	body := readBody(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	m := breakIDPattern.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no break id found on quiz view; body=%q", body)
	}
	var id int64
	if _, err := fmt.Sscanf(m[1], "%d", &id); err != nil {
		t.Fatalf("parse break id err = %v", err)
	}

	return id
}

func readBody(ctx context.Context, t *testing.T, client *http.Client, target string) string {
	t.Helper()
	resp := httpGet(ctx, t, client, target)
	defer closeBody(t, resp.Body)
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v", err)
	}

	return string(b)
}
