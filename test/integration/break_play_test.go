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
)

// breakItemRes is the wire shape for the `type=break` variant of
// GET /api/games/{gameID}/questions/next. Pinned out here so the test
// can decode the discriminated union; nested-structs linter forces the
// extraction.
type breakItemRes struct {
	Type  string `json:"type"`
	ID    int64  `json:"id"`
	Text  string `json:"text"`
	Score int    `json:"score"`
	Total int    `json:"total"`
}

// nextItemRes lets the play-loop test peek at the `type` discriminator
// before committing to a full decode. The fields are the intersection
// of question and break - just enough to branch.
type nextItemRes struct {
	Type string `json:"type"`
	ID   int64  `json:"id"`
}

// breakAnswerRes mirrors the JSON shape of POST .../answers. Pulled
// out for the nested-structs linter.
type breakAnswerRes struct {
	Correct bool `json:"correct"`
	Score   int  `json:"score"`
}

// breakPlayQuiz is the fixture used by the break play-loop tests: two
// questions with a single break between them. Created fresh per test
// so a flaky run doesn't leak state across tables.
func breakPlayQuiz(adminID int64) *quiz.Quiz {
	return &quiz.Quiz{
		Title:             "Break Play Quiz",
		Slug:              "break-play-quiz",
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
// caller can branch without committing to the question or break
// decode shape.
func peekType(t *testing.T, body []byte) string {
	t.Helper()
	var peek nextItemRes
	if err := json.Unmarshal(body, &peek); err != nil {
		t.Fatalf("decode /next type err = %v, want nil; body=%q", err, body)
	}

	return peek.Type
}

// TestBreaks_PlayLoop drives a player through a quiz with a break
// between Q1 and Q2 over the real HTTP server. Pins the slice-2
// contract: /next returns a tagged union, the break carries the
// running score, POST .../seen acknowledges the break, the next /next
// call advances to Q2, and the final /next 404s. See #167 slice 2.
func TestBreaks_PlayLoop(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	stores := setup.Stores

	adminPlayer := seedGameplayAdmin(ctx, t, baseURL, stores)

	qz := breakPlayQuiz(adminPlayer.ID)
	if err := stores.Quizzes.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}
	brk := &quiz.Break{QuizID: qz.ID, Position: 1, Text: "Halfway through!"}
	if err := stores.Quizzes.CreateBreak(ctx, brk); err != nil {
		t.Fatalf("CreateBreak err = %v, want nil", err)
	}

	client := playerClient(t)

	gameID := createBreakPlayGame(ctx, t, client, baseURL, qz.ID)

	// --- Q1: /next returns the first question ---
	q1ID := answerNextCorrect(ctx, t, client, baseURL, gameID, qz)
	if q1ID != qz.Questions[0].ID {
		t.Fatalf("first /next returned questionID = %d, want %d", q1ID, qz.Questions[0].ID)
	}

	// --- Break: /next returns the break, carrying running score ---
	breakItem := readNextBreak(ctx, t, client, baseURL, gameID)
	if got, want := breakItem.ID, brk.ID; got != want {
		t.Errorf("break.ID = %d, want %d", got, want)
	}
	if got, want := breakItem.Text, "Halfway through!"; got != want {
		t.Errorf("break.Text = %q, want %q", got, want)
	}
	// Q1 answered correctly at-or-near the start of the answer window;
	// CalculateScore yields ~1000 less the elapsed-fraction penalty.
	// The play-loop test is wall-clock-sensitive so we just assert the
	// score is in the maxPoints ballpark, not the exact value.
	if got := breakItem.Score; got < 900 || got > 1000 {
		t.Errorf("break.Score = %d, want between 900 and 1000 (one correct answer)", got)
	}
	if got, want := breakItem.Total, len(qz.Questions); got != want {
		t.Errorf("break.Total = %d, want %d (question total stays across breaks)", got, want)
	}

	// Repeated /next BEFORE seen returns the SAME break.
	repeatBreak := readNextBreak(ctx, t, client, baseURL, gameID)
	if got, want := repeatBreak.ID, brk.ID; got != want {
		t.Errorf("repeat /next break.ID = %d, want %d (idempotent until seen)", got, want)
	}

	// --- POST .../seen acknowledges the break ---
	postBreakSeen(ctx, t, client, baseURL, gameID, brk.ID)

	// --- Q2: /next returns the second question ---
	q2ID := answerNextCorrect(ctx, t, client, baseURL, gameID, qz)
	if q2ID != qz.Questions[1].ID {
		t.Fatalf("post-break /next returned questionID = %d, want %d", q2ID, qz.Questions[1].ID)
	}

	// --- Exhausted: /next 404s ---
	assertNextStatus(ctx, t, client, baseURL, gameID, http.StatusNotFound)
}

// TestBreaks_SeenIsIdempotent pins the slice-2 contract that POST
// .../seen returns 204 even on a repeated call, and the iterator
// stays on Q2 (not re-emitting the break) afterwards.
func TestBreaks_SeenIsIdempotent(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	stores := setup.Stores

	adminPlayer := seedGameplayAdmin(ctx, t, baseURL, stores)

	qz := breakPlayQuiz(adminPlayer.ID)
	if err := stores.Quizzes.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}
	brk := &quiz.Break{QuizID: qz.ID, Position: 1}
	if err := stores.Quizzes.CreateBreak(ctx, brk); err != nil {
		t.Fatalf("CreateBreak err = %v, want nil", err)
	}

	client := playerClient(t)

	// Create + answer Q1 + reach the break.
	gameID := createBreakPlayGame(ctx, t, client, baseURL, qz.ID)
	_ = answerNextCorrect(ctx, t, client, baseURL, gameID, qz)
	_ = readNextBreak(ctx, t, client, baseURL, gameID)

	// First seen: 204.
	postBreakSeen(ctx, t, client, baseURL, gameID, brk.ID)
	// Second seen: still 204 (no side effects).
	postBreakSeen(ctx, t, client, baseURL, gameID, brk.ID)

	// /next must advance to Q2, not re-emit the break.
	q2ID := readNextQuestionID(ctx, t, client, baseURL, gameID)
	if q2ID != qz.Questions[1].ID {
		t.Fatalf("post-double-seen /next returned questionID = %d, want %d", q2ID, qz.Questions[1].ID)
	}
}

