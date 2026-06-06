package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// httpPostEmpty issues a POST with no body and no JSON content-type,
// mirroring the navigator.sendBeacon the player client fires on tab close:
// the leave endpoint must accept it without requiring a JSON body.
func httpPostEmpty(ctx context.Context, t *testing.T, client *http.Client, target string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}

	return resp
}

// TestSessionLeave_DropsFromRoster drives the MP-10 leave loop end to end: two
// players join, one leaves via a bodyless POST (the sendBeacon shape), the
// leave returns 204, and the next GET /state shows the roster without the left
// player.
func TestSessionLeave_DropsFromRoster(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "leave-roster")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "leave-host", "leave-host-pass-123")

	code := createSession(ctx, t, host, baseURL, qz.ID)

	alice := newAnonClient(t)
	bob := newAnonClient(t)
	joinSession(ctx, t, alice, baseURL, code, "Alice")
	joinSession(ctx, t, bob, baseURL, code, "Bob")

	// Both are present before anyone leaves.
	before := getSessionState(ctx, t, bob, baseURL, code)
	if got, want := len(before.Players), 2; got != want {
		t.Fatalf("len(players) before leave = %d, want %d", got, want)
	}

	// Alice leaves via a bodyless POST (the sendBeacon shape); expect a 204.
	leaveResp := httpPostEmpty(ctx, t, alice, fmt.Sprintf("%s/api/sessions/%s/leave", baseURL, code))
	defer closeBody(t, leaveResp.Body)
	if got, want := leaveResp.StatusCode, http.StatusNoContent; got != want {
		t.Fatalf("leave status = %d, want %d", got, want)
	}

	// Bob's next state read no longer lists Alice.
	after := getSessionState(ctx, t, bob, baseURL, code)
	if got, want := len(after.Players), 1; got != want {
		t.Fatalf("len(players) after leave = %d, want %d", got, want)
	}
	if got, want := after.Players[0].DisplayName, "Bob"; got != want {
		t.Errorf("remaining player = %q, want %q", got, want)
	}

	// Alice has left, so she is no longer a participant: her own state read 404s.
	goneResp := httpGet(ctx, t, alice, fmt.Sprintf("%s/api/sessions/%s/state", baseURL, code))
	defer closeBody(t, goneResp.Body)
	if got, want := goneResp.StatusCode, http.StatusNotFound; got != want {
		t.Errorf("left player state status = %d, want %d", got, want)
	}

	// A repeat leave from Alice is now a 404 (her active row is already gone).
	repeatResp := httpPostEmpty(ctx, t, alice, fmt.Sprintf("%s/api/sessions/%s/leave", baseURL, code))
	defer closeBody(t, repeatResp.Body)
	if got, want := repeatResp.StatusCode, http.StatusNotFound; got != want {
		t.Errorf("repeat leave status = %d, want %d", got, want)
	}
}

// TestSessionLeave_DropsFromAnsweredBadges pins MP-10 on the answered-order
// badges: a player who answered then left disappears from answeredPlayerIds in
// the live question phase, even though the runner still has their recorded pick
// (so their score, and thus their quiz-leaderboard contribution, survives).
func TestSessionLeave_DropsFromAnsweredBadges(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegrationWithEnv(t, map[string]string{"SESSION_RUNNER_BEAT": "250ms"})
	baseURL := setup.BaseURL
	qz := seedRunnerLiveQuiz(ctx, t, setup.Stores.Quizzes, "leave-badges")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "leave-badges-host", "leave-badges-pass-123")
	code := createSession(ctx, t, host, baseURL, qz.ID)

	leaver := newAnonClient(t)
	stayer := newAnonClient(t)
	joinSession(ctx, t, leaver, baseURL, code, "Leaver")
	joinSession(ctx, t, stayer, baseURL, code, "Stayer")

	startSession(ctx, t, host, baseURL, code)

	// The runner issues the question. The leaver answers; the stayer holds back
	// so the question stays open (one active player still owes an answer).
	state := waitForPhase(ctx, t, stayer, baseURL, code, "question")
	if state.Question == nil {
		t.Fatal("question phase has no question in state")
	}
	pick := state.Question.Options[0].ID
	answerSession(ctx, t, leaver, baseURL, code, pick, http.StatusNoContent)

	// The leaver leaves; their badge must drop from the still-open question.
	leaveResp := httpPostEmpty(ctx, t, leaver, fmt.Sprintf("%s/api/sessions/%s/leave", baseURL, code))
	defer closeBody(t, leaveResp.Body)
	if got, want := leaveResp.StatusCode, http.StatusNoContent; got != want {
		t.Fatalf("leave status = %d, want %d", got, want)
	}

	open := getRunnerState(ctx, t, stayer, baseURL, code)
	if got, want := open.Phase, "question"; got != want {
		t.Fatalf("phase = %q, want %q (stayer still owes an answer)", got, want)
	}
	if got, want := len(open.Question.AnsweredPlayerIDs), 0; got != want {
		t.Errorf("answeredPlayerIds after leaver left = %d, want %d (the leaver dropped)", got, want)
	}
	if got, want := len(open.Players), 1; got != want {
		t.Errorf("roster after leave = %d, want %d (only the stayer)", got, want)
	}
}

// TestSessionLeave_StrangerIs404 pins that a player who never joined gets a
// 404 from the leave endpoint, keeping the join code opaque to outsiders like
// the other session gates.
func TestSessionLeave_StrangerIs404(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "leave-stranger")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "leave-stranger-host", "leave-stranger-pass-123")

	code := createSession(ctx, t, host, baseURL, qz.ID)

	stranger := newAnonClient(t)
	resp := httpPostEmpty(ctx, t, stranger, fmt.Sprintf("%s/api/sessions/%s/leave", baseURL, code))
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusNotFound; got != want {
		t.Errorf("stranger leave status = %d, want %d", got, want)
	}
}
