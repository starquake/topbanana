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
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/starquake/topbanana/cmd/server/app"
	"github.com/starquake/topbanana/internal/database"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
	"github.com/starquake/topbanana/internal/testutil"
)

func TestGameplay_Integration(t *testing.T) {
	t.Parallel()

	database.SetupGoose()

	var err error

	ctx, stop := testutil.SignalCtx(t)

	stdout := testutil.NewTestWriter(t)

	dbURI, cleanup := dbtest.SetupTestDB(t)
	defer cleanup()

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
	baseURL := fmt.Sprintf("http://%s", serverAddr)
	err = testutil.WaitForReady(ctx, t, 10*time.Second, fmt.Sprintf("%s/healthz", baseURL))
	if err != nil {
		t.Fatalf("error waiting for server to be ready: %v", err)
	}

	// Setup seed data for the integration test
	db, err := sql.Open("sqlite", dbURI)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	stores := store.New(db, slog.Default())

	// Create a player (ID 1 as hardcoded in clientapi)
	_, err = db.ExecContext(ctx, "INSERT INTO players (id, username, email) VALUES (1, 'tester', 'tester@example.com')")
	if err != nil {
		t.Fatalf("failed to create player: %v", err)
	}

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

	err = stores.Quizzes.CreateQuiz(ctx, qz)
	if err != nil {
		t.Fatalf("failed to create quiz: %v", err)
	}

	// Start of the integration test
	client := &http.Client{}

	// Create Game
	createGameReq := fmt.Sprintf(`{"quizId": %d}`, qz.ID)
	resp, err := client.Post(baseURL+"/api/games", "application/json", strings.NewReader(createGameReq))
	if err != nil {
		t.Fatalf("failed to create game: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", resp.StatusCode)
	}

	var createGameRes struct {
		ID string `json:"id"`
	}
	err = json.NewDecoder(resp.Body).Decode(&createGameRes)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("failed to decode create game response: %v", err)
	}
	gameID := createGameRes.ID

	// Walk through questions
	for i := 0; i < 3; i++ {
		// Get Next Question
		resp, err = client.Get(fmt.Sprintf("%s/api/games/%s/questions/next", baseURL, gameID))
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected status 200, got %d", resp.StatusCode)
		}

		var nextQsRes struct {
			ID      int64  `json:"id"`
			Text    string `json:"text"`
			Options []struct {
				ID   int64  `json:"id"`
				Text string `json:"text"`
			} `json:"options"`
		}
		err = json.NewDecoder(resp.Body).Decode(&nextQsRes)
		resp.Body.Close()
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
		resp, err = client.Post(answerURL, "application/json", strings.NewReader(answerReq))
		if err != nil {
			t.Fatalf("failed to submit answer: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected status 200, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	}

	// Verify no more questions
	resp, err = client.Get(fmt.Sprintf("%s/api/games/%s/questions/next", baseURL, gameID))
	if err != nil {
		t.Fatalf("failed to get next question (final): %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()

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
