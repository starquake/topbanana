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

// sessionRosterRowRes is one roster row in the state DTO, enough to map a
// display name to its underlying players.id.
type sessionRosterRowRes struct {
	PlayerID    int64  `json:"playerId"`
	DisplayName string `json:"displayName"`
}

// sessionRosterStateRes decodes just the roster from the state DTO.
type sessionRosterStateRes struct {
	Players []sessionRosterRowRes `json:"players"`
}

// sessionStandingRes mirrors one row of the standings the round_results and
// finished phases add to the state DTO (MP-6 / #683).
type sessionStandingRes struct {
	PlayerID    int64  `json:"playerId"`
	DisplayName string `json:"displayName"`
	RoundScore  int    `json:"roundScore"`
	TotalScore  int    `json:"totalScore"`
	Rank        int    `json:"rank"`
}

// sessionResultsStateRes decodes the standings-carrying fields of the state
// DTO for the round_results / finished phases.
type sessionResultsStateRes struct {
	Phase     string                 `json:"phase"`
	Question  *sessionRunnerQuestion `json:"question"`
	Standings []sessionStandingRes   `json:"standings"`
	ServerNow time.Time              `json:"serverNow"`
}

// getResultsState reads GET /state into the standings-aware decode target.
func getResultsState(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, code string,
) sessionResultsStateRes {
	t.Helper()
	resp := httpGet(ctx, t, client, fmt.Sprintf("%s/api/sessions/%s/state", baseURL, code))
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("state status = %d, want %d", got, want)
	}
	var state sessionResultsStateRes
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatalf("decode results state: %v", err)
	}

	return state
}

