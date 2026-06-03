package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/session"
	"github.com/starquake/topbanana/internal/store"
)

// TestAnonymous_Integration exercises the score-claiming acceptance criteria:
//   - First /api/games request without a cookie creates a players row and
//     sets a session cookie on the response.
//   - Repeating that request from the same client reuses the row.
//   - Two distinct cookie jars produce two distinct anonymous players.
func TestAnonymous_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	baseURL := srv.BaseURL

	// Seed a quiz directly via the DB so we can ask the API to start a
	// game against it. Using the store keeps this independent of the admin
	// HTTP flow exercised in admin_test.go.
	dbConn, err := sql.Open("sqlite", srv.DBURI)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() {
		if cerr := dbConn.Close(); cerr != nil {
			t.Errorf("dbConn.Close err = %v, want nil", cerr)
		}
	})

	qz := &quiz.Quiz{
		Title:             "Anonymous Quiz",
		Slug:              "anonymous-quiz",
		Description:       "for the anonymous integration test",
		CreatedByPlayerID: seededAdminID,
		Questions: []*quiz.Question{
			{
				Text:     "Q1",
				Position: 1,
				Options: []*quiz.Option{
					{Text: "A", Correct: true},
					{Text: "B"},
				},
			},
		},
	}
	stores := store.New(dbConn, slog.Default())
	if createErr := stores.Quizzes.CreateQuiz(ctx, qz); createErr != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", createErr)
	}

	// Three scenarios run sequentially in this body (rather than as t.Run
	// subtests) because they share dbConn and the EnsurePlayer-managed
	// players-row count — paralleltest would force subtests to be parallel,
	// which would race the count-delta assertions below.

	// Scenario 1: first request without cookie creates an anonymous player
	// AND sets a session cookie on the response (otherwise repeat requests
	// can't reuse the row).
	jar1, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	client1 := &http.Client{Jar: jar1}

	startCount := countAnonymousPlayers(ctx, t, dbConn)
	gameID, setCookie := postCreateGame(ctx, t, client1, baseURL, qz.ID)
	if gameID == "" {
		t.Fatal("expected non-empty game ID")
	}
	if !setCookie {
		t.Fatal("expected Set-Cookie on first /api/games response")
	}
	if got, want := countAnonymousPlayers(ctx, t, dbConn)-startCount, 1; got != want {
		t.Errorf("[scenario 1] anonymous players added = %d, want %d", got, want)
	}

	// Scenario 1b: the newly-minted anonymous player should carry a
	// petname-style displayName (Adjective-Adjective-Noun) rather than the
	// legacy "anon-<xid>" format. The petname path is the default; the
	// xid fallback only runs when the petname pool collides several times
	// in a row, which is astronomically unlikely in a single-call test.
	if got, want := countLegacyAnonDisplayNames(ctx, t, dbConn), 0; got != want {
		t.Errorf("[scenario 1b] rows matching anon-%% = %d, want %d (petname path should win)", got, want)
	}

	// Scenario 2: a request that goes through EnsurePlayer reuses the
	// existing row when the cookie jar is reused. Creating a game and
	// then issuing a follow-up GET /api/quizzes (also wrapped in
	// EnsurePlayer) exercises the reuse path. We can't repeat
	// POST /api/games for the same player + quiz any more — #145
	// enforces one attempt per (player, quiz) — so this scenario tests
	// reuse via the safer GET route.
	jar2, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	client2 := &http.Client{Jar: jar2}

	startCount = countAnonymousPlayers(ctx, t, dbConn)
	_, _ = postCreateGame(ctx, t, client2, baseURL, qz.ID)
	fetchAPIQuizzes(ctx, t, client2, baseURL)
	fetchAPIQuizzes(ctx, t, client2, baseURL)
	if got, want := countAnonymousPlayers(ctx, t, dbConn)-startCount, 1; got != want {
		t.Errorf("[scenario 2] anonymous players added = %d, want %d (jar should reuse row)", got, want)
	}

	// Scenario 3: two cookie jars mint two distinct anonymous players.
	jarA, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	jarB, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	clientA := &http.Client{Jar: jarA}
	clientB := &http.Client{Jar: jarB}

	startCount = countAnonymousPlayers(ctx, t, dbConn)
	_, _ = postCreateGame(ctx, t, clientA, baseURL, qz.ID)
	_, _ = postCreateGame(ctx, t, clientB, baseURL, qz.ID)
	if got, want := countAnonymousPlayers(ctx, t, dbConn)-startCount, 2; got != want {
		t.Errorf("[scenario 3] anonymous players added = %d, want %d (two jars → two rows)", got, want)
	}

	// Scenario 4: an anonymous player can PATCH /api/players/me to set
	// their own display name. The row stays the same (still anonymous,
	// session cookie unchanged); only the displayName changes.
	if got, want := patchPlayerDisplayName(ctx, t, client1, baseURL, "named-one"), http.StatusOK; got != want {
		t.Errorf("[scenario 4] PATCH /api/players/me status = %d, want %d", got, want)
	}

	// Scenario 4b: GET /api/players/me reflects the new displayName and the
	// row is still anonymous (no password_hash) — the front-end relies
	// on this so the claim affordances disappear after a successful
	// PATCH but the flow keeps working without forcing a login.
	gotMe := fetchPlayerMe(ctx, t, client1, baseURL)
	if got, want := gotMe.DisplayName, "named-one"; got != want {
		t.Errorf("[scenario 4b] /me displayName = %q, want %q", got, want)
	}
	if got, want := gotMe.IsAnonymous, true; got != want {
		t.Errorf("[scenario 4b] /me isAnonymous = %v, want %v", got, want)
	}
	// hasCustomName flips to true on a successful PATCH; this is what
	// the frontend now gates the end-of-quiz claim modal on, so the
	// integration test pins the contract that a returning visitor with
	// a claimed name does not re-trigger the modal (#165).
	if got, want := gotMe.HasCustomName, true; got != want {
		t.Errorf("[scenario 4b] /me hasCustomName = %v, want %v (PATCH must flip the flag)", got, want)
	}

	// Scenario 4c: a freshly minted anonymous visitor (clientB has not
	// PATCHed yet) sees hasCustomName=false so the claim affordances
	// stay rendered. Counterpart to 4b — without this we cannot tell
	// whether the flag is wired or accidentally hard-coded to true.
	gotMeB := fetchPlayerMe(ctx, t, clientB, baseURL)
	if got, want := gotMeB.HasCustomName, false; got != want {
		t.Errorf("[scenario 4c] fresh /me hasCustomName = %v, want %v", got, want)
	}

	// Scenario 5: a second anonymous player trying to claim the same
	// displayName gets 409. The first player keeps "named-one".
	if got, want := patchPlayerDisplayName(ctx, t, clientA, baseURL, "named-one"), http.StatusConflict; got != want {
		t.Errorf("[scenario 5] colliding PATCH status = %d, want %d", got, want)
	}

	// Scenario 6: whitespace-only displayName is rejected as 400 by the
	// server-side trim — the front-end trims too, but the contract is
	// what the integration test pins down.
	if got, want := patchPlayerDisplayName(ctx, t, clientA, baseURL, "   "), http.StatusBadRequest; got != want {
		t.Errorf("[scenario 6] whitespace PATCH status = %d, want %d", got, want)
	}
}

