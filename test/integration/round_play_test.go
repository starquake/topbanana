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

	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// roundItemRes is the wire shape for the `type=round_boundary` variant
// of GET /api/games/{gameID}/questions/next. Pinned out here so the
// test can decode the discriminated union; nested-structs linter forces
// the extraction.
type roundItemRes struct {
	Type    string `json:"type"`
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Score   int    `json:"score"`
	Total   int    `json:"total"`
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
// the real HTTP server. Pins the #444 contract: /next returns a tagged
// union, the round boundary carries the running score and round name,
// POST .../seen acknowledges the round, and the final /next 404s.
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
	// The round boundary only fires for a round with an authored
	// summary (#444), so stamp one on the default round to exercise the
	// boundary path.
	giveDefaultRoundSummary(ctx, t, stores, qz.ID, "Round one wrapped up!")

	client := playerClient(t)

	gameID := createRoundPlayGame(ctx, t, client, baseURL, qz.ID)

	// --- Q1 + Q2: /next returns each question in turn ---
	q1ID := answerNextCorrect(ctx, t, client, baseURL, gameID, qz)
	if q1ID != qz.Questions[0].ID {
		t.Fatalf("first /next returned questionID = %d, want %d", q1ID, qz.Questions[0].ID)
	}
	q2ID := answerNextCorrect(ctx, t, client, baseURL, gameID, qz)
	if q2ID != qz.Questions[1].ID {
		t.Fatalf("second /next returned questionID = %d, want %d", q2ID, qz.Questions[1].ID)
	}

	// --- Round boundary: /next returns the round, carrying running score ---
	roundItem := readNextRound(ctx, t, client, baseURL, gameID)
	if got, want := roundItem.Title, "Round 1"; got != want {
		t.Errorf("round.Title = %q, want %q", got, want)
	}
	if got, want := roundItem.Summary, "Round one wrapped up!"; got != want {
		t.Errorf("round.Summary = %q, want %q", got, want)
	}
	// Both questions answered correctly at-or-near the start of the
	// answer window; CalculateScore yields ~1000 each less the
	// elapsed-fraction penalty. The play-loop test is wall-clock-
	// sensitive so we just assert the score is in the ballpark of two
	// correct answers, not the exact value.
	if got := roundItem.Score; got < 1800 || got > 2000 {
		t.Errorf("round.Score = %d, want between 1800 and 2000 (two correct answers)", got)
	}
	if got, want := roundItem.Total, len(qz.Questions); got != want {
		t.Errorf("round.Total = %d, want %d (question total stays across the boundary)", got, want)
	}

	// Repeated /next BEFORE seen returns the SAME round boundary.
	repeatRound := readNextRound(ctx, t, client, baseURL, gameID)
	if got, want := repeatRound.ID, roundItem.ID; got != want {
		t.Errorf("repeat /next round.ID = %d, want %d (idempotent until seen)", got, want)
	}

	// --- POST .../seen acknowledges the round ---
	postRoundSeen(ctx, t, client, baseURL, gameID, roundItem.ID)

	// --- Exhausted: /next 404s ---
	assertNextStatus(ctx, t, client, baseURL, gameID, http.StatusNotFound)
}

// TestRounds_SeenIsIdempotent pins the #444 contract that POST .../seen
// returns 204 even on a repeated call, and the iterator stays past the
// round boundary (not re-emitting it) afterwards.
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

	// Create + answer both questions + reach the round boundary.
	gameID := createRoundPlayGame(ctx, t, client, baseURL, qz.ID)
	_ = answerNextCorrect(ctx, t, client, baseURL, gameID, qz)
	_ = answerNextCorrect(ctx, t, client, baseURL, gameID, qz)
	roundItem := readNextRound(ctx, t, client, baseURL, gameID)

	// First seen: 204.
	postRoundSeen(ctx, t, client, baseURL, gameID, roundItem.ID)
	// Second seen: still 204 (no side effects).
	postRoundSeen(ctx, t, client, baseURL, gameID, roundItem.ID)

	// /next must be exhausted, not re-emit the round boundary.
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

// readNextRound calls /next and asserts the response is the round
// boundary variant. Returns the decoded round body.
func readNextRound(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, gameID string,
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

	return r
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

// postRoundSeen calls POST /api/games/{gameID}/rounds/{roundID}/seen
// and asserts a 204 No Content response. The /api/* surface gates on
// the session cookie alone; no CSRF token is needed (see addAPIRoutes).
// Both create and idempotent re-ack paths return 204, so the helper
// pins that status rather than taking it as a parameter.
func postRoundSeen(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, gameID string,
	roundID int64,
) {
	t.Helper()
	target := fmt.Sprintf("%s/api/games/%s/rounds/%d/seen", baseURL, gameID, roundID)
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

	// Drive the game through both questions -> round ack so a
	// game_seen_rounds row exists. The HTTP client path mirrors how a
	// real reset would land in production - everything goes through the
	// same handlers a player would hit.
	client := playerClient(t)
	gameID := createRoundPlayGame(ctx, t, client, setup.BaseURL, qz.ID)
	answerNextCorrect(ctx, t, client, setup.BaseURL, gameID, qz)
	answerNextCorrect(ctx, t, client, setup.BaseURL, gameID, qz)
	roundItem := readNextRound(ctx, t, client, setup.BaseURL, gameID)
	postRoundSeen(ctx, t, client, setup.BaseURL, gameID, roundItem.ID)

	seenBefore, err := stores.Games.ListSeenRoundIDsByGame(ctx, gameID)
	if err != nil {
		t.Fatalf("ListSeenRoundIDsByGame (before) err = %v, want nil", err)
	}
	if got, want := len(seenBefore), 1; got != want {
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

	seenAfter, err := stores.Games.ListSeenRoundIDsByGame(ctx, gameID)
	if err != nil {
		t.Fatalf("ListSeenRoundIDsByGame (after) err = %v, want nil", err)
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
