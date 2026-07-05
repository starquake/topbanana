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
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// postForm is a small wrapper around client.Do that always defers a
// body close so the bodyclose linter stays happy. Returns the status
// code, Location header, and (best-effort) body bytes - enough for the
// round CRUD assertions without leaking the response.
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

// openRoundStores opens a Stores bundle against the server's DB so a
// test can resolve auto-assigned round ids by title (the round form
// auto-assigns positions, so there is no client-visible id on the
// create redirect). Closed via t.Cleanup.
func openRoundStores(t *testing.T, dbURI string) *store.Stores {
	t.Helper()
	db, err := sql.Open("sqlite", dbURI)
	if err != nil {
		t.Fatalf("sql.Open err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v, want nil", cerr)
		}
	})

	return store.New(db, slog.Default())
}

// roundByTitle returns the round with the given title on the quiz, or
// fails the test when none matches. Used to recover the id of a round
// created through the HTTP form, whose position (and therefore id) the
// server assigned.
func roundByTitle(
	ctx context.Context, t *testing.T, stores *store.Stores, quizID int64, title string,
) *quiz.Round {
	t.Helper()
	rounds, err := stores.Quizzes.ListRoundsByQuiz(ctx, quizID)
	if err != nil {
		t.Fatalf("ListRoundsByQuiz err = %v, want nil", err)
	}
	for _, r := range rounds {
		if r.Title == title {
			return r
		}
	}
	t.Fatalf("no round titled %q on quiz %d", title, quizID)

	return nil
}