// answerNextCorrect calls /next, asserts the response is a question
// (not a break), picks the correct option, and POSTs it. Returns the
// question id so the caller can double-check ordering. The break
// play-loop tests only ever submit correct answers - "submit a wrong
// answer" is exercised in TestService_SubmitAnswer at the unit-test
// layer instead.
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
	var ans breakAnswerRes
	if err := json.NewDecoder(answerResp.Body).Decode(&ans); err != nil {
		t.Fatalf("decode answer err = %v, want nil", err)
	}
	if got, want := ans.Correct, true; got != want {
		t.Errorf("answer.Correct = %v, want %v", got, want)
	}

	return q.ID
}

// readNextBreak calls /next and asserts the response is the break
// variant. Returns the decoded break body.
func readNextBreak(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, gameID string,
) breakItemRes {
	t.Helper()
	resp := httpGet(ctx, t, client, fmt.Sprintf("%s/api/games/%s/questions/next", baseURL, gameID))
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("/next status = %d, want %d", got, want)
	}
	body := readAllOrFatal(t, resp)

	if got, want := peekType(t, body), "break"; got != want {
		t.Fatalf("/next type = %q, want %q; body=%q", got, want, body)
	}
	var b breakItemRes
	if err := json.Unmarshal(body, &b); err != nil {
		t.Fatalf("decode /next break err = %v, want nil; body=%q", err, body)
	}

	return b
}