// postCreateGame issues POST /api/games and returns the new game ID plus
// whether the response set the session cookie. Failures are reported as
// fatal because every assertion downstream needs a healthy game.
func postCreateGame(
	ctx context.Context, t *testing.T, client *http.Client, baseURL string, quizID int64,
) (string, bool) {
	t.Helper()

	body := fmt.Sprintf(`{"quizId": %d}`, quizID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/games", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("resp.Body.Close err = %v, want nil", err)
		}
	}()

	if got, want := resp.StatusCode, http.StatusCreated; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}

	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("json.Decode err = %v, want nil", err)
	}

	hadSessionCookie := false
	for _, c := range resp.Cookies() {
		if c.Name == session.CookieName {
			hadSessionCookie = true
		}
	}

	return out.ID, hadSessionCookie
}

// fetchAPIQuizzes issues GET /api/quizzes through the supplied client. The
// route is wrapped in EnsurePlayer so calling it on a cookie-bearing client
// exercises the player-row reuse path without creating a game (which the
// new one-attempt-per-quiz rule would block on the second call).
func fetchAPIQuizzes(ctx context.Context, t *testing.T, client *http.Client, baseURL string) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/quizzes", nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("resp.Body.Close err = %v, want nil", cerr)
		}
	}()
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("GET /api/quizzes status = %d, want %d", got, want)
	}
}

// meResponse mirrors the JSON shape returned by GET /api/players/me. Local
// to the integration test rather than imported from clientapi so the test
// double-pins the wire contract the front-end consumes.
type meResponse struct {
	ID              int64  `json:"id"`
	DisplayName     string `json:"displayName"`
	IsAnonymous     bool   `json:"isAnonymous"`
	HasCustomName   bool   `json:"hasCustomName"`
	IsAuthenticated bool   `json:"isAuthenticated"`
}

// fetchPlayerMe issues GET /api/players/me on the supplied client and
// returns the parsed response. Fatal on non-200 so callers can read the
// returned struct without nil-guards.
func fetchPlayerMe(ctx context.Context, t *testing.T, client *http.Client, baseURL string) meResponse {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/players/me", nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("resp.Body.Close err = %v, want nil", cerr)
		}
	}()
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("GET /api/players/me status = %d, want %d", got, want)
	}
	var out meResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("json.Decode err = %v, want nil", err)
	}

	return out
}

// patchPlayerDisplayName issues PATCH /api/players/me with the given displayName
// on the supplied client and returns the response status code. Body close
// errors are reported but don't fail the test.
func patchPlayerDisplayName(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, displayName string,
) int {
	t.Helper()

	body := fmt.Sprintf(`{"displayName": %q}`, displayName)
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPatch, baseURL+"/api/players/me", strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("resp.Body.Close err = %v, want nil", cerr)
	}

	return resp.StatusCode
}

// countAnonymousPlayers returns the number of rows with NULL password_hash.
// The EnsurePlayer middleware is the only path that creates such rows, so
// the value is a direct proxy for "how many anonymous visitors the server
// has minted so far".
func countAnonymousPlayers(ctx context.Context, t *testing.T, dbConn *sql.DB) int {
	t.Helper()

	var n int
	err := dbConn.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM players WHERE password_hash IS NULL`,
	).Scan(&n)
	if err != nil {
		t.Fatalf("QueryRow err = %v, want nil", err)
	}

	return n
}

// countLegacyAnonDisplayNames returns the number of anonymous rows whose
// displayName still uses the legacy "anon-<xid>" prefix. After #165, fresh
// anonymous players get a petname-style name; a non-zero count here
// indicates the xid fallback path ran (or the migration did not flow).
func countLegacyAnonDisplayNames(ctx context.Context, t *testing.T, dbConn *sql.DB) int {
	t.Helper()

	var n int
	err := dbConn.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM players WHERE password_hash IS NULL AND display_name LIKE 'anon-%'`,
	).Scan(&n)
	if err != nil {
		t.Fatalf("QueryRow err = %v, want nil", err)
	}

	return n
}
