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
	"github.com/starquake/topbanana/internal/store"
	"github.com/starquake/topbanana/internal/testutil"
)

// httpGet issues a GET with a request-scoped context so the noctx linter is
// happy and the request gets cancelled when the test ends.
func httpGet(ctx context.Context, t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}

	return resp
}

// httpPostJSON issues a POST with a JSON body and a request-scoped context.
func httpPostJSON(ctx context.Context, t *testing.T, client *http.Client, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}

	return resp
}

// nextQuestionOption mirrors one option in the GET /api/games/.../next
// response. Pulled out so the parent decode target isn't a nested struct
// (revive's nested-structs rule).
type nextQuestionOption struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
}

// nextQuestionRes is the decode target for GET /api/games/.../next.
type nextQuestionRes struct {
	ID      int64                `json:"id"`
	Text    string               `json:"text"`
	Options []nextQuestionOption `json:"options"`
}

// playerScoreRes mirrors one player_scores entry in the GET .../results
// response. Pulled out for the same nested-structs reason.
type playerScoreRes struct {
	PlayerID int64 `json:"playerId"`
	Score    int   `json:"score"`
}

// resultsRes is the decode target for GET /api/games/.../results.
type resultsRes struct {
	GameID       string           `json:"gameId"`
	PlayerScores []playerScoreRes `json:"playerScores"`
}

// leaderboardEntryRes mirrors one entry in the leaderboard response. Pulled
// out of the parent struct to keep nested-structs-friendly types.
type leaderboardEntryRes struct {
	PlayerID        int64  `json:"playerId"`
	Username        string `json:"username"`
	Score           int    `json:"score"`
	IsCurrentPlayer bool   `json:"isCurrentPlayer"`
}

// leaderboardRes is the decode target for GET /api/quizzes/{slugID}/leaderboard.
type leaderboardRes struct {
	QuizID  int64                 `json:"quizId"`
	Entries []leaderboardEntryRes `json:"entries"`
}

// integrationSetup bundles the artefacts a gameplay-style integration test
// needs. Context is intentionally returned separately from the struct (passed
// out of setupIntegration as the first return value) to avoid containedctx.
type integrationSetup struct {
	Stop    context.CancelFunc
	ErrCh   chan error
	BaseURL string
	Stores  *store.Stores
}

func setupIntegration(t *testing.T) (context.Context, integrationSetup) {
	t.Helper()

	var err error

	ctx, stop := testutil.SignalCtx(t)

	stdout := testutil.NewTestWriter(t)

	dbURI, cleanup := dbtest.SetupTestDB(t)
	t.Cleanup(cleanup)

	getenv := func(key string) string {
		env := map[string]string{
			"HOST":   "localhost",
			"PORT":   "0", // Let the OS choose an available port
			"DB_URI": dbURI,
		}

		return env[key]
	}

	listenConfig := &net.ListenConfig{}
	var ln net.Listener
	ln, err = listenConfig.Listen(ctx, "tcp", net.JoinHostPort(getenv("HOST"), getenv("PORT")))
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(ctx, getenv, stdout, ln)
	}()

	serverAddr := ln.Addr().String()
	baseURL := "http://" + serverAddr
	err = testutil.WaitForReady(ctx, t, 10*time.Second, baseURL+"/healthz")
	if err != nil {
		t.Fatalf("error waiting for server to be ready: %v", err)
	}

	// Setup seed data for the integration test
	db, err := sql.Open("sqlite", dbURI)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v, want nil", cerr)
		}
	})

	stores := store.New(db, slog.Default())

	return ctx, integrationSetup{
		Stop:    stop,
		ErrCh:   errCh,
		BaseURL: baseURL,
		Stores:  stores,
	}
}

