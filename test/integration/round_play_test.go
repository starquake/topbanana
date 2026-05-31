//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// roundItemRes is the wire shape for the `type=round_boundary` variant
// of GET /api/games/{gameID}/questions/next, covering both the intro and
// results phases (#548). Pinned out here so the test can decode the
// discriminated union; nested-structs linter forces the extraction. The
// recap fields (RoundScore/RoundCorrect/RoundQuestions) are only
// populated on the results phase.
type roundItemRes struct {
	Type           string    `json:"type"`
	Phase          string    `json:"phase"`
	ID             int64     `json:"id"`
	Title          string    `json:"title"`
	Summary        string    `json:"summary"`
	Score          int       `json:"score"`
	RoundScore     int       `json:"roundScore"`
	RoundCorrect   int       `json:"roundCorrect"`
	RoundQuestions int       `json:"roundQuestions"`
	StartedAt      time.Time `json:"startedAt"`
	ExpiredAt      time.Time `json:"expiredAt"`
	Total          int       `json:"total"`
}

// nextItemRes lets the play-loop test peek at the `type` discriminator
// before committing to a full decode. The fields are the intersection
// of question and round boundary - just enough to branch.
type nextItemRes struct {
	Type string `json:"type"`
	ID   int64  `json:"id"`
}

// roundAnswerRes mirrors the JSON shape of POST .../answers. Pulled
// out for the nested-structs linter.
type roundAnswerRes struct {
	Correct bool `json:"correct"`
	Score   int  `json:"score"`
}

// roundPlayQuiz is the fixture used by the round play-loop tests: two
// questions in the quiz's default round. Created fresh per test so a
// flaky run doesn't leak state across tables. The store attaches both
// questions to the default 'Round 1' (#444), so the round boundary
// fires once after Q2.
func roundPlayQuiz(adminID int64) *quiz.Quiz {
	return &quiz.Quiz{
		Title:             "Round Play Quiz",
		Slug:              "round-play-quiz",
		CreatedByPlayerID: adminID,
		Questions: []*quiz.Question{
			{
				Text:     "Capital of France?",
				Position: 1,
				Options: []*quiz.Option{
					{Text: "Paris", Correct: true},
					{Text: "London"},
				},
			},
			{
				Text:     "Capital of Spain?",
				Position: 2,
				Options: []*quiz.Option{
					{Text: "Madrid", Correct: true},
					{Text: "Barcelona"},
				},
			},
		},
	}
}

// giveDefaultRoundSummary stamps a summary on the quiz's default round
// so its boundary fires during play. A round with an empty summary is
// skipped by the iterator (#444), so the play-loop tests need an
// authored summary to exercise the boundary path.
func giveDefaultRoundSummary(
	ctx context.Context, t *testing.T, stores *store.Stores, quizID int64, summary string,
) {
	t.Helper()
	round, err := stores.Quizzes.GetDefaultRound(ctx, quizID)
	if err != nil {
		t.Fatalf("GetDefaultRound err = %v, want nil", err)
	}
	round.Summary = summary
	if uErr := stores.Quizzes.UpdateRound(ctx, round); uErr != nil {
		t.Fatalf("UpdateRound err = %v, want nil", uErr)
	}
}

// findCorrectOption picks the correct option for the given question id
// from the seeded quiz. The /next response carries option ids but no
// correctness flag (and never should), so the test has to round-trip
// through the seed data to know which option scores points.
func findCorrectOption(t *testing.T, qz *quiz.Quiz, questionID int64) int64 {
	t.Helper()
	for _, q := range qz.Questions {
		if q.ID != questionID {
			continue
		}
		for _, o := range q.Options {
			if o.Correct {
				return o.ID
			}
		}
	}
	t.Fatalf("no correct option found for question %d", questionID)

	return 0
}

// playerClient returns a cookie-jar client that EnsurePlayer will
// upgrade to an anonymous players row on the first /api call.
func playerClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}

	return &http.Client{Jar: jar}
}

// peekType decodes only the `type` field of the /next response so the
// caller can branch without committing to the question or round
// boundary decode shape.
func peekType(t *testing.T, body []byte) string {
	t.Helper()
	var peek nextItemRes
	if err := json.Unmarshal(body, &peek); err != nil {
		t.Fatalf("decode /next type err = %v, want nil; body=%q", err, body)
	}

	return peek.Type
}

