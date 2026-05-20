//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
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

// httpGet issues a GET with a request-scoped context so the noctx linter is
// happy and the request gets cancelled when the test ends.
func httpGet(ctx context.Context, t *testing.T, client *http.Client, target string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
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
func httpPostJSON(ctx context.Context, t *testing.T, client *http.Client, target, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body))
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

// registerAdminAndResetPlayer registers a fresh admin via the public
// /register form (the first password-bearing registrant becomes admin),
// then POSTs to /admin/quizzes/{quizID}/players/{playerID}/reset with a
// freshly-fetched CSRF token. Used by the gameplay test to exercise the
// admin reset path end-to-end after a player has finished a quiz.
func registerAdminAndResetPlayer(
	ctx context.Context, t *testing.T, client *http.Client, baseURL string, quizID, playerID int64,
) {
	t.Helper()

	// Step 1: GET /register to seed the CSRF nonce on the jar and pull
	// the matching hidden token out of the form.
	registerToken := fetchCSRFToken(ctx, t, client, baseURL+"/register")

	registerForm := url.Values{}
	registerForm.Add("username", "gameplay-admin")
	registerForm.Add("password", "gameplay-admin-pass-123")
	registerForm.Add("csrf_token", registerToken)

	registerReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/register",
		strings.NewReader(registerForm.Encode()),
	)
	if err != nil {
		t.Fatalf("failed to build register request: %v", err)
	}
	registerReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	registerResp, err := client.Do(registerReq)
	if err != nil {
		t.Fatalf("failed to register: %v", err)
	}
	if got, want := registerResp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("register status = %d, want %d", got, want)
	}
	if cerr := registerResp.Body.Close(); cerr != nil {
		t.Errorf("register body close err = %v", cerr)
	}

	// Step 2: GET the quiz view to receive a CSRF token tied to the
	// admin session jar.
	quizViewURL := fmt.Sprintf("%s/admin/quizzes/%d", baseURL, quizID)
	resetToken := fetchCSRFToken(ctx, t, client, quizViewURL)

	resetForm := url.Values{}
	resetForm.Add("csrf_token", resetToken)

	resetURL := fmt.Sprintf("%s/admin/quizzes/%d/players/%d/reset", baseURL, quizID, playerID)
	resetReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, resetURL, strings.NewReader(resetForm.Encode()),
	)
	if err != nil {
		t.Fatalf("failed to build reset request: %v", err)
	}
	resetReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resetResp, err := client.Do(resetReq)
	if err != nil {
		t.Fatalf("failed to POST admin reset: %v", err)
	}
	defer func() {
		if cerr := resetResp.Body.Close(); cerr != nil {
			t.Errorf("reset body close err = %v", cerr)
		}
	}()
	if got, want := resetResp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("admin reset status = %d, want %d", got, want)
	}
	wantLocation := fmt.Sprintf("/admin/quizzes/%d", quizID)
	if got, want := resetResp.Header.Get("Location"), wantLocation; got != want {
		t.Errorf("admin reset Location = %q, want %q", got, want)
	}
}

// integrationSetup bundles the artefacts a gameplay-style integration test
// needs. Context is intentionally returned separately from the struct (passed
// out of setupIntegration as the first return value) to avoid containedctx.
type integrationSetup struct {
	BaseURL string
	Stores  *store.Stores
}

// setupIntegration is a gameplay-flavoured wrapper around startServer that
// opens a *sql.DB against the same dbURI and exposes a store.Stores for
// direct seeding. REGISTRATION_ENABLED is on so the admin-reset portion of
// the test can register the first user (who becomes the admin) and POST to
// /admin/quizzes/.../reset.
func setupIntegration(t *testing.T) (context.Context, integrationSetup) {
	t.Helper()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	db, err := sql.Open("sqlite", srv.DBURI)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v, want nil", cerr)
		}
	})

	return ctx, integrationSetup{
		BaseURL: srv.BaseURL,
		Stores:  store.New(db, slog.Default()),
	}
}

