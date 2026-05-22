//go:build integration

package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"testing"
	"time"

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

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	stores := setup.Stores

	qz := &quiz.Quiz{
		Title:             "Tapped-at Clamp Quiz",
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

	// One hour past the question window — without the service-side
	// clamp this would record AnsweredAt > ExpiredAt and CalculateScore
	// would return 0 even though the option is correct.
	tappedAt := nextQ.StartedAt.Add(1 * time.Hour).Format(time.RFC3339Nano)
	answerReq := fmt.Sprintf(`{"optionId": %d, "tappedAt": %q}`, correctOptionID, tappedAt)
	answerURL := fmt.Sprintf("%s/api/games/%s/questions/%d/answers", baseURL, gameID, nextQ.ID)
	answerResp := httpPostJSON(ctx, t, client, answerURL, answerReq)
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