// TestRounds_PlayLoop drives a player through a single-round quiz over
// the real HTTP server. Pins the #548 contract: /next emits an intro
// boundary (title + summary, phase=intro) before the round's questions,
// then each question, then a results boundary (phase=results) carrying
// the running total and the per-round recap. Acking each phase advances;
// the final /next 404s.
func TestRounds_PlayLoop(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	stores := setup.Stores

	adminPlayer := seedGameplayAdmin(ctx, t, baseURL, stores)

	qz := roundPlayQuiz(adminPlayer.ID)
	if err := stores.Quizzes.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}
	// Both boundary phases only fire for a round with an authored
	// summary (#548), so stamp one on the default round.
	giveDefaultRoundSummary(ctx, t, stores, qz.ID, "Round one wrapped up!")

	client := playerClient(t)

	gameID := createRoundPlayGame(ctx, t, client, baseURL, qz.ID)

	// --- Intro boundary: emitted before the round's first question ---
	introItem := readNextRound(ctx, t, client, baseURL, gameID, "intro")
	if got, want := introItem.Title, "Round 1"; got != want {
		t.Errorf("intro.Title = %q, want %q", got, want)
	}
	if got, want := introItem.Summary, "Round one wrapped up!"; got != want {
		t.Errorf("intro.Summary = %q, want %q", got, want)
	}
	if got, want := introItem.Total, len(qz.Questions); got != want {
		t.Errorf("intro.Total = %d, want %d (question total stays across the boundary)", got, want)
	}
	assertBoundaryWindow(t, "intro", introItem, qz.TimeLimitSeconds)

	// Repeated /next BEFORE acking the intro returns the SAME intro.
	repeatIntro := readNextRound(ctx, t, client, baseURL, gameID, "intro")
	if got, want := repeatIntro.ID, introItem.ID; got != want {
		t.Errorf("repeat /next intro.ID = %d, want %d (idempotent until seen)", got, want)
	}
	postRoundSeen(ctx, t, client, baseURL, gameID, introItem.ID, "intro")

	// --- Q1 + Q2: /next returns each question in turn ---
	q1ID := answerNextCorrect(ctx, t, client, baseURL, gameID, qz)
	if q1ID != qz.Questions[0].ID {
		t.Fatalf("first /next returned questionID = %d, want %d", q1ID, qz.Questions[0].ID)
	}
	q2ID := answerNextCorrect(ctx, t, client, baseURL, gameID, qz)
	if q2ID != qz.Questions[1].ID {
		t.Fatalf("second /next returned questionID = %d, want %d", q2ID, qz.Questions[1].ID)
	}

	// --- Results boundary: running total + per-round recap ---
	resultsItem := readNextRound(ctx, t, client, baseURL, gameID, "results")
	if got, want := resultsItem.Title, "Round 1"; got != want {
		t.Errorf("results.Title = %q, want %q", got, want)
	}
	// Both questions answered correctly at-or-near the start of the
	// answer window; CalculateScore yields ~1000 each less the
	// elapsed-fraction penalty. The play-loop test is wall-clock-
	// sensitive so we just assert the score is in the ballpark of two
	// correct answers, not the exact value.
	if got := resultsItem.Score; got < 1800 || got > 2000 {
		t.Errorf("results.Score = %d, want between 1800 and 2000 (two correct answers)", got)
	}
	if got := resultsItem.RoundScore; got < 1800 || got > 2000 {
		t.Errorf("results.RoundScore = %d, want between 1800 and 2000 (this round, two correct)", got)
	}
	if got, want := resultsItem.RoundCorrect, 2; got != want {
		t.Errorf("results.RoundCorrect = %d, want %d", got, want)
	}
	if got, want := resultsItem.RoundQuestions, len(qz.Questions); got != want {
		t.Errorf("results.RoundQuestions = %d, want %d", got, want)
	}
	if got, want := resultsItem.Total, len(qz.Questions); got != want {
		t.Errorf("results.Total = %d, want %d (question total stays across the boundary)", got, want)
	}
	assertBoundaryWindow(t, "results", resultsItem, qz.TimeLimitSeconds)

	// --- POST .../seen/results acknowledges the recap ---
	postRoundSeen(ctx, t, client, baseURL, gameID, resultsItem.ID, "results")

	// --- Exhausted: /next 404s ---
	assertNextStatus(ctx, t, client, baseURL, gameID, http.StatusNotFound)
}