// waitForResultsPhase polls GET /state until the session reaches want, then
// returns the standings-aware state. The runner advances on its own shrunk
// beat, so the test waits for the transition rather than sleeping.
func waitForResultsPhase(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, code, want string,
) sessionResultsStateRes {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last sessionResultsStateRes
	for time.Now().Before(deadline) {
		last = getResultsState(ctx, t, client, baseURL, code)
		if last.Phase == want {
			return last
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("session never reached phase %q; last phase %q", want, last.Phase)

	return last
}

// waitForResultsAnswersOpen polls GET /state until the given question's answer
// window has opened (serverNow at or after startedAt), returning the matching
// state. The window opens after the read beat, so a pick before then would
// 409.
func waitForResultsAnswersOpen(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, code string, questionID int64,
) sessionResultsStateRes {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last sessionResultsStateRes
	for time.Now().Before(deadline) {
		last = getResultsState(ctx, t, client, baseURL, code)
		q := last.Question
		if q != nil && q.ID == questionID && q.StartedAt != nil && !last.ServerNow.Before(*q.StartedAt) {
			return last
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("question %d answer window never opened; last phase %q", questionID, last.Phase)

	return last
}

// playQuestion drives one question to close: it waits for the question phase,
// has the winner pick the correct option (index 0) and the loser the wrong one
// (index 1) on the SAME question, then waits until that question leaves the
// question phase (early close on all-answered). Returns the question id so the
// caller can confirm each call advanced to a distinct question.
func playQuestion(
	ctx context.Context, t *testing.T, winner, loser *http.Client, baseURL, code string,
) int64 {
	t.Helper()
	state := waitForResultsPhase(ctx, t, winner, baseURL, code, "question")
	if state.Question == nil || len(state.Question.Options) < 2 {
		t.Fatal("question phase missing a two-option question")
	}
	qID := state.Question.ID

	// Answers open after the read beat, so wait until the window opens before
	// submitting; a pick during the read beat would 409.
	state = waitForResultsAnswersOpen(ctx, t, winner, baseURL, code, qID)
	answerSession(ctx, t, winner, baseURL, code, state.Question.Options[0].ID, http.StatusNoContent)
	answerSession(ctx, t, loser, baseURL, code, state.Question.Options[1].ID, http.StatusNoContent)

	// Wait for the question to close so the next call targets the next question
	// rather than re-answering this one.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		cur := getResultsState(ctx, t, winner, baseURL, code)
		if cur.Phase != "question" || (cur.Question != nil && cur.Question.ID != qID) {
			return qID
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("question %d never closed", qID)

	return qID
}

// findStanding returns the standing for playerID, failing if absent.
func findStanding(t *testing.T, standings []sessionStandingRes, playerID int64) sessionStandingRes {
	t.Helper()
	for _, s := range standings {
		if s.PlayerID == playerID {
			return s
		}
	}
	t.Fatalf("standings missing player %d", playerID)

	return sessionStandingRes{}
}

// TestSessionRoundResults_DeltasTotalsAndLeaderboard drives a two-round hosted
// session to completion and asserts: (a) the round_results phase exposes each
// player's points-this-round, new cumulative total, and ranking; (b) the
// finished phase exposes the final standings; and (c) the quiz's standard
// leaderboard reflects the recorded per-player results once the session
// finished. The winner (Ace) answers the correct option every question; the
// loser (Bee) answers wrong, so the ordering is deterministic.
func TestSessionRoundResults_DeltasTotalsAndLeaderboard(t *testing.T) {
	t.Parallel()

	// A 250ms beat keeps the beat-gated phases (round_intro / reveal /
	// round_results) observable by the 5ms poller without slowing the test
	// much; the questions close early on all-answered, not the beat. A short
	// read beat keeps the per-question pre-answer window brief.
	ctx, setup := setupIntegrationWithEnv(t, map[string]string{
		"SESSION_RUNNER_BEAT": "250ms",
		"REVEAL_DELAY":        "200ms",
	})
	baseURL := setup.BaseURL

	qz := seedMultiRoundLiveQuiz(ctx, t, setup.Stores.Quizzes, "round-results")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "rr-host", "rr-host-pass-123")
	code := createSession(ctx, t, host, baseURL, qz.ID)

	ace := newAnonClient(t)
	bee := newAnonClient(t)
	joinSession(ctx, t, ace, baseURL, code, "Ace")
	joinSession(ctx, t, bee, baseURL, code, "Bee")

	aceID := playerIDFromState(ctx, t, ace, baseURL, code, "Ace")
	beeID := playerIDFromState(ctx, t, bee, baseURL, code, "Bee")

	startSession(ctx, t, host, baseURL, code)

	// Round 1 has two questions. Ace picks the correct option, Bee the wrong
	// one; both answering closes each question early. Distinct ids confirm the
	// runner advanced from q1 to q2.
	q1 := playQuestion(ctx, t, ace, bee, baseURL, code)
	q2 := playQuestion(ctx, t, ace, bee, baseURL, code)
	if q1 == q2 {
		t.Fatalf("round 1 played the same question twice (id %d)", q1)
	}

	// round_results after round 1: Ace leads with two correct answers' worth of
	// points this round, Bee sits at 0.
	r1 := waitForResultsPhase(ctx, t, ace, baseURL, code, "round_results")
	aceR1 := findStanding(t, r1.Standings, aceID)
	beeR1 := findStanding(t, r1.Standings, beeID)
	if aceR1.RoundScore <= 0 {
		t.Errorf("Ace round 1 score = %d, want > 0", aceR1.RoundScore)
	}
	if got, want := aceR1.TotalScore, aceR1.RoundScore; got != want {
		t.Errorf("Ace round 1 total = %d, want %d (equals round score in round 1)", got, want)
	}
	if got, want := beeR1.RoundScore, 0; got != want {
		t.Errorf("Bee round 1 score = %d, want %d", got, want)
	}
	if got, want := aceR1.Rank, 1; got != want {
		t.Errorf("Ace round 1 rank = %d, want %d", got, want)
	}
	if got, want := beeR1.Rank, 2; got != want {
		t.Errorf("Bee round 1 rank = %d, want %d", got, want)
	}
	aceCumulativeAfterR1 := aceR1.TotalScore

	// Round 2 is the final round; same picks. Its closing reveal finishes the
	// session directly, skipping round_results, so the game ends on a single
	// final-standings screen (#749).
	playQuestion(ctx, t, ace, bee, baseURL, code)

	// finished: final standings carry the full cumulative totals, Ace first.
	// There is no final-round round_results, so the round 2 contribution is
	// observed here: Ace's cumulative total grew past round 1 and RoundScore is
	// 0 in the finished phase (no single round in focus).
	final := waitForResultsPhase(ctx, t, ace, baseURL, code, "finished")
	if got, want := len(final.Standings), 2; got != want {
		t.Fatalf("final standings = %d entries, want %d", got, want)
	}
	aceFinal := findStanding(t, final.Standings, aceID)
	beeFinal := findStanding(t, final.Standings, beeID)
	if got, want := aceFinal.RoundScore, 0; got != want {
		t.Errorf("Ace finished round score = %d, want %d (no round in focus)", got, want)
	}
	if aceFinal.TotalScore <= aceCumulativeAfterR1 {
		t.Errorf("Ace final total %d not greater than round 1 cumulative %d (round 2 added no points)",
			aceFinal.TotalScore, aceCumulativeAfterR1)
	}
	if got, want := aceFinal.Rank, 1; got != want {
		t.Errorf("Ace final rank = %d, want %d", got, want)
	}
	if aceFinal.TotalScore <= beeFinal.TotalScore {
		t.Errorf("Ace final total %d not greater than Bee %d", aceFinal.TotalScore, beeFinal.TotalScore)
	}
	if got, want := beeFinal.TotalScore, 0; got != want {
		t.Errorf("Bee final total = %d, want %d", got, want)
	}

	// The quiz's standard leaderboard now reflects the finished session.
	board := getQuizLeaderboard(ctx, t, host, baseURL, qz)
	aceBoard := findLeaderboardEntry(t, board, aceID)
	beeBoard := findLeaderboardEntry(t, board, beeID)
	if got, want := aceBoard.Score, aceFinal.TotalScore; got != want {
		t.Errorf("Ace leaderboard score = %d, want %d (the recorded session total)", got, want)
	}
	if got, want := beeBoard.Score, beeFinal.TotalScore; got != want {
		t.Errorf("Bee leaderboard score = %d, want %d", got, want)
	}
}

// playerIDFromState resolves a player's underlying players.id from the roster
// in GET /state by matching the display name they joined under.
func playerIDFromState(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, code, displayName string,
) int64 {
	t.Helper()
	resp := httpGet(ctx, t, client, fmt.Sprintf("%s/api/sessions/%s/state", baseURL, code))
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("state status = %d, want %d", got, want)
	}
	var state sessionRosterStateRes
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatalf("decode roster: %v", err)
	}
	for _, p := range state.Players {
		if p.DisplayName == displayName {
			return p.PlayerID
		}
	}
	t.Fatalf("roster has no player named %q", displayName)

	return 0
}

// getQuizLeaderboard reads GET /api/quizzes/{slug}-{id}/leaderboard.
func getQuizLeaderboard(
	ctx context.Context, t *testing.T, client *http.Client, baseURL string, qz *quiz.Quiz,
) leaderboardRes {
	t.Helper()
	url := fmt.Sprintf("%s/api/quizzes/%s-%d/leaderboard", baseURL, qz.Slug, qz.ID)
	resp := httpGet(ctx, t, client, url)
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("leaderboard status = %d, want %d", got, want)
	}
	var board leaderboardRes
	if err := json.NewDecoder(resp.Body).Decode(&board); err != nil {
		t.Fatalf("decode leaderboard: %v", err)
	}

	return board
}

// findLeaderboardEntry returns the leaderboard entry for playerID, failing if
// absent.
func findLeaderboardEntry(t *testing.T, board leaderboardRes, playerID int64) leaderboardEntryRes {
	t.Helper()
	for _, e := range board.Entries {
		if e.PlayerID == playerID {
			return e
		}
	}
	t.Fatalf("leaderboard missing player %d", playerID)

	return leaderboardEntryRes{}
}

// seedMultiRoundLiveQuiz seeds a live quiz with two rounds (two questions then
// one). The first option of every question is correct, so a player picking
// index 0 scores and index 1 does not.
func seedMultiRoundLiveQuiz(ctx context.Context, t *testing.T, quizzes quiz.Store, slug string) *quiz.Quiz {
	t.Helper()
	rightWrong := func(pos int) *quiz.Question {
		return &quiz.Question{
			Text:     fmt.Sprintf("Q%d", pos),
			Position: pos,
			Options:  []*quiz.Option{{Text: "right", Correct: true}, {Text: "wrong"}},
		}
	}
	qz := &quiz.Quiz{
		Title:             "Multi " + slug,
		Slug:              slug,
		Description:       "two-round hosted fixture",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeLive,
		Rounds: []*quiz.Round{
			{Title: "Round 1", Questions: []*quiz.Question{rightWrong(1), rightWrong(2)}},
			{Title: "Round 2", Questions: []*quiz.Question{rightWrong(3)}},
		},
	}
	if err := quizzes.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz multi-round live err = %v, want nil", err)
	}

	return qz
}
