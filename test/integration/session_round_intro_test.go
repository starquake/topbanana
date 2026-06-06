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

// sessionRoundRes mirrors the round the round_intro phase adds to the state
// DTO (#748): the round's title, summary, and 1-indexed position.
type sessionRoundRes struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Number  int    `json:"number"`
	Total   int    `json:"total"`
}

// sessionIntroStateRes decodes the round-carrying fields of the state DTO for
// the round_intro phase.
type sessionIntroStateRes struct {
	Phase string           `json:"phase"`
	Round *sessionRoundRes `json:"round"`
}

// getIntroState reads GET /state into the round-aware decode target.
func getIntroState(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, code string,
) sessionIntroStateRes {
	t.Helper()
	resp := httpGet(ctx, t, client, fmt.Sprintf("%s/api/sessions/%s/state", baseURL, code))
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("state status = %d, want %d", got, want)
	}
	var state sessionIntroStateRes
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatalf("decode intro state: %v", err)
	}

	return state
}

// waitForIntroRound polls GET /state until the session is in round_intro with a
// round whose number matches want, then returns that round. The runner advances
// on its own shrunk beat, so the test waits for the transition rather than
// sleeping.
func waitForIntroRound(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, code string, want int,
) sessionRoundRes {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last sessionIntroStateRes
	for time.Now().Before(deadline) {
		last = getIntroState(ctx, t, client, baseURL, code)
		if last.Phase == "round_intro" && last.Round != nil && last.Round.Number == want {
			return *last.Round
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("session never reached round_intro for round %d; last phase %q", want, last.Phase)

	return sessionRoundRes{}
}

// TestSessionRoundIntro_TitleSummaryAndPosition drives a two-round hosted
// session and asserts the round_intro phase exposes each round's title and
// summary, plus its 1-indexed position and the round total, so a surface can
// name the round and tell the first round (number 1) apart from a later one.
func TestSessionRoundIntro_TitleSummaryAndPosition(t *testing.T) {
	t.Parallel()

	// A 250ms beat keeps the beat-gated round_intro observable by the 5ms
	// poller; the questions close early on all-answered, not the beat. A short
	// read beat keeps the per-question pre-answer window brief.
	ctx, setup := setupIntegrationWithEnv(t, map[string]string{
		"SESSION_RUNNER_BEAT": "250ms",
		"REVEAL_DELAY":        "200ms",
	})
	baseURL := setup.BaseURL

	qz := seedRoundIntroLiveQuiz(ctx, t, setup.Stores.Quizzes, "round-intro")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "ri-host", "ri-host-pass-123")
	code := createSession(ctx, t, host, baseURL, qz.ID)

	ace := newAnonClient(t)
	bee := newAnonClient(t)
	joinSession(ctx, t, ace, baseURL, code, "Ace")
	joinSession(ctx, t, bee, baseURL, code, "Bee")

	startSession(ctx, t, host, baseURL, code)

	// First round_intro: the title and summary are the round's own, and the
	// position marks it as the first of two rounds.
	r1 := waitForIntroRound(ctx, t, ace, baseURL, code, 1)
	if got, want := r1.Title, "Geography"; got != want {
		t.Errorf("round 1 title = %q, want %q", got, want)
	}
	if got, want := r1.Summary, "Capitals and borders"; got != want {
		t.Errorf("round 1 summary = %q, want %q", got, want)
	}
	if got, want := r1.Number, 1; got != want {
		t.Errorf("round 1 number = %d, want %d (first round)", got, want)
	}
	if got, want := r1.Total, 2; got != want {
		t.Errorf("round 1 total = %d, want %d", got, want)
	}

	// Play round 1's single question so the runner advances into round 2.
	playQuestion(ctx, t, ace, bee, baseURL, code)

	// Second round_intro: the second round's title, and a position past the
	// first round so a surface knows it is not the first round.
	r2 := waitForIntroRound(ctx, t, ace, baseURL, code, 2)
	if got, want := r2.Title, "History"; got != want {
		t.Errorf("round 2 title = %q, want %q", got, want)
	}
	if got, want := r2.Number, 2; got != want {
		t.Errorf("round 2 number = %d, want %d (not the first round)", got, want)
	}
	if got, want := r2.Total, 2; got != want {
		t.Errorf("round 2 total = %d, want %d", got, want)
	}
}

// seedRoundIntroLiveQuiz seeds a live quiz with two titled, summarised rounds of
// one question each, so the round_intro read has distinct titles/summaries and a
// round count to assert. The first option of every question is correct.
func seedRoundIntroLiveQuiz(ctx context.Context, t *testing.T, quizzes quiz.Store, slug string) *quiz.Quiz {
	t.Helper()
	rightWrong := func(pos int) *quiz.Question {
		return &quiz.Question{
			Text:     fmt.Sprintf("Q%d", pos),
			Position: pos,
			Options:  []*quiz.Option{{Text: "right", Correct: true}, {Text: "wrong"}},
		}
	}
	qz := &quiz.Quiz{
		Title:             "Round intro " + slug,
		Slug:              slug,
		Description:       "two titled rounds",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeLive,
		Rounds: []*quiz.Round{
			{Title: "Geography", Summary: "Capitals and borders", Questions: []*quiz.Question{rightWrong(1)}},
			{Title: "History", Summary: "Dates and rulers", Questions: []*quiz.Question{rightWrong(2)}},
		},
	}
	if err := quizzes.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz round-intro live err = %v, want nil", err)
	}

	return qz
}