// TestRounds_SeenIsIdempotent pins the #548 contract that POST
// .../seen/{phase} returns 204 even on a repeated call, and the iterator
// stays past the round boundary phase (not re-emitting it) afterwards.
func TestRounds_SeenIsIdempotent(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	stores := setup.Stores

	adminPlayer := seedGameplayAdmin(ctx, t, baseURL, stores)

	qz := roundPlayQuiz(adminPlayer.ID)
	if err := stores.Quizzes.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}
	giveDefaultRoundSummary(ctx, t, stores, qz.ID, "Round one wrapped up!")

	client := playerClient(t)

	// Intro -> ack it twice -> answer both questions -> reach results.
	gameID := createRoundPlayGame(ctx, t, client, baseURL, qz.ID)
	introItem := readNextRound(ctx, t, client, baseURL, gameID, "intro")
	postRoundSeen(ctx, t, client, baseURL, gameID, introItem.ID, "intro")
	postRoundSeen(ctx, t, client, baseURL, gameID, introItem.ID, "intro")

	_ = answerNextCorrect(ctx, t, client, baseURL, gameID, qz)
	_ = answerNextCorrect(ctx, t, client, baseURL, gameID, qz)
	resultsItem := readNextRound(ctx, t, client, baseURL, gameID, "results")

	// First seen: 204. Second seen: still 204 (no side effects).
	postRoundSeen(ctx, t, client, baseURL, gameID, resultsItem.ID, "results")
	postRoundSeen(ctx, t, client, baseURL, gameID, resultsItem.ID, "results")

	// /next must be exhausted, not re-emit the results boundary.
	assertNextStatus(ctx, t, client, baseURL, gameID, http.StatusNotFound)
}

// TestRounds_NoSummaryShowsNoBoundary pins that a quiz whose round has
// no authored summary plays straight through both questions with neither
// an intro nor a results boundary, then /next 404s (the client treats
// that as "go to the final leaderboard"). This is the EXACTLY-as-today
// path for single default-round quizzes (#548).
func TestRounds_NoSummaryShowsNoBoundary(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	stores := setup.Stores

	adminPlayer := seedGameplayAdmin(ctx, t, baseURL, stores)

	qz := roundPlayQuiz(adminPlayer.ID)
	if err := stores.Quizzes.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}
	// No giveDefaultRoundSummary: the default round keeps its empty
	// summary, so no boundary card should appear.

	client := playerClient(t)
	gameID := createRoundPlayGame(ctx, t, client, baseURL, qz.ID)

	// Both questions come back as questions, no intro before Q1.
	if got := answerNextCorrect(ctx, t, client, baseURL, gameID, qz); got != qz.Questions[0].ID {
		t.Fatalf("first /next questionID = %d, want %d", got, qz.Questions[0].ID)
	}
	if got := answerNextCorrect(ctx, t, client, baseURL, gameID, qz); got != qz.Questions[1].ID {
		t.Fatalf("second /next questionID = %d, want %d", got, qz.Questions[1].ID)
	}

	// No results boundary: straight to exhausted.
	assertNextStatus(ctx, t, client, baseURL, gameID, http.StatusNotFound)
}

// answerNextCorrect calls /next, asserts the response is a question
// (not a round boundary), picks the correct option, and POSTs it.
// Returns the question id so the caller can double-check ordering.
func answerNextCorrect(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, gameID string, qz *quiz.Quiz,
) int64 {
	t.Helper()
	resp := httpGet(ctx, t, client, fmt.Sprintf("%s/api/games/%s/questions/next", baseURL, gameID))
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("/next status = %d, want %d", got, want)
	}
	body := readAllOrFatal(t, resp)

	if got, want := peekType(t, body), "question"; got != want {
		t.Fatalf("/next type = %q, want %q; body=%q", got, want, body)
	}
	var q nextQuestionRes
	if err := json.Unmarshal(body, &q); err != nil {
		t.Fatalf("decode /next question err = %v, want nil; body=%q", err, body)
	}

	optionID := findCorrectOption(t, qz, q.ID)
	answerURL := fmt.Sprintf("%s/api/games/%s/questions/%d/answers", baseURL, gameID, q.ID)
	answerResp := httpPostJSON(ctx, t, client, answerURL, fmt.Sprintf(`{"optionId": %d}`, optionID))
	defer closeBody(t, answerResp.Body)
	if got, want := answerResp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("answer status = %d, want %d", got, want)
	}
	var ans roundAnswerRes
	if err := json.NewDecoder(answerResp.Body).Decode(&ans); err != nil {
		t.Fatalf("decode answer err = %v, want nil", err)
	}
	if got, want := ans.Correct, true; got != want {
		t.Errorf("answer.Correct = %v, want %v", got, want)
	}

	return q.ID
}

