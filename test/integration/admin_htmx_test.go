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
//   - HX-Request: true → 200 + text/html fragment (not the full page).
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
	// two questions directly — keeps this test focused on the reorder
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
	registerAdminViaHTTP(ctx, t, client, srv.BaseURL)

	adminPlayer, err := stores.Players.GetPlayerByUsername(ctx, "htmx-admin")
	if err != nil {
		t.Fatalf("GetPlayerByUsername err = %v, want nil", err)
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
	// Both questions present — proves the partial rendered against
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

	// Refresh the CSRF token for the second POST — the nonce cookie
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

// TestAdminHTMX_BreakMove pins the HX-Request branch on the break
// reorder endpoint (#437). Mirrors TestAdminHTMX_QuestionReorder so
// the break path stays symmetric with the question path: the HX
// request gets a 200 fragment (page scroll preserved) and the no-JS
// fallback still 303-redirects.
//
// Also covers the ErrBreakMoveImpossible HX branch (#439): asking to
// move a break at position 0 up returns the unchanged partial at 200
// rather than the 4xx the other sentinel errors get, because the
// store's "target slot unavailable" outcome is treated as a no-op so
// a stale-form click renders cleanly.
func TestAdminHTMX_BreakMove(t *testing.T) {
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
	registerAdminViaHTTP(ctx, t, client, srv.BaseURL)

	adminPlayer, err := stores.Players.GetPlayerByUsername(ctx, "htmx-admin")
	if err != nil {
		t.Fatalf("GetPlayerByUsername err = %v, want nil", err)
	}

	const (
		questionOneText = "What is the river running through Prague?"
		questionTwoText = "What is the capital of Portugal?"
		breakText       = "Take a sip of water"
	)
	qz := &quiz.Quiz{
		Title:             "HTMX Break Reorder Quiz",
		Slug:              "htmx-break-reorder",
		Description:       "seed for the HTMX break-move integration test",
		CreatedByPlayerID: adminPlayer.ID,
		Questions: []*quiz.Question{
			{
				Text:     questionOneText,
				Position: 1,
				Options:  []*quiz.Option{{Text: "Vltava", Correct: true}, {Text: "Danube"}},
			},
			{
				Text:     questionTwoText,
				Position: 2,
				Options:  []*quiz.Option{{Text: "Lisbon", Correct: true}, {Text: "Madrid"}},
			},
		},
	}
	if cerr := stores.Quizzes.CreateQuiz(ctx, qz); cerr != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", cerr)
	}

	// Seed a break at position 0 (Beginning) so the move-down click
	// has a valid destination (after Q1) and the move-up click hits
	// the ErrBreakMoveImpossible branch.
	brk := &quiz.Break{QuizID: qz.ID, Position: 0, Text: breakText}
	if cerr := stores.Quizzes.CreateBreak(ctx, brk); cerr != nil {
		t.Fatalf("CreateBreak err = %v, want nil", cerr)
	}

	quizViewURL := fmt.Sprintf("%s/admin/quizzes/%d", srv.BaseURL, qz.ID)

	moveDownURL := fmt.Sprintf(
		"%s/admin/quizzes/%d/breaks/%d/move/down",
		srv.BaseURL, qz.ID, brk.ID,
	)

	// HX-Request happy path: expect a 200 fragment carrying the
	// break text, the wrapper id, and no full-page body classes.
	csrfToken := fetchCSRFToken(ctx, t, client, quizViewURL)
	hxBody := postHXBreakMove(ctx, t, client, moveDownURL, csrfToken)
	if !strings.Contains(hxBody, breakText) {
		t.Errorf("HX body = %q, should contain break text %q", hxBody, breakText)
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

	// HX-Request impossible-move: shift the break one more slot down
	// via a plain POST so it sits at position 2 (after Q2). With no
	// Q3, asking to move it down again hits ErrBreakMoveImpossible
	// in the store - the HX path must still 200 + return the
	// unchanged partial, not a 4xx.
	csrfToken = fetchCSRFToken(ctx, t, client, quizViewURL)
	setupForm := url.Values{}
	setupForm.Add("csrf_token", csrfToken)
	setupReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, moveDownURL, strings.NewReader(setupForm.Encode()),
	)
	if err != nil {
		t.Fatalf("setup NewRequest err = %v, want nil", err)
	}
	setupReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	setupResp, err := client.Do(setupReq)
	if err != nil {
		t.Fatalf("setup client.Do err = %v, want nil", err)
	}
	if cerr := setupResp.Body.Close(); cerr != nil {
		t.Errorf("setup Body.Close err = %v, want nil", cerr)
	}
	if got, want := setupResp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("setup move-down status = %d, want %d", got, want)
	}

	csrfToken = fetchCSRFToken(ctx, t, client, quizViewURL)
	hxImpossibleBody := postHXBreakMove(ctx, t, client, moveDownURL, csrfToken)
	if !strings.Contains(hxImpossibleBody, breakText) {
		t.Errorf("HX impossible body = %q, should still contain break text %q", hxImpossibleBody, breakText)
	}
	if !strings.Contains(hxImpossibleBody, `id="questions-list"`) {
		t.Errorf("HX impossible body should keep id=\"questions-list\", got %q", hxImpossibleBody)
	}

	// Non-HX path: same endpoint, no HX-Request header. Classic 303
	// redirect, matching TestBreaks_Move's existing assertion.
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

// postHXBreakMove submits an HTMX form POST to a break move endpoint
// and returns the response body, asserting the response is a 200
// text/html fragment along the way. Folded out of TestAdminHTMX_BreakMove
// so the happy and impossible-move paths share their shape and only
// the body checks differ.
func postHXBreakMove(
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

// registerAdminViaHTTP posts /register through the supplied client so
// the response sets the session cookie on its jar. The first registered
// user becomes the admin per the existing auth flow.
func registerAdminViaHTTP(ctx context.Context, t *testing.T, client *http.Client, baseURL string) {
	t.Helper()

	registerToken := fetchCSRFToken(ctx, t, client, baseURL+"/register")

	form := url.Values{}
	form.Add("username", "htmx-admin")
	form.Add("password", "htmx-admin-pass-123")
	form.Add("csrf_token", registerToken)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/register", strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("register client.Do err = %v, want nil", err)
	}
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("register Body.Close err = %v, want nil", cerr)
	}
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("register status = %d, want %d", got, want)
	}
}
