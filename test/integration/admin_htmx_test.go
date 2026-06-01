//go:build integration

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
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// TestAdminHTMX_QuestionReorder pins the HX-Request branch on the
// question reorder endpoint. The fragment swap is the first piece of
// HTMX in the admin surface (#213 phase 4), so this test locks in:
//
//   - HX-Request: true -> 200 + text/html fragment (not the full page).
//   - The fragment contains both question texts in the new order.
//   - The fragment carries id="questions-list" so subsequent swaps
//     still find their hx-target.
//   - A request without HX-Request still 303-redirects, so the no-JS
//     fallback path stays intact.
func TestAdminHTMX_QuestionReorder(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	// Open a *sql.DB against the same URI so we can seed a quiz with
	// two questions directly - keeps this test focused on the reorder
	// endpoint rather than re-exercising the full create-quiz flow.
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

	// Register an admin via the HTTP flow first so we can attribute the
	// seeded quiz to their player id. Owner-gated routes (#281) reject
	// the reorder POST if the session player isn't the quiz creator,
	// so seeding under the seeded admin would 403 every probe.
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
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "htmx-admin", "htmx-admin-pass-123")

	adminPlayer, err := stores.Players.GetPlayerByDisplayName(ctx, "htmx-admin")
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
	}

	const (
		questionOneText = "What is the river running through Prague?"
		questionTwoText = "What is the capital of Portugal?"
	)
	qz := &quiz.Quiz{
		Title:             "HTMX Reorder Quiz",
		Slug:              "htmx-reorder",
		Description:       "seed for the HTMX integration test",
		CreatedByPlayerID: adminPlayer.ID,
		Questions: []*quiz.Question{
			{
				Text:     questionOneText,
				Position: 1,
				Options: []*quiz.Option{
					{Text: "Vltava", Correct: true},
					{Text: "Danube"},
				},
			},
			{
				Text:     questionTwoText,
				Position: 2,
				Options: []*quiz.Option{
					{Text: "Lisbon", Correct: true},
					{Text: "Madrid"},
				},
			},
		},
	}
	if cerr := stores.Quizzes.CreateQuiz(ctx, qz); cerr != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", cerr)
	}

	// Fetch the quiz view to seed the CSRF nonce on the jar and pull
	// out the matching hidden token. The reorder POST has to carry
	// both halves.
	quizViewURL := fmt.Sprintf("%s/admin/quizzes/%d", srv.BaseURL, qz.ID)
	csrfToken := fetchCSRFToken(ctx, t, client, quizViewURL)

	moveDownURL := fmt.Sprintf(
		"%s/admin/quizzes/%d/questions/%d/move/down",
		srv.BaseURL, qz.ID, qz.Questions[0].ID,
	)

	// HX-Request path: expect a 200 fragment.
	moveForm := url.Values{}
	moveForm.Add("csrf_token", csrfToken)

	hxReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, moveDownURL, strings.NewReader(moveForm.Encode()),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	hxReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// HTMX sends "HX-Request: true" on the wire; using Go's canonical
	// form here keeps canonicalheader happy and matches the spelling the
	// handler reads back via r.Header.Get.
	hxReq.Header.Set("Hx-Request", "true")

	hxResp, err := client.Do(hxReq)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	hxBody, err := io.ReadAll(hxResp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if cerr := hxResp.Body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}

	if got, want := hxResp.StatusCode, http.StatusOK; got != want {
		t.Errorf("HX status = %d, want %d, body = %q", got, want, hxBody)
	}
	if got, want := hxResp.Header.Get("Content-Type"), "text/html"; !strings.HasPrefix(got, want) {
		t.Errorf("HX Content-Type = %q, want prefix %q", got, want)
	}
	body := string(hxBody)
	// Both questions present - proves the partial rendered against
	// real data and includes the post-swap order.
	if !strings.Contains(body, questionOneText) {
		t.Errorf("HX body = %q, should contain question one text %q", body, questionOneText)
	}
	if !strings.Contains(body, questionTwoText) {
		t.Errorf("HX body = %q, should contain question two text %q", body, questionTwoText)
	}
	// The wrapper id has to survive the swap so the next hx-post still
	// has its target. Pin it.
	if !strings.Contains(body, `id="questions-list"`) {
		t.Errorf("HX body should keep id=\"questions-list\" on the wrapper, got %q", body)
	}
	// Negative check: a real fragment is just the question list, no
	// navbar / page shell. The Tailwind body-class string only renders
	// from base.gohtml, so its absence proves we returned a fragment.
	if strings.Contains(body, `bg-bg text-text font-sans antialiased`) {
		t.Errorf("HX body should NOT contain the full-page <body> classes, got %q", body)
	}

	// Refresh the CSRF token for the second POST - the nonce cookie
	// rolls per request.
	csrfToken = fetchCSRFToken(ctx, t, client, quizViewURL)

	// Non-HX path: same endpoint, no HX-Request header. Expect the
	// classic 303 redirect.
	plainForm := url.Values{}
	plainForm.Add("csrf_token", csrfToken)

	// Reorder back so the test leaves state predictable, but the
	// assertion is on the response shape regardless of direction.
	moveUpURL := fmt.Sprintf(
		"%s/admin/quizzes/%d/questions/%d/move/up",
		srv.BaseURL, qz.ID, qz.Questions[0].ID,
	)
	plainReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, moveUpURL, strings.NewReader(plainForm.Encode()),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	plainReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	plainResp, err := client.Do(plainReq)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	if cerr := plainResp.Body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}

	if got, want := plainResp.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("non-HX status = %d, want %d", got, want)
	}
	if got, want := plainResp.Header.Get("Location"), fmt.Sprintf("/admin/quizzes/%d", qz.ID); got != want {
		t.Errorf("non-HX Location = %q, want %q", got, want)
	}
}