// readNextRound calls /next, asserts the response is the round boundary
// variant in the expected phase, and returns the decoded round body.
func readNextRound(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, gameID, wantPhase string,
) roundItemRes {
	t.Helper()
	resp := httpGet(ctx, t, client, fmt.Sprintf("%s/api/games/%s/questions/next", baseURL, gameID))
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("/next status = %d, want %d", got, want)
	}
	body := readAllOrFatal(t, resp)

	if got, want := peekType(t, body), "round_boundary"; got != want {
		t.Fatalf("/next type = %q, want %q; body=%q", got, want, body)
	}
	var r roundItemRes
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("decode /next round err = %v, want nil; body=%q", err, body)
	}
	if got := r.Phase; got != wantPhase {
		t.Fatalf("/next round phase = %q, want %q; body=%q", got, wantPhase, body)
	}

	return r
}

// assertBoundaryWindow pins the #548 auto-advance contract: both round
// boundary phases carry a non-zero StartedAt/ExpiredAt window exactly
// one quiz-default answer duration (timeLimitSeconds) long.
func assertBoundaryWindow(t *testing.T, phase string, item roundItemRes, timeLimitSeconds int) {
	t.Helper()
	if item.StartedAt.IsZero() {
		t.Errorf("%s.StartedAt is zero, want a populated timestamp", phase)
	}
	if item.ExpiredAt.IsZero() {
		t.Errorf("%s.ExpiredAt is zero, want a populated timestamp", phase)
	}
	want := time.Duration(timeLimitSeconds) * time.Second
	if got := item.ExpiredAt.Sub(item.StartedAt); got != want {
		t.Errorf("%s window ExpiredAt-StartedAt = %v, want %v (quiz default)", phase, got, want)
	}
}

// createRoundPlayGame issues POST /api/games for the given quiz id and
// returns the created game id. Wraps the create-game boilerplate so the
// parent tests don't have to nest the JSON decode block twice.
func createRoundPlayGame(
	ctx context.Context, t *testing.T, client *http.Client, baseURL string, quizID int64,
) string {
	t.Helper()
	resp := httpPostJSON(ctx, t, client, baseURL+"/api/games", fmt.Sprintf(`{"quizId": %d}`, quizID))
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusCreated; got != want {
		t.Fatalf("create game status = %d, want %d", got, want)
	}
	var createGameRes struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createGameRes); err != nil {
		t.Fatalf("decode create game err = %v, want nil", err)
	}

	return createGameRes.ID
}

// assertNextStatus calls /next and asserts the status code without
// decoding the body. Pulled out so the "exhausted -> 404" check at the
// tail of the play loop doesn't replicate the close-body dance.
func assertNextStatus(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, gameID string, wantStatus int,
) {
	t.Helper()
	resp := httpGet(ctx, t, client, fmt.Sprintf("%s/api/games/%s/questions/next", baseURL, gameID))
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, wantStatus; got != want {
		t.Fatalf("/next status = %d, want %d", got, want)
	}
}

// postRoundSeen calls POST /api/games/{gameID}/rounds/{roundID}/seen/{phase}
// and asserts a 204 No Content response. The /api/* surface gates on
// the session cookie alone; no CSRF token is needed (see addAPIRoutes).
// Both create and idempotent re-ack paths return 204, so the helper
// pins that status rather than taking it as a parameter.
func postRoundSeen(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, gameID string,
	roundID int64, phase string,
) {
	t.Helper()
	target := fmt.Sprintf("%s/api/games/%s/rounds/%d/seen/%s", baseURL, gameID, roundID, phase)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, nil)
	if err != nil {
		t.Fatalf("NewRequest seen err = %v, want nil", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do seen err = %v, want nil", err)
	}
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusNoContent; got != want {
		t.Fatalf("POST .../seen status = %d, want %d", got, want)
	}
}

