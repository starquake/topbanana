package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/quiz"
)

// sessionRunnerStateRes decodes the in-game fields the runner adds to the
// frozen state DTO (MP-5 / #682). It carries only what the runner tests
// assert; the lobby fields are covered by session_lobby_test.
type sessionRunnerStateRes struct {
	Phase     string                  `json:"phase"`
	Question  *sessionRunnerQuestion  `json:"question"`
	Players   []sessionStatePlayerRes `json:"players"`
	ServerNow time.Time               `json:"serverNow"`
}

type sessionRunnerQuestion struct {
	ID                int64              `json:"id"`
	Options           []sessionRunnerOpt `json:"options"`
	StartedAt         *time.Time         `json:"startedAt"`
	ExpiresAt         *time.Time         `json:"expiresAt"`
	AnsweredPlayerIDs []int64            `json:"answeredPlayerIds"`
	Answers           []sessionRunnerAns `json:"answers"`
	CorrectOptionIDs  []int64            `json:"correctOptionIds"`
}

type sessionRunnerOpt struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
}

type sessionRunnerAns struct {
	PlayerID int64 `json:"playerId"`
	Correct  *bool `json:"correct"`
	Score    *int  `json:"score"`
}

// getRunnerState reads GET /state into the runner-aware decode target.
func getRunnerState(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, code string,
) sessionRunnerStateRes {
	t.Helper()
	resp := httpGet(ctx, t, client, fmt.Sprintf("%s/api/sessions/%s/state", baseURL, code))
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("state status = %d, want %d", got, want)
	}
	var state sessionRunnerStateRes
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatalf("decode state: %v", err)
	}

	return state
}