// TestAdminHTMX_RoundMove pins the HX-Request branch on the round
// reorder endpoint (#444). Mirrors TestAdminHTMX_QuestionReorder so
// the round path stays symmetric with the question path: the HX
// request gets a 200 fragment (page scroll preserved) and the no-JS
// fallback still 303-redirects.
//
// Also covers the ErrRoundMoveImpossible HX branch: asking to move the
// last round down returns the unchanged partial at 200 rather than the
// 4xx the other sentinel errors get, because the store's "target slot
// unavailable" outcome is treated as a no-op so a stale-form click
// renders cleanly.
func TestAdminHTMX_RoundMove(t *testing.T) {
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
	registerVerifyAndSignIn(ctx, t, client, srv.BaseURL, srv.DBURI, "htmx-admin", "htmx-admin-pass-123")

	adminPlayer, err := stores.Players.GetPlayerByDisplayName(ctx, "htmx-admin")
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
	}

	const (
		questionOneText = "What is the river running through Prague?"
		secondRoundName = "Second Round"
	)
	qz := &quiz.Quiz{
		Title:             "HTMX Round Reorder Quiz",
		Slug:              "htmx-round-reorder",
		Description:       "seed for the HTMX round-move integration test",
		CreatedByPlayerID: adminPlayer.ID,
		Questions: []*quiz.Question{
			{
				Text:     questionOneText,
				Position: 1,
				Options:  []*quiz.Option{{Text: "Vltava", Correct: true}, {Text: "Danube"}},
			},
		},
	}
	if cerr := stores.Quizzes.CreateQuiz(ctx, qz); cerr != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", cerr)
	}

	// Every quiz starts with a default round at position 0 holding its
	// questions. Add a second round at position 1 so the default round
	// has a neighbour to move down past, and the default round can hit
	// the ErrRoundMoveImpossible branch once it sits last.
	defaultRound, err := stores.Quizzes.GetDefaultRound(ctx, qz.ID)
	if err != nil {
		t.Fatalf("GetDefaultRound err = %v, want nil", err)
	}
	secondRound := &quiz.Round{QuizID: qz.ID, Position: 1, Title: secondRoundName}
	if cerr := stores.Quizzes.CreateRound(ctx, secondRound); cerr != nil {
		t.Fatalf("CreateRound err = %v, want nil", cerr)
	}

	quizViewURL := fmt.Sprintf("%s/admin/quizzes/%d", srv.BaseURL, qz.ID)

	moveDownURL := fmt.Sprintf(
		"%s/admin/quizzes/%d/rounds/%d/move/down",
		srv.BaseURL, qz.ID, defaultRound.ID,
	)

	// HX-Request happy path: moving the default round down swaps it with
	// the second round. Expect a 200 fragment carrying both round
	// titles, the wrapper id, and no full-page body classes.
	csrfToken := fetchCSRFToken(ctx, t, client, quizViewURL)
	hxBody := postHXRoundMove(ctx, t, client, moveDownURL, csrfToken)
	if !strings.Contains(hxBody, secondRoundName) {
		t.Errorf("HX body = %q, should contain second round name %q", hxBody, secondRoundName)
	}
	if !strings.Contains(hxBody, questionOneText) {
		t.Errorf("HX body = %q, should contain question one text %q", hxBody, questionOneText)
	}
	if !strings.Contains(hxBody, `id="questions-list"`) {
		t.Errorf("HX body should keep id=\"questions-list\" on the wrapper, got %q", hxBody)
	}
	if strings.Contains(hxBody, `bg-bg text-text font-sans antialiased`) {
		t.Errorf("HX body should NOT contain the full-page <body> classes, got %q", hxBody)
	}

	// HX-Request impossible-move: the default round now sits last (it
	// swapped to position 1). Asking to move it down again hits
	// ErrRoundMoveImpossible in the store - the HX path must still
	// 200 + return the unchanged partial, not a 4xx.
	csrfToken = fetchCSRFToken(ctx, t, client, quizViewURL)
	hxImpossibleBody := postHXRoundMove(ctx, t, client, moveDownURL, csrfToken)
	if !strings.Contains(hxImpossibleBody, secondRoundName) {
		t.Errorf(
			"HX impossible body = %q, should still contain second round name %q",
			hxImpossibleBody,
			secondRoundName,
		)
	}
	if !strings.Contains(hxImpossibleBody, `id="questions-list"`) {
		t.Errorf("HX impossible body should keep id=\"questions-list\", got %q", hxImpossibleBody)
	}

	// Non-HX path: same endpoint, no HX-Request header. Classic 303
	// redirect.
	csrfToken = fetchCSRFToken(ctx, t, client, quizViewURL)
	plainForm := url.Values{}
	plainForm.Add("csrf_token", csrfToken)
	plainReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, moveDownURL, strings.NewReader(plainForm.Encode()),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	plainReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	plainResp, err := client.Do(plainReq)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	if cerr := plainResp.Body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}
	if got, want := plainResp.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("non-HX status = %d, want %d", got, want)
	}
	if got, want := plainResp.Header.Get("Location"), fmt.Sprintf("/admin/quizzes/%d", qz.ID); got != want {
		t.Errorf("non-HX Location = %q, want %q", got, want)
	}
}

// postHXRoundMove submits an HTMX form POST to a round move endpoint
// and returns the response body, asserting the response is a 200
// text/html fragment along the way. Folded out of TestAdminHTMX_RoundMove
// so the happy and impossible-move paths share their shape and only
// the body checks differ.
func postHXRoundMove(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	target, csrfToken string,
) string {
	t.Helper()

	form := url.Values{}
	form.Add("csrf_token", csrfToken)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, target, strings.NewReader(form.Encode()),
	)
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
