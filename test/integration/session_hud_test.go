package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// sessionHUDQuestion decodes the question fields the live answer-pad HUD reads:
// the 1-indexed position within the quiz and the quiz's total question count
// (#956), alongside the id/options the test drives answers off.
type sessionHUDQuestion struct {
	ID       int64              `json:"id"`
	Position int                `json:"position"`
	Total    int                `json:"total"`
	Options  []sessionRunnerOpt `json:"options"`
}

// sessionHUDSelf decodes the viewer's own per-game state: their running score
// (#956), which the HUD shows during a question.
type sessionHUDSelf struct {
	Score int `json:"score"`
}

// sessionHUDStateRes decodes the HUD-relevant slice of the state DTO.
type sessionHUDStateRes struct {
	Phase    string              `json:"phase"`
	Question *sessionHUDQuestion `json:"question"`
	Self     *sessionHUDSelf     `json:"self"`
}

// getHUDState reads GET /state into the HUD-aware decode target.
func getHUDState(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, code string,
) sessionHUDStateRes {
	t.Helper()
	resp := httpGet(ctx, t, client, fmt.Sprintf("%s/api/sessions/%s/state", baseURL, code))
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("state status = %d, want %d", got, want)
	}
	var state sessionHUDStateRes
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatalf("decode HUD state: %v", err)
	}

	return state
}

// TestSessionState_HUDPositionTotalAndSelfScore drives a hosted session through
// its first question and asserts the answer-pad HUD wire (#956): the question
// carries its 1-indexed position and the quiz's total question count, the
// in-lobby read carries no self block, and once the question is scored the
// viewer's self.score reflects the points they earned. The scorer (Ace) picks
// the correct option; the slacker (Bee) picks wrong, so their self scores
// diverge deterministically.
func TestSessionState_HUDPositionTotalAndSelfScore(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegrationWithEnv(t, map[string]string{
		"SESSION_RUNNER_BEAT": "250ms",
		"REVEAL_DELAY":        "200ms",
	})
	baseURL := setup.BaseURL

	// The multi-round fixture has three questions across two rounds, so total is
	// 3 and the first question's position is 1.
	qz := seedMultiRoundLiveQuiz(ctx, t, setup.Stores.Quizzes, "hud")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "hud-host", "hud-host-pass-123")
	code := createSession(ctx, t, host, baseURL, qz.ID)

	ace := newAnonClient(t)
	bee := newAnonClient(t)
	joinSession(ctx, t, ace, baseURL, code, "Ace")
	joinSession(ctx, t, bee, baseURL, code, "Bee")

	// In the lobby no game has scored yet, so the self block is omitted.
	lobby := getHUDState(ctx, t, ace, baseURL, code)
	if got, want := lobby.Phase, "lobby"; got != want {
		t.Fatalf("pre-start phase = %q, want %q", got, want)
	}
	if lobby.Self != nil {
		t.Errorf("lobby self = %+v, want nil (no game scored yet)", lobby.Self)
	}

	startSession(ctx, t, host, baseURL, code)

	// During the first question the HUD chips read off the question block: it is
	// question 1 of the quiz's 3 total questions.
	waitForResultsPhase(ctx, t, ace, baseURL, code, "question")
	hud := getHUDState(ctx, t, ace, baseURL, code)
	if hud.Question == nil {
		t.Fatal("question phase carries no question for the HUD")
	}
	if got, want := hud.Question.Position, 1; got != want {
		t.Errorf("question position = %d, want %d", got, want)
	}
	if got, want := hud.Question.Total, 3; got != want {
		t.Errorf("question total = %d, want %d", got, want)
	}
	// Before scoring (mid-question) the viewer's running score is 0.
	if hud.Self == nil {
		t.Fatal("question phase carries no self block for the HUD")
	}
	if got, want := hud.Self.Score, 0; got != want {
		t.Errorf("pre-answer self score = %d, want %d", got, want)
	}

	// Both answer the same question (Ace correct, Bee wrong); all-answered closes
	// it early and the runner scores it.
	playQuestion(ctx, t, ace, bee, baseURL, code)

	// After the question is scored, Ace's self.score reflects the points earned;
	// Bee, who picked wrong, stays at 0.
	aceScore := waitForSelfScore(ctx, t, ace, baseURL, code)
	if aceScore <= 0 {
		t.Errorf("Ace self score after scoring = %d, want > 0", aceScore)
	}
	beeState := getHUDState(ctx, t, bee, baseURL, code)
	if beeState.Self == nil {
		t.Fatal("post-question state carries no self block for Bee")
	}
	if got, want := beeState.Self.Score, 0; got != want {
		t.Errorf("Bee self score = %d, want %d (picked the wrong option)", got, want)
	}

	// Finish round 1's second question so the round_results board appears, then
	// confirm Ace's self.score equals their standings total - the HUD reads the
	// same per-game aggregation the board does.
	playQuestion(ctx, t, ace, bee, baseURL, code)
	rr := waitForResultsPhase(ctx, t, ace, baseURL, code, "round_results")
	aceID := playerIDFromState(ctx, t, ace, baseURL, code, "Ace")
	aceStanding := findStanding(t, rr.Standings, aceID)
	final := getHUDState(ctx, t, ace, baseURL, code)
	if final.Self == nil {
		t.Fatal("round_results state carries no self block for Ace")
	}
	if got, want := final.Self.Score, aceStanding.TotalScore; got != want {
		t.Errorf("Ace self score = %d, want %d (matches standings total)", got, want)
	}
}

// waitForSelfScore polls GET /state until the viewer's self.score becomes
// positive, returning it. The runner scores a question on a later beat than the
// early close, so the score lands after the question phase ends.
func waitForSelfScore(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, code string,
) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		state := getHUDState(ctx, t, client, baseURL, code)
		if state.Self != nil && state.Self.Score > 0 {
			return state.Self.Score
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("viewer self score never became positive")

	return 0
}