func TestGameplay_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	stop := setup.Stop
	errCh := setup.ErrCh
	baseURL := setup.BaseURL
	stores := setup.Stores

	qz := &quiz.Quiz{
		Title:       "Integration Quiz",
		Slug:        "integration-quiz",
		Description: "A quiz for integration testing",
		Questions: []*quiz.Question{
			{
				Text:     "Question 1",
				Position: 1,
				Options: []*quiz.Option{
					{Text: "Correct 1", Correct: true},
					{Text: "Incorrect 1", Correct: false},
				},
			},
			{
				Text:     "Question 2",
				Position: 2,
				Options: []*quiz.Option{
					{Text: "Correct 2", Correct: true},
					{Text: "Incorrect 2", Correct: false},
				},
			},
			{
				Text:     "Question 3",
				Position: 3,
				Options: []*quiz.Option{
					{Text: "Correct 3", Correct: true},
					{Text: "Incorrect 3", Correct: false},
				},
			},
		},
	}

	if err := stores.Quizzes.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("failed to create quiz: %v", err)
	}

	// Start of the integration test. The cookie jar carries the anonymous
	// session cookie that EnsurePlayer issues on the first request, so
	// every subsequent request is attributed to the same player row.
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	var resp *http.Response

	// Get a list of quizzes
	resp = httpGet(ctx, t, client, baseURL+"/api/quizzes")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var quizzesRes []struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	err = json.NewDecoder(resp.Body).Decode(&quizzesRes)
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("resp.Body.Close err = %v, want nil", cerr)
	}
	if err != nil {
		t.Fatalf("failed to decode quizzes response: %v", err)
	}
	if got, want := len(quizzesRes), 1; got != want {
		t.Fatalf("got %d quizzes, want %d", got, want)
	}
	if got, want := quizzesRes[0].Title, qz.Title; got != want {
		t.Fatalf("got quiz title %q, want %q", got, want)
	}
	if got, want := quizzesRes[0].Description, qz.Description; got != want {
		t.Fatalf("got quiz description %q, want %q", got, want)
	}

	// Create Game
	createGameReq := fmt.Sprintf(`{"quizId": %d}`, qz.ID)
	resp = httpPostJSON(ctx, t, client, baseURL+"/api/games", createGameReq)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", resp.StatusCode)
	}

	var createGameRes struct {
		ID string `json:"id"`
	}
	err = json.NewDecoder(resp.Body).Decode(&createGameRes)
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("resp.Body.Close err = %v, want nil", cerr)
	}
	if err != nil {
		t.Fatalf("failed to decode create game response: %v", err)
	}
	gameID := createGameRes.ID

	runningScore := 0
	// Walk through questions
	for i := range 3 {
		// Get Next Question
		resp = httpGet(ctx, t, client, fmt.Sprintf("%s/api/games/%s/questions/next", baseURL, gameID))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected status 200, got %d", resp.StatusCode)
		}

		var nextQsRes nextQuestionRes
		err = json.NewDecoder(resp.Body).Decode(&nextQsRes)
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("resp.Body.Close err = %v, want nil", cerr)
		}
		if err != nil {
			t.Fatalf("failed to decode next question response: %v", err)
		}

		// Find correct or incorrect option
		// Let's pick correct for first and last, incorrect for middle
		targetCorrect := i != 1
		var optionID int64
		found := false

		// We need to know which option is correct from the seeded data
		// but the API doesn't return that (as it shouldn't).
		// We can find it from our local 'qz' variable.
		for _, q := range qz.Questions {
			if q.ID == nextQsRes.ID {
				for _, o := range q.Options {
					if o.Correct == targetCorrect {
						optionID = o.ID
						found = true

						break
					}
				}
			}
			if found {
				break
			}
		}

		if !found {
			t.Fatalf("could not find target option for question %d", i+1)
		}

		// Submit Answer
		answerReq := fmt.Sprintf(`{"optionId": %d}`, optionID)
		answerURL := fmt.Sprintf("%s/api/games/%s/questions/%d/answers", baseURL, gameID, nextQsRes.ID)
		resp = httpPostJSON(ctx, t, client, answerURL, answerReq)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected status 200, got %d", resp.StatusCode)
		}

		var answerRes struct {
			Correct bool `json:"correct"`
			Score   int  `json:"score"`
		}
		err = json.NewDecoder(resp.Body).Decode(&answerRes)
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("resp.Body.Close err = %v, want nil", cerr)
		}
		if err != nil {
			t.Fatalf("failed to decode results response: %v", err)
		}
		if got, want := answerRes.Correct, targetCorrect; got != want {
			t.Fatalf("got correct %v, want %v", got, want)
		}
		runningScore += answerRes.Score
	}

	// Get Results
	resp = httpGet(ctx, t, client, fmt.Sprintf("%s/api/games/%s/results", baseURL, gameID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var results resultsRes
	err = json.NewDecoder(resp.Body).Decode(&results)
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("resp.Body.Close err = %v, want nil", cerr)
	}
	if err != nil {
		t.Fatalf("failed to decode results response: %v", err)
	}

	if got, want := results.GameID, gameID; got != want {
		t.Fatalf("got game ID %q, want %q", got, want)
	}
	if got, want := len(results.PlayerScores), 1; got != want {
		t.Fatalf("got %d player scores, want %d", got, want)
	}

	// The server picks the player ID for an anonymous session, so we
	// don't assert a specific value — just that it is a real row.
	if got := results.PlayerScores[0].PlayerID; got <= 0 {
		t.Fatalf("got player ID %d, want > 0", got)
	}
	if got, want := results.PlayerScores[0].Score, runningScore; got != want {
		t.Fatalf("got score %d, want %d", got, want)
	}

	// Verify no more questions
	resp = httpGet(ctx, t, client, fmt.Sprintf("%s/api/games/%s/questions/next", baseURL, gameID))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", resp.StatusCode)
	}
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("resp.Body.Close err = %v, want nil", cerr)
	}

	// Quiz leaderboard: the player who just finished should appear with
	// IsCurrentPlayer=true and the same score they accumulated above.
	leaderboardURL := fmt.Sprintf("%s/api/quizzes/%s-%d/leaderboard", baseURL, qz.Slug, qz.ID)
	resp = httpGet(ctx, t, client, leaderboardURL)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var leaderboard leaderboardRes
	err = json.NewDecoder(resp.Body).Decode(&leaderboard)
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("resp.Body.Close err = %v, want nil", cerr)
	}
	if err != nil {
		t.Fatalf("failed to decode leaderboard response: %v", err)
	}

	if got, want := leaderboard.QuizID, qz.ID; got != want {
		t.Fatalf("leaderboard.QuizID = %d, want %d", got, want)
	}
	if got, want := len(leaderboard.Entries), 1; got != want {
		t.Fatalf("len(leaderboard.Entries) = %d, want %d", got, want)
	}
	if got, want := leaderboard.Entries[0].IsCurrentPlayer, true; got != want {
		t.Errorf("leaderboard.Entries[0].IsCurrentPlayer = %v, want %v", got, want)
	}
	if got, want := leaderboard.Entries[0].Score, runningScore; got != want {
		t.Errorf("leaderboard.Entries[0].Score = %d, want %d", got, want)
	}
	if got := leaderboard.Entries[0].PlayerID; got <= 0 {
		t.Errorf("leaderboard.Entries[0].PlayerID = %d, want > 0", got)
	}

	// Shutdown server
	stop()
	select {
	case err = <-errCh:
		// Ignore context.Canceled because we triggered it ourselves via stop()
		if err != nil && !errors.Is(err, context.Background().Err()) && !errors.Is(err, context.Canceled) {
			t.Errorf("run() returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("server failed to shutdown in time")
	}
}
