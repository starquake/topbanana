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
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// TestAdminHTMX_DeleteSwaps pins the HX-Request short-circuit on the
// admin delete/reset endpoints converted to inline htmx swaps (#986):
// quiz delete, question delete, round delete, and player reset. Each
// returns an empty 200 with no Location header to an HX request (so
// htmx removes the row via its outerHTML swap) while a plain form post
// still 303-redirects, keeping the no-JS fallback intact.
func TestAdminHTMX_DeleteSwaps(t *testing.T) {
	t.Parallel()

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
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "htmx-del-admin", "htmx-del-pass-123")

	adminPlayer, err := stores.Players.GetPlayerByDisplayName(ctx, "htmx-del-admin")
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
	}

	seedDelQuiz := func(t *testing.T, slug string) *quiz.Quiz {
		t.Helper()
		qz := &quiz.Quiz{
			Title:             "HTMX Delete Quiz " + slug,
			Slug:              slug,
			Description:       "seed for the HTMX delete-swap integration test",
			CreatedByPlayerID: adminPlayer.ID,
			Questions: []*quiz.Question{
				{
					Text:     "What is the capital of Portugal?",
					Position: 1,
					Options:  []*quiz.Option{{Text: "Lisbon", Correct: true}, {Text: "Madrid"}},
				},
			},
		}
		if cerr := stores.Quizzes.CreateQuiz(ctx, qz); cerr != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", cerr)
		}

		return qz
	}

	// The four endpoints share one admin jar and the per-request CSRF
	// nonce rolls on each fetch, so the cases run sequentially rather
	// than as parallel subtests. Each seeds its own quiz, then probes the
	// HX (empty 200) and plain (303) paths against a fresh quiz so the
	// delete of one does not invalidate the next.

	// Quiz delete.
	quizHX := seedDelQuiz(t, "htmx-del-quiz-hx")
	assertHXDelete(ctx, t, client, srv.BaseURL,
		fmt.Sprintf("/admin/quizzes/%d/delete", quizHX.ID))
	quizPlain := seedDelQuiz(t, "htmx-del-quiz-plain")
	assertPlainDelete(ctx, t, client, srv.BaseURL,
		fmt.Sprintf("/admin/quizzes/%d/delete", quizPlain.ID), "/admin/quizzes")

	// Question delete.
	qHX := seedDelQuiz(t, "htmx-del-q-hx")
	assertHXDelete(ctx, t, client, srv.BaseURL,
		fmt.Sprintf("/admin/quizzes/%d/questions/%d/delete", qHX.ID, qHX.Questions[0].ID))
	qPlain := seedDelQuiz(t, "htmx-del-q-plain")
	assertPlainDelete(ctx, t, client, srv.BaseURL,
		fmt.Sprintf("/admin/quizzes/%d/questions/%d/delete", qPlain.ID, qPlain.Questions[0].ID),
		fmt.Sprintf("/admin/quizzes/%d", qPlain.ID))

	// Round delete (the default round cascades to its questions).
	rHX := seedDelQuiz(t, "htmx-del-r-hx")
	rHXRound, err := stores.Quizzes.GetDefaultRound(ctx, rHX.ID)
	if err != nil {
		t.Fatalf("GetDefaultRound err = %v, want nil", err)
	}
	assertHXDelete(ctx, t, client, srv.BaseURL,
		fmt.Sprintf("/admin/quizzes/%d/rounds/%d/delete", rHX.ID, rHXRound.ID))
	rPlain := seedDelQuiz(t, "htmx-del-r-plain")
	rPlainRound, err := stores.Quizzes.GetDefaultRound(ctx, rPlain.ID)
	if err != nil {
		t.Fatalf("GetDefaultRound err = %v, want nil", err)
	}
	assertPlainDelete(ctx, t, client, srv.BaseURL,
		fmt.Sprintf("/admin/quizzes/%d/rounds/%d/delete", rPlain.ID, rPlainRound.ID),
		fmt.Sprintf("/admin/quizzes/%d", rPlain.ID))

	// Player reset. The reset deletes the player's game on the quiz; with
	// no game it is a no-op, which is enough to pin the HX short-circuit
	// and the no-JS fallback. The full reset-with-played-game flow lives
	// in gameplay_test.go.
	resetHX := seedDelQuiz(t, "htmx-reset-hx")
	assertHXDelete(ctx, t, client, srv.BaseURL,
		fmt.Sprintf("/admin/quizzes/%d/players/%d/reset", resetHX.ID, adminPlayer.ID))
	resetPlain := seedDelQuiz(t, "htmx-reset-plain")
	assertPlainDelete(ctx, t, client, srv.BaseURL,
		fmt.Sprintf("/admin/quizzes/%d/players/%d/reset", resetPlain.ID, adminPlayer.ID),
		fmt.Sprintf("/admin/quizzes/%d", resetPlain.ID))
}

// assertHXDelete posts a CSRF-bearing form with HX-Request: true to the
// target and asserts the empty-200 short-circuit: status 200, no
// Location header, empty body.
func assertHXDelete(ctx context.Context, t *testing.T, client *http.Client, baseURL, path string) {
	t.Helper()

	token := fetchCSRFToken(ctx, t, client, baseURL+"/admin/quizzes")
	form := url.Values{"csrf_token": {token}}
	req := newFormReq(ctx, t, baseURL+path, form)
	req.Header.Set("Hx-Request", "true")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("HX delete Do err = %v, want nil", err)
	}
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("HX delete status = %d, want %d; body=%q", got, want, body)
	}
	if got := resp.Header.Get("Location"); got != "" {
		t.Errorf("HX delete Location = %q, want empty", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("HX delete body read err = %v, want nil", err)
	}
	if len(body) != 0 {
		t.Errorf("HX delete body = %q, want empty", body)
	}
}

// assertPlainDelete posts a CSRF-bearing form without HX-Request and
// asserts the classic 303 redirect to wantLocation, proving the no-JS
// fallback still works.
func assertPlainDelete(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, path, wantLocation string,
) {
	t.Helper()

	token := fetchCSRFToken(ctx, t, client, baseURL+"/admin/quizzes")
	form := url.Values{"csrf_token": {token}}
	req := newFormReq(ctx, t, baseURL+path, form)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("plain delete Do err = %v, want nil", err)
	}
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("plain delete status = %d, want %d; body=%q", got, want, body)
	}
	if got, want := resp.Header.Get("Location"), wantLocation; got != want {
		t.Errorf("plain delete Location = %q, want %q", got, want)
	}
}

// verifyPlayerEmail stamps email_verified_at on the named player so
// follow-up requests can pass the #111 PR3 verified-email gate. Used
// by integration tests that drive /admin/* after registering through
// the HTTP register flow.
func verifyPlayerEmail(ctx context.Context, t *testing.T, dbURI, displayName string) {
	t.Helper()

	dbConn, stores := openStores(t, dbURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	player, err := stores.Players.GetPlayerByDisplayName(ctx, displayName)
	if err != nil {
		t.Fatalf("verifyPlayerEmail GetPlayerByDisplayName err = %v, want nil", err)
	}
	if err := stores.OAuth.MarkPlayerEmailVerifiedIfNew(ctx, player.ID); err != nil {
		t.Fatalf("verifyPlayerEmail MarkPlayerEmailVerifiedIfNew err = %v, want nil", err)
	}
}