// TestRounds_CRUD covers the admin routes for the round entity (#444).
// Every quiz starts with a default 'Round 1'; the flow adds a second
// round, edits its title + summary, and deletes it, checking the
// rendered quiz view picks up each transition.
func TestRounds_CRUD(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_EMAILS":         "round-admin@example.test",
	})
	baseURL := srv.BaseURL
	stores := openRoundStores(t, srv.DBURI)

	client := registerAdminClient(ctx, t, baseURL, srv.DBURI, "round-admin")
	quizID := createQuizAs(ctx, t, client, baseURL, "Quiz With Rounds")

	// --- Create a second round ----------------------------------------------
	createToken := fetchCSRFToken(
		ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d/rounds/new", quizID),
	)
	status, location, body := postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/rounds", quizID),
		url.Values{
			"title":      {"Picture Round"},
			"summary":    {"Halfway summary"},
			"csrf_token": {createToken},
		},
	)
	if got, want := status, http.StatusSeeOther; got != want {
		t.Fatalf("create round status = %d, want %d; body=%q", got, want, body)
	}
	if got, want := location, fmt.Sprintf("/admin/quizzes/%d", quizID); got != want {
		t.Errorf("create Location = %q, want %q", got, want)
	}

	viewBody := readBody(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	if got, want := viewBody, "Picture Round"; !strings.Contains(got, want) {
		t.Errorf("quiz view should contain round title %q", want)
	}
	if got, want := viewBody, "Halfway summary"; !strings.Contains(got, want) {
		t.Errorf("quiz view should contain round summary %q", want)
	}

	roundID := roundByTitle(ctx, t, stores, quizID, "Picture Round").ID

	// --- Edit the round - rename + change summary ---------------------------
	editToken := fetchCSRFToken(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/rounds/%d/edit", quizID, roundID),
	)
	status, _, body = postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/rounds/%d", quizID, roundID),
		url.Values{
			"title":      {"Music Round"},
			"summary":    {"Almost done!"},
			"csrf_token": {editToken},
		},
	)
	if got, want := status, http.StatusSeeOther; got != want {
		t.Fatalf("edit round status = %d, want %d; body=%q", got, want, body)
	}

	viewBody = readBody(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	if got, want := viewBody, "Music Round"; !strings.Contains(got, want) {
		t.Errorf("quiz view should contain updated round title %q", want)
	}
	if got, want := viewBody, "Almost done!"; !strings.Contains(got, want) {
		t.Errorf("quiz view should contain updated round summary %q", want)
	}
	if got, want := viewBody, "Picture Round"; strings.Contains(got, want) {
		t.Errorf("quiz view still contains the stale round title %q", want)
	}

	// --- Delete the round ---------------------------------------------------
	deleteToken := fetchCSRFToken(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	status, _, body = postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/rounds/%d/delete", quizID, roundID),
		url.Values{"csrf_token": {deleteToken}},
	)
	if got, want := status, http.StatusSeeOther; got != want {
		t.Fatalf("delete round status = %d, want %d; body=%q", got, want, body)
	}

	viewBody = readBody(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	if got, want := viewBody, "Music Round"; strings.Contains(got, want) {
		t.Errorf("quiz view still contains deleted round title %q", want)
	}
}

// TestRounds_Move drives the per-round up/down arrows through the admin
// route (#444). Three rounds (the default plus two added) are reordered
// and the rendered quiz view's substring order is checked each step.
func TestRounds_Move(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_EMAILS":         "round-move-admin@example.test",
	})
	baseURL := srv.BaseURL
	stores := openRoundStores(t, srv.DBURI)

	client := registerAdminClient(ctx, t, baseURL, srv.DBURI, "round-move-admin")
	quizID := createQuizAs(ctx, t, client, baseURL, "Quiz With Round Moves")

	// Rename the default round so the substring order assertions have
	// three distinct titles to track.
	defaultRound := roundByTitle(ctx, t, stores, quizID, "Round 1")
	renameToken := fetchCSRFToken(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/rounds/%d/edit", quizID, defaultRound.ID),
	)
	status, _, body := postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/rounds/%d", quizID, defaultRound.ID),
		url.Values{"title": {"Alpha"}, "csrf_token": {renameToken}},
	)
	if got, want := status, http.StatusSeeOther; got != want {
		t.Fatalf("rename default round status = %d, want %d; body=%q", got, want, body)
	}

	addRound(ctx, t, client, baseURL, quizID, "Bravo")
	addRound(ctx, t, client, baseURL, quizID, "Charlie")

	// Sanity check: rendered order is Alpha, Bravo, Charlie.
	viewBody := readBody(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	assertOrder(t, viewBody, "initial", "Alpha", "Bravo", "Charlie")

	bravoID := roundByTitle(ctx, t, stores, quizID, "Bravo").ID

	// Move Bravo down once - swaps with Charlie.
	moveToken := fetchCSRFToken(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	status, _, body = postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/rounds/%d/move/down", quizID, bravoID),
		url.Values{"csrf_token": {moveToken}},
	)
	if got, want := status, http.StatusSeeOther; got != want {
		t.Fatalf("move-down status = %d, want %d; body=%q", got, want, body)
	}

	viewBody = readBody(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	assertOrder(t, viewBody, "after move-down", "Alpha", "Charlie", "Bravo")

	// Move Bravo up - settles back to the middle.
	moveToken = fetchCSRFToken(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	status, _, body = postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/rounds/%d/move/up", quizID, bravoID),
		url.Values{"csrf_token": {moveToken}},
	)
	if got, want := status, http.StatusSeeOther; got != want {
		t.Fatalf("move-up status = %d, want %d; body=%q", got, want, body)
	}

	viewBody = readBody(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	assertOrder(t, viewBody, "after move-up", "Alpha", "Bravo", "Charlie")
}

// addRound posts a round-create form with the given title and asserts
// the 303 redirect. The round form auto-assigns the position, so the
// new round lands at the end of the round list.
func addRound(ctx context.Context, t *testing.T, client *http.Client, baseURL string, quizID int64, title string) {
	t.Helper()
	token := fetchCSRFToken(
		ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d/rounds/new", quizID),
	)
	status, _, body := postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/rounds", quizID),
		url.Values{"title": {title}, "csrf_token": {token}},
	)
	if got, want := status, http.StatusSeeOther; got != want {
		t.Fatalf("add round %q status = %d, want %d; body=%q", title, got, want, body)
	}
}

// assertOrder pins the substring order of needles in haystack and
// reports the first violation with a stage label so failures point at
// which click broke the sequence.
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

// TestRounds_EmptyTitleRejected pins the round-form validation: a blank
// title re-renders the form at 400 with the inline field error rather
// than persisting a nameless round (#444).
func TestRounds_EmptyTitleRejected(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_EMAILS":         "round-validation-admin@example.test",
	})
	baseURL := srv.BaseURL

	client := registerAdminClient(ctx, t, baseURL, srv.DBURI, "round-validation-admin")
	quizID := createQuizAs(ctx, t, client, baseURL, "Quiz Round Validation")

	createToken := fetchCSRFToken(
		ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d/rounds/new", quizID),
	)
	status, _, body := postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/rounds", quizID),
		url.Values{"title": {""}, "csrf_token": {createToken}},
	)
	if got, want := status, http.StatusBadRequest; got != want {
		t.Fatalf("empty-title status = %d, want %d; body=%q", got, want, body)
	}
	if got, want := string(body), "Give the round a name."; !strings.Contains(got, want) {
		t.Errorf("empty-title body should contain %q; got=%q", want, body)
	}
}

