//go:build integration

package integration_test

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// TestGameTenancy_Integration covers #272: the three player-facing endpoints
// keyed on a gameID — POST /answers, GET /questions/next, GET /results —
// must reject a player who is not a participant of the supplied gameID.
//
// Before the fix any authenticated visitor who learned a stranger's gameID
// could probe or mutate that game. This test mints two distinct anonymous
// players (separate cookie jars), starts a game as player A, then asserts
// player B's requests against A's gameID all return 404 without leaking
// game state.
func TestGameTenancy_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	baseURL := srv.BaseURL

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
		Title:       "Tenancy Quiz",
		Slug:        "tenancy-quiz",
		Description: "for the tenancy integration test",
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

	// Player A starts a game; player B has a separate cookie jar so the
	// session middleware mints them a different anonymous player row.
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

	gameID, _ := postCreateGame(ctx, t, clientA, baseURL, qz.ID)
	if gameID == "" {
		t.Fatal("expected non-empty game ID for player A")
	}

	// Touch /api/quizzes from clientB so the EnsurePlayer middleware
	// mints player B's row before any of the tenancy probes — otherwise
	// the first 404 we observe could be from the absent-player path
	// rather than the participant gate.
	fetchAPIQuizzes(ctx, t, clientB, baseURL)

	t.Run("GET /questions/next from a stranger returns 404", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, clientB, fmt.Sprintf("%s/api/games/%s/questions/next", baseURL, gameID))
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("POST /answers from a stranger returns 404", func(t *testing.T) {
		t.Parallel()
		body := `{"optionId": 1}`
		resp := httpPostJSON(
			ctx, t, clientB,
			fmt.Sprintf("%s/api/games/%s/questions/1/answers", baseURL, gameID),
			body,
		)
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("GET /results from a stranger returns 404", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, clientB, fmt.Sprintf("%s/api/games/%s/results", baseURL, gameID))
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}

		// Defensive: the 404 must not leak the body of a Results payload.
		// A naive future regression could land a `GetResults` that wrote
		// the body before checking ownership; pin the absence of the
		// telltale "playerScores" field so that path fails loudly.
		buf := make([]byte, 1024)
		n, _ := resp.Body.Read(buf)
		if strings.Contains(string(buf[:n]), `"playerScores"`) {
			t.Errorf("404 response leaked results body: %q", buf[:n])
		}
	})

	// Sanity: player A can still hit all three endpoints on their own
	// game. Without this the test could "pass" because the endpoints
	// were broken for everyone, not just strangers.
	t.Run("owner can still call /questions/next", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, clientA, fmt.Sprintf("%s/api/games/%s/questions/next", baseURL, gameID))
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})
}