// waitForPhase polls GET /state until the session reaches want or the deadline
// passes, returning the matching state. The runner advances on its own beat
// (shrunk via SESSION_RUNNER_BEAT), so the test waits for the transition
// rather than sleeping a fixed amount.
func waitForPhase(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, code, want string,
) sessionRunnerStateRes {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last sessionRunnerStateRes
	for time.Now().Before(deadline) {
		last = getRunnerState(ctx, t, client, baseURL, code)
		if last.Phase == want {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("session never reached phase %q; last phase %q", want, last.Phase)

	return last
}

// waitForAnswersOpen polls GET /state until the question's answer window has
// opened (serverNow at or after startedAt), returning the matching state. The
// answer window opens after the read beat, so a poll before startedAt is still
// in the read beat; the helper waits for the beat to elapse rather than
// sleeping a fixed amount.
func waitForAnswersOpen(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, code string,
) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last sessionRunnerStateRes
	for time.Now().Before(deadline) {
		last = getRunnerState(ctx, t, client, baseURL, code)
		if last.Question != nil && last.Question.StartedAt != nil && !last.ServerNow.Before(*last.Question.StartedAt) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("question answer window never opened; last phase %q", last.Phase)
}

// startSession posts the host Start and asserts the 204.
func startSession(ctx context.Context, t *testing.T, client *http.Client, baseURL, code string) {
	t.Helper()
	resp := httpPostJSON(ctx, t, client, fmt.Sprintf("%s/api/sessions/%s/start", baseURL, code), `{}`)
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusNoContent; got != want {
		t.Fatalf("start status = %d, want %d", got, want)
	}
}

// answerSession posts a pick for the calling client and asserts the status.
func answerSession(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, code string, optionID int64, wantStatus int,
) {
	t.Helper()
	body := fmt.Sprintf(`{"optionId": %d}`, optionID)
	resp := httpPostJSON(ctx, t, client, fmt.Sprintf("%s/api/sessions/%s/answer", baseURL, code), body)
	defer closeBody(t, resp.Body)
	if got := resp.StatusCode; got != wantStatus {
		t.Fatalf("answer status = %d, want %d", got, wantStatus)
	}
}

// TestSessionRunner_HostStartQuestionAnswerReveal drives a hosted session
// through the runner end to end against the real server with a shrunk beat:
// host starts, the runner issues a question, a player answers, and the runner
// reveals - asserting the wire DTO hides correctness in the question phase and
// exposes it (plus a positive score) at reveal.
func TestSessionRunner_HostStartQuestionAnswerReveal(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegrationWithEnv(t, map[string]string{
		"SESSION_RUNNER_BEAT": "250ms",
		"REVEAL_DELAY":        "500ms",
	})
	baseURL := setup.BaseURL

	qz := seedRunnerLiveQuiz(ctx, t, setup.Stores.Quizzes, "runner-host-start")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "runner-host", "runner-host-pass-123")
	code := createSession(ctx, t, host, baseURL, qz.ID)

	player := newAnonClient(t)
	joinSession(ctx, t, player, baseURL, code, "Solo")

	// Host Start overrides the auto-start window: 204 and the runner begins.
	startSession(ctx, t, host, baseURL, code)

	// The runner walks round_intro -> question. In the question phase the
	// options carry no correct flag and no correctOptionIds.
	state := waitForPhase(ctx, t, player, baseURL, code, "question")
	if state.Question == nil {
		t.Fatal("question phase has no question in state")
	}
	if state.Question.ExpiresAt == nil {
		t.Error("question has no expiresAt deadline")
	}
	if got := len(state.Question.CorrectOptionIDs); got != 0 {
		t.Errorf("correctOptionIds in question phase = %d entries, want 0 (no spoilers)", got)
	}
	if len(state.Question.Options) == 0 {
		t.Fatal("question has no options")
	}
	if state.Question.StartedAt == nil {
		t.Fatal("question has no startedAt (answers-open) anchor")
	}

	// During the read beat the answer window has not opened yet: a pick lands as
	// 409, so a client cannot pre-submit before the options open.
	pick := state.Question.Options[0].ID
	if state.ServerNow.Before(*state.Question.StartedAt) {
		answerSession(ctx, t, player, baseURL, code, pick, http.StatusConflict)
	}

	// Once the read beat elapses the window opens; the player answers the FIRST
	// option (seeded correct). The state read before reveal still must not say
	// which pick was right.
	waitForAnswersOpen(ctx, t, player, baseURL, code)
	answerSession(ctx, t, player, baseURL, code, pick, http.StatusNoContent)

	preReveal := getRunnerState(ctx, t, player, baseURL, code)
	if preReveal.Phase == "question" {
		if got, want := len(preReveal.Question.AnsweredPlayerIDs), 1; got != want {
			t.Errorf("answeredPlayerIds = %d, want %d", got, want)
		}
		for _, a := range preReveal.Question.Answers {
			if a.Correct != nil {
				t.Error("answer carries correct before reveal, want nil (no spoilers)")
			}
			if a.Score != nil {
				t.Error("answer carries score before reveal, want nil")
			}
		}
	}

	// The runner closes the question (all active players answered) and
	// reveals: correctOptionIds and per-answer correctness/score appear.
	reveal := waitForPhase(ctx, t, player, baseURL, code, "reveal")
	if reveal.Question == nil {
		t.Fatal("reveal phase has no question in state")
	}
	if got := len(reveal.Question.CorrectOptionIDs); got != 1 {
		t.Fatalf("correctOptionIds at reveal = %d, want 1", got)
	}
	if got, want := reveal.Question.CorrectOptionIDs[0], pick; got != want {
		t.Errorf("correctOptionIds[0] = %d, want %d (the seeded correct option)", got, want)
	}
	if got, want := len(reveal.Question.Answers), 1; got != want {
		t.Fatalf("reveal answers = %d, want %d", got, want)
	}
	ans := reveal.Question.Answers[0]
	if ans.Correct == nil || !*ans.Correct {
		t.Error("revealed answer Correct = nil/false, want true")
	}
	if ans.Score == nil || *ans.Score <= 0 {
		t.Errorf("revealed answer Score = %v, want a positive score", ans.Score)
	}
}

// TestSessionRunner_AnswerRejectedOutsideQuestion pins that the answer
// endpoint 409s when no question is open (the lobby phase) and 404s for a
// non-participant.
func TestSessionRunner_AnswerRejectedOutsideQuestion(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegrationWithEnv(t, map[string]string{"SESSION_RUNNER_BEAT": "250ms"})
	baseURL := setup.BaseURL

	qz := seedRunnerLiveQuiz(ctx, t, setup.Stores.Quizzes, "runner-answer-gate")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "runner-gate-host", "runner-gate-pass-123")
	code := createSession(ctx, t, host, baseURL, qz.ID)

	player := newAnonClient(t)
	joinSession(ctx, t, player, baseURL, code, "Gatee")

	// In the lobby no question is open: 409.
	answerSession(ctx, t, player, baseURL, code, 1, http.StatusConflict)

	// A stranger who never joined gets 404 (the code stays opaque).
	stranger := newAnonClient(t)
	resp := httpPostJSON(ctx, t, stranger, fmt.Sprintf("%s/api/sessions/%s/answer", baseURL, code), `{"optionId": 1}`)
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusNotFound; got != want {
		t.Fatalf("stranger answer status = %d, want %d", got, want)
	}
}

// seedRunnerLiveQuiz seeds a one-question live quiz whose single option is
// correct, so the runner test can assert a positive score on a correct pick.
func seedRunnerLiveQuiz(ctx context.Context, t *testing.T, quizzes quiz.Store, slug string) *quiz.Quiz {
	t.Helper()
	qz := &quiz.Quiz{
		Title:             "Runner " + slug,
		Slug:              slug,
		Description:       "hosted runner fixture",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeLive,
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "right", Correct: true}, {Text: "wrong"}}},
		},
	}
	if err := quizzes.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz runner live err = %v, want nil", err)
	}

	return qz
}
