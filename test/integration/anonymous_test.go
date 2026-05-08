//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/starquake/topbanana/cmd/server/app"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/session"
	"github.com/starquake/topbanana/internal/store"
	"github.com/starquake/topbanana/internal/testutil"
)

// TestAnonymous_Integration exercises the score-claiming acceptance criteria:
//   - First /api/games request without a cookie creates a players row and
//     sets a session cookie on the response.
//   - Repeating that request from the same client reuses the row.
//   - Two distinct cookie jars produce two distinct anonymous players.
func TestAnonymous_Integration(t *testing.T) {
	t.Parallel()

	ctx, stop := testutil.SignalCtx(t)

	stdout := testutil.NewTestWriter(t)

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	getenv := func(key string) string {
		env := map[string]string{
			"HOST":   "localhost",
			"PORT":   "0",
			"DB_URI": dbURI,
		}

		return env[key]
	}

	listenConfig := &net.ListenConfig{}
	ln, err := listenConfig.Listen(ctx, "tcp", net.JoinHostPort(getenv("HOST"), getenv("PORT")))
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(ctx, getenv, stdout, ln)
	}()

	serverAddr := ln.Addr().String()
	baseURL := "http://" + serverAddr
	if readyErr := testutil.WaitForReady(ctx, t, 10*time.Second, baseURL+"/healthz"); readyErr != nil {
		t.Fatalf("error waiting for server to be ready: %v", readyErr)
	}

	// Seed a quiz directly via the DB so we can ask the API to start a
	// game against it. Using the store keeps this independent of the admin
	// HTTP flow exercised in admin_test.go.
	dbConn, err := sql.Open("sqlite", dbURI)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() {
		if cerr := dbConn.Close(); cerr != nil {
			t.Errorf("dbConn.Close err = %v, want nil", cerr)
		}
	})

	qz := &quiz.Quiz{
		Title:       "Anonymous Quiz",
		Slug:        "anonymous-quiz",
		Description: "for the anonymous integration test",
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

	// Scenario 2: second request from same client reuses the existing row.
	jar2, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	client2 := &http.Client{Jar: jar2}

	startCount = countAnonymousPlayers(ctx, t, dbConn)
	_, _ = postCreateGame(ctx, t, client2, baseURL, qz.ID)
	_, _ = postCreateGame(ctx, t, client2, baseURL, qz.ID)
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

	stop()
	select {
	case err = <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("run() returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("server failed to shutdown in time")
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