// readAllOrFatal reads the whole response body, t.Fatalfs on error.
// The helpers above peek at the `type` discriminator and then decode
// the body, so they need the full bytes in hand rather than a one-shot
// json.Decoder.
func readAllOrFatal(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("io.ReadAll body err = %v, want nil", err)
	}

	return buf
}

// TestRounds_ResetCascadesSeenRows pins the FK cascade on
// game_seen_rounds.game_id: when an admin resets a player's game on a
// quiz (which calls store.GameStore.DeleteGamesForPlayerOnQuiz), the
// seen-round rows for that game must disappear too. Without the cascade
// those rows would be orphans referencing a deleted game id, which a
// future re-play could match against by accident.
func TestRounds_ResetCascadesSeenRows(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	stores := setup.Stores

	adminPlayer := seedGameplayAdmin(ctx, t, setup.BaseURL, stores)
	qz := roundPlayQuiz(adminPlayer.ID)
	if err := stores.Quizzes.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}
	giveDefaultRoundSummary(ctx, t, stores, qz.ID, "Round one wrapped up!")

	// Drive the game through both boundary phases so two
	// game_seen_rounds rows exist. The HTTP client path mirrors how a
	// real reset would land in production - everything goes through the
	// same handlers a player would hit.
	client := playerClient(t)
	gameID := createRoundPlayGame(ctx, t, client, setup.BaseURL, qz.ID)
	introItem := readNextRound(ctx, t, client, setup.BaseURL, gameID, "intro")
	postRoundSeen(ctx, t, client, setup.BaseURL, gameID, introItem.ID, "intro")
	answerNextCorrect(ctx, t, client, setup.BaseURL, gameID, qz)
	answerNextCorrect(ctx, t, client, setup.BaseURL, gameID, qz)
	resultsItem := readNextRound(ctx, t, client, setup.BaseURL, gameID, "results")
	postRoundSeen(ctx, t, client, setup.BaseURL, gameID, resultsItem.ID, "results")

	seenBefore, err := stores.Games.ListSeenRoundPhasesByGame(ctx, gameID)
	if err != nil {
		t.Fatalf("ListSeenRoundPhasesByGame (before) err = %v, want nil", err)
	}
	if got, want := len(seenBefore), 2; got != want {
		t.Fatalf("seen rows before reset = %d, want %d", got, want)
	}

	// Look up the player id from the cookie jar - the anonymous player
	// is whichever row the EnsurePlayer middleware just minted for the
	// cookie.
	playerID := fetchSelfPlayerID(ctx, t, client, setup.BaseURL)

	// Reset the player's game on the quiz. This is the same transaction
	// the admin /reset button triggers.
	if dErr := stores.Games.DeleteGamesForPlayerOnQuiz(ctx, playerID, qz.ID); dErr != nil {
		t.Fatalf("DeleteGamesForPlayerOnQuiz err = %v, want nil", dErr)
	}

	seenAfter, err := stores.Games.ListSeenRoundPhasesByGame(ctx, gameID)
	if err != nil {
		t.Fatalf("ListSeenRoundPhasesByGame (after) err = %v, want nil", err)
	}
	if got, want := len(seenAfter), 0; got != want {
		t.Errorf(
			"seen rows after reset = %d, want %d (FK cascade should have swept them)",
			got, want,
		)
	}
}

// fetchSelfPlayerID asks the server "who am I" via the same /me
// endpoint the SPA uses. Returns the anonymous player id minted by
// EnsurePlayer for the cookie jar on the supplied client.
func fetchSelfPlayerID(ctx context.Context, t *testing.T, client *http.Client, baseURL string) int64 {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/players/me", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext err = %v, want nil", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do /api/players/me err = %v, want nil", err)
	}
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("GET /api/players/me status = %d, want %d", got, want)
	}
	var me playerMeResponse
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		t.Fatalf("decode /api/players/me err = %v, want nil", err)
	}

	return me.ID
}