// readNextQuestionID calls /next, asserts the response is a question,
// and returns the question id without submitting an answer. Used to
// pin that the iterator advanced past a break.
func readNextQuestionID(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, gameID string,
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

	return q.ID
}

// createBreakPlayGame issues POST /api/games for the given quiz id
// and returns the created game id. Wraps the create-game boilerplate
// so the parent tests don't have to nest the JSON decode block twice.
func createBreakPlayGame(
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
// decoding the body. Pulled out so the "exhausted -> 404" check at
// the tail of the play loop doesn't replicate the close-body dance.
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

// postBreakSeen calls POST /api/games/{gameID}/breaks/{breakID}/seen
// and asserts a 204 No Content response. The /api/* surface gates on
// the session cookie alone; no CSRF token is needed (see addAPIRoutes).
// Both create and idempotent re-ack paths return 204, so the helper
// pins that status rather than taking it as a parameter.
func postBreakSeen(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, gameID string,
	breakID int64,
) {
	t.Helper()
	target := fmt.Sprintf("%s/api/games/%s/breaks/%d/seen", baseURL, gameID, breakID)
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

// TestBreaks_ResetCascadesSeenRows pins the FK cascade on
// game_seen_breaks.game_id: when an admin resets a player's game on a
// quiz (POST /admin/quizzes/{id}/players/{playerID}/reset, which calls
// store.GameStore.DeleteGamesForPlayerOnQuiz), the seen-break rows for
// that game must disappear too. Without the cascade those rows would
// be orphans referencing a deleted game id, which a future re-play
// could match against by accident.
func TestBreaks_ResetCascadesSeenRows(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	stores := setup.Stores

	adminPlayer := seedGameplayAdmin(ctx, t, setup.BaseURL, stores)
	qz := breakPlayQuiz(adminPlayer.ID)
	if err := stores.Quizzes.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}
	brk := &quiz.Break{QuizID: qz.ID, Position: 1, Text: "Halfway through!"}
	if err := stores.Quizzes.CreateBreak(ctx, brk); err != nil {
		t.Fatalf("CreateBreak err = %v, want nil", err)
	}

	// Drive the game through Q1 -> break ack so a game_seen_breaks
	// row exists. The HTTP client path mirrors how a real reset
	// would land in production - everything goes through the same
	// handlers a player would hit.
	client := playerClient(t)
	gameID := createBreakPlayGame(ctx, t, client, setup.BaseURL, qz.ID)
	answerNextCorrect(ctx, t, client, setup.BaseURL, gameID, qz)
	readNextBreak(ctx, t, client, setup.BaseURL, gameID)
	postBreakSeen(ctx, t, client, setup.BaseURL, gameID, brk.ID)

	seenBefore, err := stores.Games.ListSeenBreakIDsByGame(ctx, gameID)
	if err != nil {
		t.Fatalf("ListSeenBreakIDsByGame (before) err = %v, want nil", err)
	}
	if got, want := len(seenBefore), 1; got != want {
		t.Fatalf("seen rows before reset = %d, want %d", got, want)
	}

	// Look up the player id from the cookie jar - the anonymous
	// player is whichever row the EnsurePlayer middleware just
	// minted for the cookie. fetchSelfPlayerID is a small helper
	// that calls GET /api/players/me with the cookie.
	playerID := fetchSelfPlayerID(ctx, t, client, setup.BaseURL)

	// Reset the player's game on the quiz. This is the same
	// transaction the admin /reset button triggers.
	if dErr := stores.Games.DeleteGamesForPlayerOnQuiz(ctx, playerID, qz.ID); dErr != nil {
		t.Fatalf("DeleteGamesForPlayerOnQuiz err = %v, want nil", dErr)
	}

	seenAfter, err := stores.Games.ListSeenBreakIDsByGame(ctx, gameID)
	if err != nil {
		t.Fatalf("ListSeenBreakIDsByGame (after) err = %v, want nil", err)
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