// TestRounds_NonOwnerForbidden exercises the requireQuizOwner gate on
// the mutating round routes. Mirrors the question IDOR tests' shape so a
// future creator-only-edit rule change touches one spot.
func TestRounds_NonOwnerForbidden(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		// A throwaway first registrant consumes the first-registrant Admin
		// promotion so both owners under test are Hosts (own-games-only),
		// which is the tier the requireQuizOwner gate distinguishes.
		"ADMIN_EMAILS": "rounds-boss@example.test",
	})
	baseURL := srv.BaseURL

	registerAdminClient(ctx, t, baseURL, srv.DBURI, "rounds-boss")
	adminA := registerAdminClient(ctx, t, baseURL, srv.DBURI, "rounds-owner-a")
	adminB := registerAdminClient(ctx, t, baseURL, srv.DBURI, "rounds-owner-b")
	makeHost(ctx, t, srv.DBURI, "rounds-owner-a")
	makeHost(ctx, t, srv.DBURI, "rounds-owner-b")
	quizID := createQuizAs(ctx, t, adminA, baseURL, "Owned Quiz With Rounds")

	t.Run("non-owner GET new round form returns 404", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, adminB, baseURL+fmt.Sprintf("/admin/quizzes/%d/rounds/new", quizID))
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("non-owner POST create returns 404", func(t *testing.T) {
		t.Parallel()
		token := fetchCSRFToken(ctx, t, adminB, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
		status, _, _ := postForm(
			ctx, t, adminB,
			baseURL+fmt.Sprintf("/admin/quizzes/%d/rounds", quizID),
			url.Values{"title": {"hijacked"}, "csrf_token": {token}},
		)
		if got, want := status, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})
}

// TestRounds_DeleteQuizCascadesRounds confirms the FK ON DELETE CASCADE
// on rounds(quiz_id). The store-level cascade is unit-tested in
// internal/store; this integration probe exercises the same invariant
// end-to-end through the admin delete-quiz route, using the default
// round every quiz is created with.
func TestRounds_DeleteQuizCascadesRounds(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_EMAILS":         "round-cascade-admin@example.test",
	})
	baseURL := srv.BaseURL
	stores := openRoundStores(t, srv.DBURI)

	client := registerAdminClient(ctx, t, baseURL, srv.DBURI, "round-cascade-admin")
	quizID := createQuizAs(ctx, t, client, baseURL, "Quiz To Delete With Rounds")

	roundID := roundByTitle(ctx, t, stores, quizID, "Round 1").ID

	deleteToken := fetchCSRFToken(ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
	status, _, body := postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/delete", quizID),
		url.Values{"csrf_token": {deleteToken}},
	)
	if got, want := status, http.StatusSeeOther; got != want {
		t.Fatalf("delete quiz status = %d, want %d; body=%q", got, want, body)
	}

	// The round must be gone with its parent quiz. ErrRoundNotFound is
	// the cascade signal.
	if _, err := stores.Quizzes.GetRound(ctx, roundID); !errors.Is(err, quiz.ErrRoundNotFound) {
		t.Errorf("GetRound after quiz delete err = %v, want %v", err, quiz.ErrRoundNotFound)
	}
}

// readBody GETs target and returns the response body as a string.
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
