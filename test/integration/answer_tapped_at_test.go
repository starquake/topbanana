package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"testing"
	"time"

	"github.com/starquake/topbanana/cmd/server/app"
	"github.com/starquake/topbanana/internal/quiz"
)

// TestAnswer_TappedAtClamp pins the #237 wire contract end-to-end: an
// out-of-window tappedAt does not poison the recorded score. Without
// the service-side clamp, a tappedAt one hour past expiredAt would land
// the answer in the "too late" branch of CalculateScore and return 0
// even though the player tapped on time. With the clamp the recorded
// AnsweredAt falls back to serverNow, so the score is computed against
// the actual server-side window.
func TestAnswer_TappedAtClamp(t *testing.T) {
	t.Parallel()

	ctx, g := startSingleQuestionGame(t)

	// One hour past the question window - without the service-side
	// clamp this would record AnsweredAt > ExpiredAt and CalculateScore
	// would return 0 even though the option is correct.
	tappedAt := g.question.StartedAt.Add(1 * time.Hour).Format(time.RFC3339Nano)
	answerReq := fmt.Sprintf(`{"optionId": %d, "tappedAt": %q}`, g.correctOptionID, tappedAt)
	answerResp := httpPostJSON(ctx, t, g.client, g.answerURL(), answerReq)
	defer closeBody(t, answerResp.Body)
	if got, want := answerResp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("answer status = %d, want %d", got, want)
	}

	var answerRes struct {
		Correct bool `json:"correct"`
		Score   int  `json:"score"`
	}
	if derr := json.NewDecoder(answerResp.Body).Decode(&answerRes); derr != nil {
		t.Fatalf("decode answer response: %v", derr)
	}
	if got, want := answerRes.Correct, true; got != want {
		t.Errorf("Correct = %v, want %v", got, want)
	}
	// The service-side clamp falls back to serverNow, which is inside
	// the window, so the correct option earns a non-zero score.
	if got, want := answerRes.Score, 0; got == want {
		t.Errorf("Score = %v, want a non-zero score (clamp should have rescued the future tappedAt)", got)
	}
}

// singleQuestionGame is a solo game on a one-question quiz whose question has
// been issued.
type singleQuestionGame struct {
	client          *http.Client
	baseURL         string
	gameID          string
	question        nextQuestionRes
	correctOptionID int64
}

// answerURL is the answer-post endpoint for the issued question.
func (g singleQuestionGame) answerURL() string {
	return fmt.Sprintf("%s/api/games/%s/questions/%d/answers", g.baseURL, g.gameID, g.question.ID)
}

// startSingleQuestionGame seeds a one-question quiz, starts a solo game on it
// and issues the question.
func startSingleQuestionGame(t *testing.T, runOpts ...app.Option) (context.Context, singleQuestionGame) {
	t.Helper()

	ctx, setup := setupIntegrationWithEnv(t, nil, runOpts...)
	baseURL := setup.BaseURL
	stores := setup.Stores

	qz := &quiz.Quiz{
		Title:             "Tapped-at Clamp Quiz",
		Published:         true,
		Slug:              "tapped-at-clamp-quiz",
		Description:       "single-question fixture for #237 integration coverage",
		CreatedByPlayerID: seededAdminID,
		Questions: []*quiz.Question{
			{
				Text:     "Q1",
				Position: 1,
				Options: []*quiz.Option{
					{Text: "Yes", Correct: true},
					{Text: "No"},
				},
			},
		},
	}
	if err := stores.Quizzes.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}
	correctOptionID := qz.Questions[0].Options[0].ID

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	client := &http.Client{Jar: jar}

	createReq := fmt.Sprintf(`{"quizId": %d}`, qz.ID)
	createResp := httpPostJSON(ctx, t, client, baseURL+"/api/games", createReq)
	defer closeBody(t, createResp.Body)
	if got, want := createResp.StatusCode, http.StatusCreated; got != want {
		t.Fatalf("create game status = %d, want %d", got, want)
	}
	var createRes struct {
		ID string `json:"id"`
	}
	if derr := json.NewDecoder(createResp.Body).Decode(&createRes); derr != nil {
		t.Fatalf("decode create game: %v", derr)
	}
	gameID := createRes.ID

	nextResp := httpGet(ctx, t, client, fmt.Sprintf("%s/api/games/%s/questions/next", baseURL, gameID))
	defer closeBody(t, nextResp.Body)
	if got, want := nextResp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("next question status = %d, want %d", got, want)
	}
	var nextQ nextQuestionRes
	if derr := json.NewDecoder(nextResp.Body).Decode(&nextQ); derr != nil {
		t.Fatalf("decode next question: %v", derr)
	}

	return ctx, singleQuestionGame{
		client:          client,
		baseURL:         baseURL,
		gameID:          gameID,
		question:        nextQ,
		correctOptionID: correctOptionID,
	}
}

// TestAnswer_RejectedDuringReadBeat pins #1337 end-to-end: an answer posted
// before the question's startedAt is rejected with 409 instead of scoring.
func TestAnswer_RejectedDuringReadBeat(t *testing.T) {
	t.Parallel()

	ctx, g := startSingleQuestionGame(t, app.WithSoloRevealDelay(time.Hour))

	answerReq := fmt.Sprintf(`{"optionId": %d}`, g.correctOptionID)
	answerResp := httpPostJSON(ctx, t, g.client, g.answerURL(), answerReq)
	defer closeBody(t, answerResp.Body)
	if got, want := answerResp.StatusCode, http.StatusConflict; got != want {
		t.Errorf("answer status = %d, want %d", got, want)
	}
}
