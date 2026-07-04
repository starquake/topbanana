package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
)

// seedShuffleLiveQuiz seeds a one-question live quiz with eight options so the
// per-session answer shuffle (#1074) has a wide permutation space (8! = 40320).
// The correct option is deliberately NOT first, so the test can confirm scoring
// and reveal key off the option id, not its display position. Returns the quiz
// with option ids populated, in DB order.
func seedShuffleLiveQuiz(ctx context.Context, t *testing.T, quizzes quiz.Store, slug string) *quiz.Quiz {
	t.Helper()
	options := make([]*quiz.Option, 0, 8)
	for i := range 8 {
		// The fifth option (index 4) is the correct one.
		options = append(options, &quiz.Option{Text: fmt.Sprintf("opt-%d", i), Correct: i == 4})
	}
	qz := &quiz.Quiz{
		Title:             "Shuffle " + slug,
		Published:         true,
		Slug:              slug,
		Description:       "per-session shuffle fixture",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeLive,
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: options},
		},
	}
	if err := quizzes.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz shuffle live err = %v, want nil", err)
	}

	return qz
}

// startShuffleSession registers a fresh host, promotes it so it may open a
// room, opens a session for quizID, joins the given players, and starts the
// runner, returning the join code. Each session gets its own host because a
// host may hold only one running game at a time, and the test needs several
// concurrent sessions to compare orders. It mints the host session cookie
// (registerVerifyAndMint) rather than logging in so several back-to-back host
// sign-ins from one IP don't trip the per-IP login cooldown.
func startShuffleSession(
	ctx context.Context, t *testing.T, setup integrationSetup, quizID int64, hostSlug string, players ...*http.Client,
) string {
	t.Helper()
	baseURL := setup.BaseURL
	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndMint(ctx, t, host, baseURL, setup.DBURI, hostSlug, hostSlug+"-pass-123")
	makeHost(ctx, t, setup.DBURI, hostSlug)
	code := createSession(ctx, t, host, baseURL, quizID)
	for i, p := range players {
		joinSession(ctx, t, p, baseURL, code, fmt.Sprintf("%s-p%d", hostSlug, i))
	}
	startSession(ctx, t, host, baseURL, code)

	return code
}

// questionOptionIDs waits for the question phase and returns the option ids in
// the order the server serves them to client.
func questionOptionIDs(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, code string,
) []int64 {
	t.Helper()
	state := waitForPhase(ctx, t, client, baseURL, code, "question")
	if state.Question == nil {
		t.Fatal("question phase has no question in state")
	}
	ids := make([]int64, 0, len(state.Question.Options))
	for _, o := range state.Question.Options {
		ids = append(ids, o.ID)
	}

	return ids
}

// TestSessionAnswer_OptionsShuffledPerSession pins the #1074 contract end to
// end: the live answer options are served in a per-session order that is the
// same for every player in one session, stable across reads, different across
// sessions, and not the raw DB order - while scoring and reveal still key off
// the option id. Three sessions of an eight-option quiz make the "different
// sessions differ" check effectively non-flaky (false-fail below 1e-9).
func TestSessionAnswer_OptionsShuffledPerSession(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegrationWithEnv(t, map[string]string{
		"SESSION_RUNNER_BEAT": "250ms",
		"REVEAL_DELAY":        "200ms",
	})
	baseURL := setup.BaseURL

	qz := seedShuffleLiveQuiz(ctx, t, setup.Stores.Quizzes, "answer-shuffle")
	dbOrder := make([]int64, 0, len(qz.Questions[0].Options))
	for _, o := range qz.Questions[0].Options {
		dbOrder = append(dbOrder, o.ID)
	}
	wantSet := slices.Clone(dbOrder)
	slices.Sort(wantSet)

	const sessionCount = 3
	orders := make([][]int64, 0, sessionCount)
	for s := range sessionCount {
		// Two players per session so the same-session-agreement check has two
		// independent reads.
		one := newAnonClient(t)
		two := newAnonClient(t)
		code := startShuffleSession(ctx, t, setup, qz.ID, fmt.Sprintf("sh-host-%d", s), one, two)

		first := questionOptionIDs(ctx, t, one, baseURL, code)
		second := questionOptionIDs(ctx, t, two, baseURL, code)

		// Every player in the same session sees the identical order.
		if !slices.Equal(first, second) {
			t.Errorf("session %d players saw different orders: %v vs %v", s, first, second)
		}
		// The served set is a permutation of the DB option ids - nothing dropped
		// or duplicated.
		gotSet := slices.Clone(first)
		slices.Sort(gotSet)
		if !slices.Equal(gotSet, wantSet) {
			t.Errorf("session %d option id set = %v, want %v", s, gotSet, wantSet)
		}
		orders = append(orders, first)
	}

	// At least one session's order differs from raw DB order, proving the
	// shuffle is applied rather than the options being passed through.
	shuffledSomewhere := slices.ContainsFunc(orders, func(o []int64) bool {
		return !slices.Equal(o, dbOrder)
	})
	if !shuffledSomewhere {
		t.Errorf("no session shuffled the options; all matched DB order %v", dbOrder)
	}

	// Different sessions get different orders (the anti-cheat property): across
	// three sessions of an eight-option quiz, at least two distinct orders.
	distinct := make(map[string]struct{}, sessionCount)
	for _, o := range orders {
		distinct[fmt.Sprintf("%v", o)] = struct{}{}
	}
	if len(distinct) < 2 {
		t.Errorf("3 sessions produced only %d distinct orders, want >= 2", len(distinct))
	}
}

