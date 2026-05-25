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
// code, Location header, and (best-effort) body bytes — enough for the
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

// TestBreaks_CRUD covers the slice-1 admin routes for the new break
// entity (#167). The flow registers an admin, creates a quiz, adds /
// edits / deletes a break, and confirms the rendered quiz view picks
// up each transition.
func TestBreaks_CRUD(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_USERNAMES":      "break-admin",
	})
	baseURL := srv.BaseURL

	client := registerAdminClient(ctx, t, baseURL, "break-admin")
	quizID := createQuizAs(ctx, t, client, baseURL, "Quiz With Breaks")

	// --- Create a break -----------------------------------------------------
	createToken := fetchCSRFToken(
		ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks/new", quizID),
	)
	status, location, body := postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks", quizID),
		url.Values{
			"text":       {"Halfway, take a breather"},
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
	if got, want := viewBody, "Halfway, take a breather"; !strings.Contains(got, want) {
		t.Errorf("quiz view should contain break text %q", want)
	}
	if got, want := viewBody, "Breaks"; !strings.Contains(got, want) {
		t.Error("quiz view should contain Breaks section header")
	}

	// --- Edit the break -----------------------------------------------------
	editToken := fetchCSRFToken(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks/%d/edit", quizID, breakID),
	)
	status, _, body = postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks/%d", quizID, breakID),
		url.Values{
			"text":       {"Almost done!"},
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
	if got, want := viewBody, "Halfway, take a breather"; strings.Contains(got, want) {
		t.Errorf("quiz view still contains the stale break text %q", want)
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
	if got, want := viewBody, "This quiz has no breaks yet."; !strings.Contains(got, want) {
		t.Error("quiz view should fall back to empty-state placeholder")
	}
}

// TestBreaks_NonOwnerForbidden exercises the requireQuizOwner gate on
// every mutating break route. Mirrors TestQuizOwnership_Integration's
// shape so a future creator-only-edit rule change touches one spot.
func TestBreaks_NonOwnerForbidden(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_USERNAMES":      "breaks-owner-a,breaks-owner-b",
	})
	baseURL := srv.BaseURL

	adminA := registerAdminClient(ctx, t, baseURL, "breaks-owner-a")
	adminB := registerAdminClient(ctx, t, baseURL, "breaks-owner-b")
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
			url.Values{"text": {"hijacked"}, "csrf_token": {token}},
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
		"ADMIN_USERNAMES":      "break-cascade-admin",
	})
	baseURL := srv.BaseURL

	client := registerAdminClient(ctx, t, baseURL, "break-cascade-admin")
	quizID := createQuizAs(ctx, t, client, baseURL, "Quiz To Delete With Breaks")

	createToken := fetchCSRFToken(
		ctx, t, client, baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks/new", quizID),
	)
	status, _, body := postForm(
		ctx, t, client,
		baseURL+fmt.Sprintf("/admin/quizzes/%d/breaks", quizID),
		url.Values{
			"text":       {"breaks must cascade"},
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
	// sqlc-backed store. ErrBreakNotFound is the cascade signal — the
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