// Subtests share state (cookie jars, gameID, runningScore, completedPlayerID,
// adminClient, freshGameID) and run sequentially by design — earlier scenarios
// produce the player/game rows later ones rely on. Disable paralleltest and
// tparallel so subtests don't call t.Parallel() (which would corrupt the
// shared state).
//
//nolint:paralleltest,tparallel // subtests share state and must run sequentially
func TestGameplay_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
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

	// Shared state across subtests. These are declared in the parent so
	// later subtests can read what earlier subtests produced — the
	// scenarios run sequentially and intentionally share cookie jars,
	// player IDs, and game IDs. New subtests inserted between existing
	// ones must never call t.Parallel() and must use `=` (not `:=`) when
	// assigning these variables; rebinding inside a subtest closure
	// would silently break every later subtest that reads them.
	//
	// Producer / consumer map for these variables:
	//   jar, client       set in "single player can play through to completion";
	//                     read by every later subtest that acts as the player.
	//   gameID            set in "single player ..."; read by "my-game returns
	//                     the completed game ...".
	//   runningScore      set in "single player ..."; read there + in
	//                     "multi-player leaderboard ...".
	//   completedPlayerID set at the end of "single player ..."; read by
	//                     "admin reset clears the played game ...".
	//   adminClient       set in "admin reset ..."; read by every later admin
	//                     POST (question delete, CSRF reject, quiz delete).
	//   freshGameID       set in "admin reset ..."; read by "in-flight game
	//                     answer ...".
	var (
		jar               *cookiejar.Jar
		client            *http.Client
		gameID            string
		runningScore      int
		completedPlayerID int64
		adminClient       *http.Client
		freshGameID       string
	)

	t.Run("play deep-link serves SPA shell", func(t *testing.T) {
		// Deep-link smoke (#157 sec.3): GET /play/{slug}-{id} should rewrite
		// to the SPA shell and return 200 with the player client HTML. Use a
		// throwaway client (no jar) so any accidental cookie write from the
		// static handler does not pollute the player session below.
		deepLinkURL := fmt.Sprintf("%s/play/%s-%d", baseURL, qz.Slug, qz.ID)
		deepLinkResp := httpGet(ctx, t, &http.Client{}, deepLinkURL)
		if got, want := deepLinkResp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("GET /play/{slugID} status = %d, want %d", got, want)
		}
		deepLinkBody, err := io.ReadAll(deepLinkResp.Body)
		if cerr := deepLinkResp.Body.Close(); cerr != nil {
			t.Errorf("deep-link body close err = %v", cerr)
		}
		if err != nil {
			t.Fatalf("failed to read deep-link body: %v", err)
		}
		// The shell handler injects the quiz's title into both <title> and
		// og:title (issue #258). Asserting on the quiz title proves the
		// shell was served AND the per-quiz OG override ran.
		wantTitle := fmt.Sprintf(`<title>%s — Top Banana!</title>`, qz.Title)
		if got := string(deepLinkBody); !strings.Contains(got, wantTitle) {
			t.Errorf(
				"deep-link body should contain %q (proves per-quiz OG override ran), got body of length %d",
				wantTitle,
				len(got),
			)
		}
	})

	t.Run("quizzes API lists the seeded quiz", func(t *testing.T) {
		// Start of the integration test. The cookie jar carries the anonymous
		// session cookie that EnsurePlayer issues on the first request, so
		// every subsequent request is attributed to the same player row.
		var err error
		jar, err = cookiejar.New(nil)
		if err != nil {
			t.Fatalf("failed to create cookie jar: %v", err)
		}
		client = &http.Client{Jar: jar}

		// Get a list of quizzes
		resp := httpGet(ctx, t, client, baseURL+"/api/quizzes")
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("list quizzes status = %d, want %d", got, want)
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
	})

	createGameReq := fmt.Sprintf(`{"quizId": %d}`, qz.ID)

	t.Run("single player can play through to completion", func(t *testing.T) {
		// Create Game
		resp := httpPostJSON(ctx, t, client, baseURL+"/api/games", createGameReq)
		if got, want := resp.StatusCode, http.StatusCreated; got != want {
			t.Fatalf("create game status = %d, want %d", got, want)
		}

		var createGameRes struct {
			ID string `json:"id"`
		}
		err := json.NewDecoder(resp.Body).Decode(&createGameRes)
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("resp.Body.Close err = %v, want nil", cerr)
		}
		if err != nil {
			t.Fatalf("failed to decode create game response: %v", err)
		}
		gameID = createGameRes.ID

		// Walk through questions
		for i := range 3 {
			// Get Next Question
			resp = httpGet(ctx, t, client, fmt.Sprintf("%s/api/games/%s/questions/next", baseURL, gameID))
			if got, want := resp.StatusCode, http.StatusOK; got != want {
				t.Fatalf("next question status = %d, want %d", got, want)
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
			if got, want := resp.StatusCode, http.StatusOK; got != want {
				t.Fatalf("answer status = %d, want %d", got, want)
			}

			var answerRes struct {
				Correct          bool    `json:"correct"`
				Score            int     `json:"score"`
				CorrectOptionIDs []int64 `json:"correctOptionIds"`
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
			// Every question in this fixture has exactly one correct
			// option, so the response must always carry one ID — the
			// client uses it to highlight the correct answer post-pick
			// (#233). Asserting len(...) > 0 is enough; the value
			// itself depends on insertion order.
			if got, want := len(answerRes.CorrectOptionIDs), 1; got != want {
				t.Errorf("answer CorrectOptionIDs len = %d, want %d", got, want)
			}
			runningScore += answerRes.Score
		}

		// Get Results
		resp = httpGet(ctx, t, client, fmt.Sprintf("%s/api/games/%s/results", baseURL, gameID))
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("results status = %d, want %d", got, want)
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
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Fatalf("post-completion next question status = %d, want %d", got, want)
		}
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("resp.Body.Close err = %v, want nil", cerr)
		}

		// Quiz leaderboard: the player who just finished should appear with
		// IsCurrentPlayer=true and the same score they accumulated above.
		leaderboardURL := fmt.Sprintf("%s/api/quizzes/%s-%d/leaderboard", baseURL, qz.Slug, qz.ID)
		resp = httpGet(ctx, t, client, leaderboardURL)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("leaderboard status = %d, want %d", got, want)
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
		completedPlayerID = leaderboard.Entries[0].PlayerID
	})

	leaderboardURL := fmt.Sprintf("%s/api/quizzes/%s-%d/leaderboard", baseURL, qz.Slug, qz.ID)
	myGameURL := fmt.Sprintf("%s/api/quizzes/%s-%d/my-game", baseURL, qz.Slug, qz.ID)

	t.Run("multi-player leaderboard ranks by score and flips IsCurrentPlayer per requester", func(t *testing.T) {
		// Multi-player leaderboard (#157 sec.2): a second player finishes the
		// same quiz with a different (strictly higher) score so we can assert
		// ranking by score descending and per-requester IsCurrentPlayer flags.
		// The first player got Q1+Q3 correct (2/3); player 2 gets all three
		// correct so the totals are unambiguously different.
		jar2, err := cookiejar.New(nil)
		if err != nil {
			t.Fatalf("failed to create second cookie jar: %v", err)
		}
		client2 := &http.Client{Jar: jar2}

		resp := httpPostJSON(ctx, t, client2, baseURL+"/api/games", createGameReq)
		if got, want := resp.StatusCode, http.StatusCreated; got != want {
			t.Fatalf("player2 create game status = %d, want %d", got, want)
		}

		var createGame2Res struct {
			ID string `json:"id"`
		}
		if derr := json.NewDecoder(resp.Body).Decode(&createGame2Res); derr != nil {
			t.Fatalf("failed to decode player2 create-game response: %v", derr)
		}
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("resp.Body.Close err = %v, want nil", cerr)
		}
		gameID2 := createGame2Res.ID

		runningScore2 := 0
		for i := range 3 {
			resp = httpGet(ctx, t, client2, fmt.Sprintf("%s/api/games/%s/questions/next", baseURL, gameID2))
			if got, want := resp.StatusCode, http.StatusOK; got != want {
				t.Fatalf("player2 next question status = %d, want %d", got, want)
			}

			var nextQs2Res nextQuestionRes
			if derr := json.NewDecoder(resp.Body).Decode(&nextQs2Res); derr != nil {
				t.Fatalf("failed to decode player2 next question: %v", derr)
			}
			if cerr := resp.Body.Close(); cerr != nil {
				t.Errorf("resp.Body.Close err = %v, want nil", cerr)
			}

			// Player 2 gets all three questions correct.
			var optionID2 int64
			found2 := false
			for _, q := range qz.Questions {
				if q.ID == nextQs2Res.ID {
					for _, o := range q.Options {
						if o.Correct {
							optionID2 = o.ID
							found2 = true

							break
						}
					}
				}
				if found2 {
					break
				}
			}
			if !found2 {
				t.Fatalf("could not find correct option for player2 question %d", i+1)
			}

			answer2Req := fmt.Sprintf(`{"optionId": %d}`, optionID2)
			answer2URL := fmt.Sprintf("%s/api/games/%s/questions/%d/answers", baseURL, gameID2, nextQs2Res.ID)
			resp = httpPostJSON(ctx, t, client2, answer2URL, answer2Req)
			if got, want := resp.StatusCode, http.StatusOK; got != want {
				t.Fatalf("player2 answer status = %d, want %d", got, want)
			}

			var answer2Res struct {
				Correct bool `json:"correct"`
				Score   int  `json:"score"`
			}
			if derr := json.NewDecoder(resp.Body).Decode(&answer2Res); derr != nil {
				t.Fatalf("failed to decode player2 answer response: %v", derr)
			}
			if cerr := resp.Body.Close(); cerr != nil {
				t.Errorf("resp.Body.Close err = %v, want nil", cerr)
			}
			if got, want := answer2Res.Correct, true; got != want {
				t.Fatalf("player2 Q%d correct = %v, want %v", i+1, got, want)
			}
			runningScore2 += answer2Res.Score
		}

		// Sanity: player2 must strictly out-score player1 for the ranking
		// assertion below to be meaningful.
		if got := runningScore2; got <= runningScore {
			t.Fatalf("runningScore2 = %d, want > runningScore (%d)", got, runningScore)
		}

		// GET leaderboard from player2's session: 2 entries, descending by
		// score (player2 first), player2 flagged IsCurrentPlayer=true.
		resp = httpGet(ctx, t, client2, leaderboardURL)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("player2 leaderboard status = %d, want %d", got, want)
		}

		var leaderboard2 leaderboardRes
		if derr := json.NewDecoder(resp.Body).Decode(&leaderboard2); derr != nil {
			t.Fatalf("failed to decode player2 leaderboard response: %v", derr)
		}
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("resp.Body.Close err = %v, want nil", cerr)
		}

		if got, want := len(leaderboard2.Entries), 2; got != want {
			t.Fatalf("len(leaderboard2.Entries) = %d, want %d", got, want)
		}
		if got, want := leaderboard2.Entries[0].Score, runningScore2; got != want {
			t.Errorf("leaderboard2.Entries[0].Score = %d, want %d (player2 should rank first)", got, want)
		}
		if got, want := leaderboard2.Entries[1].Score, runningScore; got != want {
			t.Errorf("leaderboard2.Entries[1].Score = %d, want %d (player1 should rank second)", got, want)
		}
		if got, want := leaderboard2.Entries[0].IsCurrentPlayer, true; got != want {
			t.Errorf("leaderboard2.Entries[0].IsCurrentPlayer = %v, want %v (requester is player2)", got, want)
		}
		if got, want := leaderboard2.Entries[1].IsCurrentPlayer, false; got != want {
			t.Errorf("leaderboard2.Entries[1].IsCurrentPlayer = %v, want %v (player1 is not the requester)", got, want)
		}
		if got := leaderboard2.Entries[0].PlayerID; got <= 0 {
			t.Errorf("leaderboard2.Entries[0].PlayerID = %d, want > 0", got)
		}
		if got := leaderboard2.Entries[1].PlayerID; got <= 0 {
			t.Errorf("leaderboard2.Entries[1].PlayerID = %d, want > 0", got)
		}
		if leaderboard2.Entries[0].PlayerID == leaderboard2.Entries[1].PlayerID {
			t.Errorf("leaderboard2 entries have same PlayerID %d, want distinct", leaderboard2.Entries[0].PlayerID)
		}
		if got, want := leaderboard2.Entries[1].PlayerID, completedPlayerID; got != want {
			t.Errorf("leaderboard2.Entries[1].PlayerID = %d, want %d (player1)", got, want)
		}

		// Re-fetch leaderboard from player1's session: same order (score is
		// the only sort key) but the IsCurrentPlayer flags flip.
		resp = httpGet(ctx, t, client, leaderboardURL)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("player1 re-fetch leaderboard status = %d, want %d", got, want)
		}

		var leaderboard1Again leaderboardRes
		if derr := json.NewDecoder(resp.Body).Decode(&leaderboard1Again); derr != nil {
			t.Fatalf("failed to decode player1 re-fetch leaderboard response: %v", derr)
		}
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("resp.Body.Close err = %v, want nil", cerr)
		}

		if got, want := len(leaderboard1Again.Entries), 2; got != want {
			t.Fatalf("len(leaderboard1Again.Entries) = %d, want %d", got, want)
		}
		if got, want := leaderboard1Again.Entries[0].Score, runningScore2; got != want {
			t.Errorf("leaderboard1Again.Entries[0].Score = %d, want %d", got, want)
		}
		if got, want := leaderboard1Again.Entries[1].Score, runningScore; got != want {
			t.Errorf("leaderboard1Again.Entries[1].Score = %d, want %d", got, want)
		}
		if got, want := leaderboard1Again.Entries[0].IsCurrentPlayer, false; got != want {
			t.Errorf(
				"leaderboard1Again.Entries[0].IsCurrentPlayer = %v, want %v (player2 is not the requester)",
				got,
				want,
			)
		}
		if got, want := leaderboard1Again.Entries[1].IsCurrentPlayer, true; got != want {
			t.Errorf("leaderboard1Again.Entries[1].IsCurrentPlayer = %v, want %v (requester is player1)", got, want)
		}
		if got, want := leaderboard1Again.Entries[1].PlayerID, completedPlayerID; got != want {
			t.Errorf("leaderboard1Again.Entries[1].PlayerID = %d, want %d (player1)", got, want)
		}
	})

	t.Run("my-game returns the completed game and second-attempt is rejected", func(t *testing.T) {
		// One-attempt-per-quiz: GET /my-game now reports the finished game
		// with completed=true.
		resp := httpGet(ctx, t, client, myGameURL)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("GET /my-game status = %d, want %d", got, want)
		}

		var myGameRes struct {
			GameID    string `json:"gameId"`
			Completed bool   `json:"completed"`
		}
		if derr := json.NewDecoder(resp.Body).Decode(&myGameRes); derr != nil {
			t.Fatalf("failed to decode my-game response: %v", derr)
		}
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("resp.Body.Close err = %v, want nil", cerr)
		}
		if got, want := myGameRes.GameID, gameID; got != want {
			t.Errorf("my-game GameID = %q, want %q", got, want)
		}
		if got, want := myGameRes.Completed, true; got != want {
			t.Errorf("my-game Completed = %v, want %v", got, want)
		}

		// A second POST /api/games for the same player + quiz must be
		// rejected with 409 — the frontend should have called my-game first.
		resp = httpPostJSON(ctx, t, client, baseURL+"/api/games", createGameReq)
		if got, want := resp.StatusCode, http.StatusConflict; got != want {
			t.Fatalf("second create game status = %d, want %d", got, want)
		}
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("resp.Body.Close err = %v, want nil", cerr)
		}
	})

	t.Run("admin reset clears the played game and unblocks replay", func(t *testing.T) {
		// Admin reset (drive via the HTTP route, with CSRF token from the
		// admin form). Use a separate jar / client so the admin's own session
		// does not interfere with the player flow above.
		adminClient = &http.Client{
			Jar: func() *cookiejar.Jar {
				j, jerr := cookiejar.New(nil)
				if jerr != nil {
					t.Fatalf("failed to create admin cookie jar: %v", jerr)
				}

				return j
			}(),
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		registerAdminAndResetPlayer(ctx, t, adminClient, baseURL, qz.ID, completedPlayerID)

		// After reset, GET /my-game returns 404 — no game for this (player, quiz).
		resp := httpGet(ctx, t, client, myGameURL)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Fatalf("after reset, GET /my-game status = %d, want %d", got, want)
		}
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("resp.Body.Close err = %v, want nil", cerr)
		}

		// And the player can now POST /api/games again to start fresh.
		resp = httpPostJSON(ctx, t, client, baseURL+"/api/games", createGameReq)
		if got, want := resp.StatusCode, http.StatusCreated; got != want {
			t.Fatalf("after reset, create game status = %d, want %d", got, want)
		}

		var freshGameRes struct {
			ID string `json:"id"`
		}
		if derr := json.NewDecoder(resp.Body).Decode(&freshGameRes); derr != nil {
			t.Fatalf("failed to decode fresh create-game response: %v", derr)
		}
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("resp.Body.Close err = %v, want nil", cerr)
		}
		if got := freshGameRes.ID; got == "" {
			t.Error("fresh game ID is empty, want non-empty")
		}
		if freshGameRes.ID == gameID {
			t.Errorf("fresh game ID %q equals old game ID %q, want a new ID", freshGameRes.ID, gameID)
		}
		freshGameID = freshGameRes.ID
	})

	t.Run("anonymous admin requests redirect to login", func(t *testing.T) {
		// Auth gate: an unauthenticated client requesting an /admin route is
		// redirected to /login with 303, not allowed through to render the
		// admin page. A throwaway client with no jar and no auto-redirect so
		// we can assert on the redirect itself.
		anonClient := &http.Client{
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		anonResp := httpGet(ctx, t, anonClient, baseURL+"/admin/quizzes")
		if got, want := anonResp.StatusCode, http.StatusSeeOther; got != want {
			t.Errorf("anon /admin/quizzes status = %d, want %d", got, want)
		}
		if got, want := anonResp.Header.Get("Location"), "/login"; got != want {
			t.Errorf("anon /admin/quizzes Location = %q, want %q", got, want)
		}
		if cerr := anonResp.Body.Close(); cerr != nil {
			t.Errorf("anonResp body close err = %v", cerr)
		}
	})

	t.Run("in-flight game answer surfaces in my-game with completed=false", func(t *testing.T) {
		// Regression for #155: admin delete of a played quiz must not 500.
		// Answer one question on the fresh game first so game_questions and
		// game_answers both have rows referencing the quiz at the moment the
		// delete fires. Without QuizStore's in-Go cascade those rows would
		// trigger a FOREIGN KEY constraint failure and surface as a 500.
		resp := httpGet(ctx, t, client, fmt.Sprintf("%s/api/games/%s/questions/next", baseURL, freshGameID))
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("fresh next question status = %d, want %d", got, want)
		}

		var freshQs nextQuestionRes
		if derr := json.NewDecoder(resp.Body).Decode(&freshQs); derr != nil {
			t.Fatalf("failed to decode fresh next question: %v", derr)
		}
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("resp.Body.Close err = %v, want nil", cerr)
		}

		freshAnswerURL := fmt.Sprintf("%s/api/games/%s/questions/%d/answers", baseURL, freshGameID, freshQs.ID)
		freshAnswerBody := fmt.Sprintf(`{"optionId": %d}`, freshQs.Options[0].ID)
		resp = httpPostJSON(ctx, t, client, freshAnswerURL, freshAnswerBody)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("fresh answer status = %d, want %d", got, want)
		}
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("resp.Body.Close err = %v, want nil", cerr)
		}

		// /my-game during an in-flight game: the player has answered one
		// question but not finished, so the response is 200 with the
		// in-flight game ID and completed=false. The player client uses
		// this to skip the start-game button and resume.
		resp = httpGet(ctx, t, client, myGameURL)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("in-flight /my-game status = %d, want %d", got, want)
		}

		var inFlightMyGame struct {
			GameID    string `json:"gameId"`
			Completed bool   `json:"completed"`
		}
		if derr := json.NewDecoder(resp.Body).Decode(&inFlightMyGame); derr != nil {
			t.Fatalf("failed to decode in-flight my-game response: %v", derr)
		}
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("resp.Body.Close err = %v, want nil", cerr)
		}
		if got, want := inFlightMyGame.GameID, freshGameID; got != want {
			t.Errorf("in-flight my-game GameID = %q, want %q", got, want)
		}
		if got, want := inFlightMyGame.Completed, false; got != want {
			t.Errorf("in-flight my-game Completed = %v, want %v", got, want)
		}
	})

	t.Run("admin can delete a played question without FK 787", func(t *testing.T) {
		// Regression for #157 sec.1: admin delete of a played question must not
		// 500. The fresh game above answered question[0], so game_questions and
		// game_answers both have rows referencing that question at the moment
		// the delete fires. Without the in-Go cascade in execDeleteQuestion the
		// FK on game_questions.question_id would raise FOREIGN KEY constraint
		// failed (787) and the admin route would surface it as a 500.
		questionDeleteToken := fetchCSRFToken(
			ctx, t, adminClient, fmt.Sprintf("%s/admin/quizzes/%d", baseURL, qz.ID),
		)
		questionDeleteForm := url.Values{}
		questionDeleteForm.Add("csrf_token", questionDeleteToken)
		questionDeleteURL := fmt.Sprintf(
			"%s/admin/quizzes/%d/questions/%d/delete", baseURL, qz.ID, qz.Questions[0].ID,
		)
		questionDeleteReq, err := http.NewRequestWithContext(
			ctx, http.MethodPost, questionDeleteURL, strings.NewReader(questionDeleteForm.Encode()),
		)
		if err != nil {
			t.Fatalf("failed to build question delete request: %v", err)
		}
		questionDeleteReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		questionDeleteResp, err := adminClient.Do(questionDeleteReq)
		if err != nil {
			t.Fatalf("failed to POST question delete: %v", err)
		}
		if got, want := questionDeleteResp.StatusCode, http.StatusSeeOther; got != want {
			t.Fatalf(
				"question delete status = %d, want %d (500 here means the FK cascade for question delete regressed)",
				got, want,
			)
		}
		wantQuestionDeleteLocation := fmt.Sprintf("/admin/quizzes/%d", qz.ID)
		if got, want := questionDeleteResp.Header.Get("Location"), wantQuestionDeleteLocation; got != want {
			t.Errorf("question delete Location = %q, want %q", got, want)
		}
		if cerr := questionDeleteResp.Body.Close(); cerr != nil {
			t.Errorf("question delete body close err = %v", cerr)
		}
	})

	t.Run("CSRF middleware rejects POSTs missing or carrying bad tokens", func(t *testing.T) {
		// CSRF: a POST to a state-changing admin route without the
		// csrf_token form field is rejected by the CSRF middleware before
		// the handler runs. The middleware sits in front of requireAdmin,
		// so the response is 403 (not a 303 to /login). Use a body with no
		// csrf_token field so the cookie is present but the form value is
		// missing.
		csrfRejectURL := fmt.Sprintf("%s/admin/quizzes/%d/delete", baseURL, qz.ID)
		csrfRejectReq, err := http.NewRequestWithContext(
			ctx, http.MethodPost, csrfRejectURL, strings.NewReader(""),
		)
		if err != nil {
			t.Fatalf("failed to build CSRF-reject request: %v", err)
		}
		csrfRejectReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		csrfRejectResp, err := adminClient.Do(csrfRejectReq)
		if err != nil {
			t.Fatalf("failed to POST CSRF-reject request: %v", err)
		}
		if got, want := csrfRejectResp.StatusCode, http.StatusForbidden; got != want {
			t.Errorf("CSRF-less admin delete status = %d, want %d", got, want)
		}
		if cerr := csrfRejectResp.Body.Close(); cerr != nil {
			t.Errorf("csrfReject body close err = %v", cerr)
		}

		// CSRF: a forged token (cookie present, form value present but
		// mismatched) is also rejected. Different code path from the
		// missing-field case above — the cookie HMAC verification fails
		// rather than the form-value lookup.
		badTokenForm := url.Values{}
		badTokenForm.Add("csrf_token", "not-a-real-token")
		badTokenReq, err := http.NewRequestWithContext(
			ctx, http.MethodPost, csrfRejectURL, strings.NewReader(badTokenForm.Encode()),
		)
		if err != nil {
			t.Fatalf("failed to build bad-token request: %v", err)
		}
		badTokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		badTokenResp, err := adminClient.Do(badTokenReq)
		if err != nil {
			t.Fatalf("failed to POST bad-token request: %v", err)
		}
		if got, want := badTokenResp.StatusCode, http.StatusForbidden; got != want {
			t.Errorf("bad-token admin delete status = %d, want %d", got, want)
		}
		if cerr := badTokenResp.Body.Close(); cerr != nil {
			t.Errorf("badToken body close err = %v", cerr)
		}
	})

	t.Run("admin can delete a played quiz and clears it from the listing", func(t *testing.T) {
		// Admin POSTs /admin/quizzes/{id}/delete with a CSRF token tied to
		// the admin session jar created by registerAdminAndResetPlayer above.
		quizDetailURL := fmt.Sprintf("%s/admin/quizzes/%d", baseURL, qz.ID)
		deleteToken := fetchCSRFToken(ctx, t, adminClient, quizDetailURL)

		deleteForm := url.Values{}
		deleteForm.Add("csrf_token", deleteToken)

		deleteURL := fmt.Sprintf("%s/admin/quizzes/%d/delete", baseURL, qz.ID)
		deleteReq, err := http.NewRequestWithContext(
			ctx, http.MethodPost, deleteURL, strings.NewReader(deleteForm.Encode()),
		)
		if err != nil {
			t.Fatalf("failed to build admin delete request: %v", err)
		}
		deleteReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		deleteResp, err := adminClient.Do(deleteReq)
		if err != nil {
			t.Fatalf("failed to POST admin delete: %v", err)
		}
		if got, want := deleteResp.StatusCode, http.StatusSeeOther; got != want {
			t.Fatalf("admin delete status = %d, want %d (500 here means the FK cascade regressed)", got, want)
		}
		if got, want := deleteResp.Header.Get("Location"), "/admin/quizzes"; got != want {
			t.Errorf("admin delete Location = %q, want %q", got, want)
		}
		if cerr := deleteResp.Body.Close(); cerr != nil {
			t.Errorf("delete body close err = %v", cerr)
		}

		// /api/quizzes no longer lists the deleted quiz.
		resp := httpGet(ctx, t, client, baseURL+"/api/quizzes")
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("post-delete /api/quizzes status = %d, want %d", got, want)
		}

		var afterDelete []struct {
			Title string `json:"title"`
		}
		if derr := json.NewDecoder(resp.Body).Decode(&afterDelete); derr != nil {
			t.Fatalf("failed to decode quizzes after delete: %v", derr)
		}
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("resp.Body.Close err = %v, want nil", cerr)
		}
		if got, want := len(afterDelete), 0; got != want {
			t.Errorf("quizzes after delete len = %d, want %d", got, want)
		}
	})
}