// TestSessionAnswer_ScoringByOptionID confirms the shuffle leaves scoring and
// reveal untouched: a player who picks the seeded-correct option (found by id,
// wherever the shuffle placed it) is scored correct with a positive score and
// reveal names that same id, while a wrong picker stays at zero.
func TestSessionAnswer_ScoringByOptionID(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegrationWithEnv(t, map[string]string{
		"SESSION_RUNNER_BEAT": "250ms",
		"REVEAL_DELAY":        "200ms",
	})
	baseURL := setup.BaseURL

	qz := seedShuffleLiveQuiz(ctx, t, setup.Stores.Quizzes, "answer-shuffle-score")
	correctID, wrongID := correctAndWrongOptionID(t, qz, qz.Questions[0].ID)

	ace := newAnonClient(t)
	bee := newAnonClient(t)
	code := startShuffleSession(ctx, t, setup, qz.ID, "sh-score-host", ace, bee)

	aceID := playerIDFromState(ctx, t, ace, baseURL, code, "sh-score-host-p0")
	beeID := playerIDFromState(ctx, t, bee, baseURL, code, "sh-score-host-p1")

	// Wait for the answer window, then pick by option id regardless of the
	// shuffled display position.
	waitForAnswersOpen(ctx, t, ace, baseURL, code)
	answerSession(ctx, t, ace, baseURL, code, correctID, http.StatusNoContent)
	answerSession(ctx, t, bee, baseURL, code, wrongID, http.StatusNoContent)

	reveal := waitForPhase(ctx, t, ace, baseURL, code, "reveal")
	if reveal.Question == nil {
		t.Fatal("reveal phase has no question in state")
	}
	// Reveal names the correct option by id, whatever position the shuffle gave
	// it.
	if got, want := reveal.Question.CorrectOptionIDs, []int64{correctID}; !slices.Equal(got, want) {
		t.Errorf("correctOptionIds = %v, want %v", got, want)
	}

	aceAns := findAnswer(t, reveal.Question.Answers, aceID)
	if aceAns.Correct == nil || !*aceAns.Correct {
		t.Errorf("Ace answer Correct = %v, want true", aceAns.Correct)
	}
	if aceAns.Score == nil || *aceAns.Score <= 0 {
		t.Errorf("Ace answer Score = %v, want a positive score", aceAns.Score)
	}
	beeAns := findAnswer(t, reveal.Question.Answers, beeID)
	if beeAns.Correct == nil || *beeAns.Correct {
		t.Errorf("Bee answer Correct = %v, want false", beeAns.Correct)
	}
}

// findAnswer returns the recorded answer for playerID, failing if absent.
func findAnswer(t *testing.T, answers []sessionRunnerAns, playerID int64) sessionRunnerAns {
	t.Helper()
	for _, a := range answers {
		if a.PlayerID == playerID {
			return a
		}
	}
	t.Fatalf("answers missing player %d", playerID)

	return sessionRunnerAns{}
}
