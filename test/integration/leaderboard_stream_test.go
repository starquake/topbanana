//go:build integration

package integration_test

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// TestLeaderboardStream_Integration covers the SSE leaderboard pipe (#239)
// end-to-end:
//
//  1. Seed a quiz with a question + two correct options via the store.
//  2. Subscribe to GET /api/quizzes/{slug}/leaderboard/stream from client A.
//  3. Drain the initial-snapshot event.
//  4. Have a second client submit an answer that scores.
//  5. Assert client A receives a second event whose payload reflects the
//     submitted answer.
//
// The test enforces a short overall timeout — if the hub Publish never
// reaches the subscriber the test fails loudly instead of hanging.
func TestLeaderboardStream_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	db, err := sql.Open("sqlite", srv.DBURI)
	if err != nil {
		t.Fatalf("sql.Open err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v, want nil", cerr)
		}
	})
	stores := store.New(db, slog.Default())

	// Seed: one quiz, one question, two options (one correct).
	qz := &quiz.Quiz{
		Title:             "Stream Quiz",
		Slug:              "stream-quiz",
		Description:       "seed for the SSE leaderboard test",
		CreatedByPlayerID: seededAdminID,
		Questions: []*quiz.Question{
			{
				Text:     "What is 2+2?",
				Position: 1,
				Options: []*quiz.Option{
					{Text: "4", Correct: true},
					{Text: "5"},
				},
			},
		},
	}
	if cerr := stores.Quizzes.CreateQuiz(ctx, qz); cerr != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", cerr)
	}

	// Client A subscribes to the stream. We use a separate http.Client
	// (no cookie jar shared with the answer-submitting client B) so the
	// two represent different EnsurePlayer-minted anonymous players.
	streamCtx, streamCancel := context.WithTimeout(ctx, 10*time.Second)
	defer streamCancel()

	streamURL := fmt.Sprintf("%s/api/quizzes/%s-%d/leaderboard/stream", srv.BaseURL, qz.Slug, qz.ID)
	streamReq, err := http.NewRequestWithContext(streamCtx, http.MethodGet, streamURL, nil)
	if err != nil {
		t.Fatalf("NewRequest stream err = %v, want nil", err)
	}
	streamReq.Header.Set("Accept", "text/event-stream")

	streamResp, err := http.DefaultClient.Do(streamReq)
	if err != nil {
		t.Fatalf("stream Do err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := streamResp.Body.Close(); cerr != nil {
			t.Errorf("stream Body.Close err = %v, want nil", cerr)
		}
	})

	if got, want := streamResp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("stream status = %d, want %d", got, want)
	}
	if got, want := streamResp.Header.Get("Content-Type"), "text/event-stream"; !strings.HasPrefix(got, want) {
		t.Errorf("stream Content-Type = %q, want prefix %q", got, want)
	}

	scanner := bufio.NewScanner(streamResp.Body)

	// Drain the initial snapshot. It should contain the quiz ID and an
	// empty entries list (no answers yet).
	initial := readSSEEvent(t, scanner)
	if got := initial.QuizID; got != qz.ID {
		t.Errorf("initial event quizId = %d, want %d", got, qz.ID)
	}
	if got, want := len(initial.Entries), 0; got != want {
		t.Errorf("initial event entries len = %d, want %d (no answers yet)", got, want)
	}

	// Client B: a fresh HTTP client with its own jar that the EnsurePlayer
	// middleware will treat as a brand-new anonymous visitor.
	submitURL := srv.BaseURL + "/api/games"
	createReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, submitURL,
		strings.NewReader(fmt.Sprintf(`{"quizId": %d}`, qz.ID)),
	)
	if err != nil {
		t.Fatalf("NewRequest create err = %v, want nil", err)
	}
	createReq.Header.Set("Content-Type", "application/json")
	// Need a cookie jar so the session cookie persists between
	// /api/games create and the subsequent /api/games/<id>/.../answers POST.
	clientB := newCookieJarClient(t)
	createResp, err := clientB.Do(createReq)
	if err != nil {
		t.Fatalf("create game Do err = %v, want nil", err)
	}
	gameID := decodeGameID(t, createResp)
	if cerr := createResp.Body.Close(); cerr != nil {
		t.Errorf("create game Body.Close err = %v, want nil", cerr)
	}

	// CreateGame fires a leaderboard publish (#335) so client A sees B
	// land on the board at score 0 / in-progress before any answer
	// commits. Drain that event before submitting the answer; otherwise
	// the answer-commit event below would race with this one and the
	// score assertion could read the pre-answer tick by mistake.
	afterJoin := readSSEEvent(t, scanner)
	if got, want := len(afterJoin.Entries), 1; got != want {
		t.Fatalf("after-join entries len = %d, want %d (CreateGame must publish a tick)", got, want)
	}
	if got, want := afterJoin.Entries[0].Score, 0; got != want {
		t.Errorf("after-join top score = %d, want %d (no answers yet)", got, want)
	}
	if got, want := afterJoin.Entries[0].InProgress, true; got != want {
		t.Errorf("after-join inProgress = %v, want %v", got, want)
	}

	// Fetch the question (server records started_at on first /next call,
	// otherwise the answer-submit is rejected because the question wasn't
	// served).
	nextReq, err := http.NewRequestWithContext(
		ctx, http.MethodGet,
		fmt.Sprintf("%s/api/games/%s/questions/next", srv.BaseURL, gameID), nil,
	)
	if err != nil {
		t.Fatalf("NewRequest next err = %v, want nil", err)
	}
	nextResp, err := clientB.Do(nextReq)
	if err != nil {
		t.Fatalf("next Do err = %v, want nil", err)
	}
	pick := decodeQuestionAndPickCorrect(t, nextResp, qz)
	if cerr := nextResp.Body.Close(); cerr != nil {
		t.Errorf("next Body.Close err = %v, want nil", cerr)
	}

	// Submit the correct option.
	answerURL := fmt.Sprintf("%s/api/games/%s/questions/%d/answers", srv.BaseURL, gameID, pick.QuestionID)
	answerBody := fmt.Sprintf(`{"optionId": %d}`, pick.OptionID)
	answerReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, answerURL, strings.NewReader(answerBody),
	)
	if err != nil {
		t.Fatalf("NewRequest answer err = %v, want nil", err)
	}
	answerReq.Header.Set("Content-Type", "application/json")
	answerResp, err := clientB.Do(answerReq)
	if err != nil {
		t.Fatalf("answer Do err = %v, want nil", err)
	}
	if got, want := answerResp.StatusCode, http.StatusOK; got != want {
		t.Errorf("answer status = %d, want %d", got, want)
	}
	if cerr := answerResp.Body.Close(); cerr != nil {
		t.Errorf("answer Body.Close err = %v, want nil", cerr)
	}

	// Client A should now see a second SSE event with the new score.
	second := readSSEEvent(t, scanner)
	if got := second.QuizID; got != qz.ID {
		t.Errorf("second event quizId = %d, want %d", got, qz.ID)
	}
	if got, want := len(second.Entries), 1; got != want {
		t.Fatalf("second event entries len = %d, want %d (client B's answer should be on the board)", got, want)
	}
	if got := second.Entries[0].Score; got <= 0 {
		t.Errorf("second event top score = %d, want > 0 (correct answer should score)", got)
	}
	// Bound the is_completed predicate (#335): client B answered the
	// only quiz question, so their entry must flip out of the
	// in-progress state. A regression that hard-codes inProgress=true
	// (or returns 0 from the CASE) would pass the pre-answer integration
	// test; this assertion catches that.
	if got, want := second.Entries[0].InProgress, false; got != want {
		t.Errorf("second event top inProgress = %v, want %v (game completed)", got, want)
	}
}

// TestLeaderboardStream_HeartbeatKeepsConnectionAlivePastWriteTimeout
// pins the #244 follow-up that disables the per-request write deadline
// on the SSE handler and emits a periodic comment heartbeat. Before the
// fix, the HTTP server's 10s WriteTimeout would kill every leaderboard
// stream at exactly 10.003s — silent (no error to the client beyond
// NS_ERROR_PARTIAL_TRANSFER in the browser) but fatal to anything that
// expected the stream to stay open while no answers were committing.
//
// The test opens a stream against a fresh quiz with no players, waits
// past the 10s WriteTimeout AND past one heartbeat tick (25s), and
// asserts:
//   - the connection is still alive at the end of the window (no
//     premature EOF — proves the WriteTimeout fix),
//   - at least one heartbeat comment (`:` line) arrived after the
//     initial snapshot (proves the heartbeat is firing).
//
// 27s wall-clock makes this one of the slower integration tests, but
// it runs in parallel so the suite's critical path doesn't grow.
func TestLeaderboardStream_HeartbeatKeepsConnectionAlivePastWriteTimeout(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	db, err := sql.Open("sqlite", srv.DBURI)
	if err != nil {
		t.Fatalf("sql.Open err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v, want nil", cerr)
		}
	})
	stores := store.New(db, slog.Default())

	qz := &quiz.Quiz{
		Title:             "Heartbeat Quiz",
		Slug:              "heartbeat-quiz",
		Description:       "seed for the SSE write-deadline regression test",
		CreatedByPlayerID: seededAdminID,
		Questions: []*quiz.Question{
			{
				Text:     "Anything?",
				Position: 1,
				Options:  []*quiz.Option{{Text: "yes", Correct: true}, {Text: "no"}},
			},
		},
	}
	if cerr := stores.Quizzes.CreateQuiz(ctx, qz); cerr != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", cerr)
	}

	// 27s is past the 10s WriteTimeout AND past the 25s heartbeat
	// interval, so a working server emits at least one heartbeat
	// inside the window. The request context cancels at the end, which
	// unblocks the scanner loop below.
	const window = 27 * time.Second
	streamCtx, streamCancel := context.WithTimeout(ctx, window)
	defer streamCancel()

	streamURL := fmt.Sprintf("%s/api/quizzes/%s-%d/leaderboard/stream", srv.BaseURL, qz.Slug, qz.ID)
	streamReq, err := http.NewRequestWithContext(streamCtx, http.MethodGet, streamURL, nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	streamReq.Header.Set("Accept", "text/event-stream")

	start := time.Now()
	streamResp, err := http.DefaultClient.Do(streamReq)
	if err != nil {
		t.Fatalf("stream Do err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := streamResp.Body.Close(); cerr != nil && !errors.Is(cerr, context.DeadlineExceeded) {
			t.Errorf("stream Body.Close err = %v", cerr)
		}
	})

	if got, want := streamResp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("stream status = %d, want %d", got, want)
	}

	// Drain lines as they arrive. heartbeatLines counts SSE comment
	// frames (lines beginning with ":"); a single one past the 10s
	// mark proves the WriteTimeout fix is in place AND the heartbeat
	// is firing.
	scanner := bufio.NewScanner(streamResp.Body)
	var heartbeatLines int
	var sawInitialData bool
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data: "):
			sawInitialData = true
		case strings.HasPrefix(line, ":"):
			heartbeatLines++
		default:
			// Blank separator lines and "event:" framing — ignore.
		}
	}
	elapsed := time.Since(start)

	if !sawInitialData {
		t.Fatal("never received the initial-snapshot SSE event")
	}
	// The scanner only exits when the context cancels (after `window`)
	// or the server closes the body. If we got out in well under the
	// window, the server closed early — WriteTimeout still bites.
	if elapsed < window-2*time.Second {
		t.Fatalf("stream closed after %v, want stream to stay open the full %v window", elapsed, window)
	}
	if heartbeatLines == 0 {
		t.Errorf("got 0 heartbeat (`:` ...) lines in %v, want at least 1", window)
	}
}

// TestLeaderboardStream_UnknownQuiz_Returns404 locks in the initial-fetch
// error path: an unknown quiz ID must respond with a proper HTTP 404 (not
// a half-open text/event-stream that smuggles the error into the body).
// This is the regression guard for the fetchQuizLeaderboard refactor that
// stopped writing http.Error into the SSE body.
func TestLeaderboardStream_UnknownQuiz_Returns404(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	streamCtx, streamCancel := context.WithTimeout(ctx, 5*time.Second)
	defer streamCancel()

	// Slug shape (`{slug}-{id}`) must parse, but no such quiz exists.
	streamURL := srv.BaseURL + "/api/quizzes/does-not-exist-999999/leaderboard/stream"
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, streamURL, nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("Body.Close err = %v, want nil", cerr)
		}
	})

	if got, want := resp.StatusCode, http.StatusNotFound; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := resp.Header.Get("Content-Type"), "text/event-stream"; strings.HasPrefix(got, want) {
		t.Errorf("Content-Type = %q, must not start with %q (error path must not pose as SSE)", got, want)
	}
}

// TestQuizLeaderboard_ShowsParticipantBeforeAnyAnswer pins #335: a
// player who has clicked Start (POST /api/games) but has not yet
// submitted an answer must already appear on the GET /leaderboard
// response with a score of 0 and inProgress=true. Before #335 the
// leaderboard was empty until the first answer committed, which left
// the host and other players staring at "be the first" during the
// first ~10s of a session.
func TestQuizLeaderboard_ShowsParticipantBeforeAnyAnswer(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	db, err := sql.Open("sqlite", srv.DBURI)
	if err != nil {
		t.Fatalf("sql.Open err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v, want nil", cerr)
		}
	})
	stores := store.New(db, slog.Default())

	qz := &quiz.Quiz{
		Title:             "Pre-Answer Quiz",
		Slug:              "pre-answer-quiz",
		Description:       "seed for #335",
		CreatedByPlayerID: seededAdminID,
		Questions: []*quiz.Question{
			{
				Text:     "Anything?",
				Position: 1,
				Options:  []*quiz.Option{{Text: "yes", Correct: true}, {Text: "no"}},
			},
		},
	}
	if cerr := stores.Quizzes.CreateQuiz(ctx, qz); cerr != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", cerr)
	}

	client := newCookieJarClient(t)

	createReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, srv.BaseURL+"/api/games",
		strings.NewReader(fmt.Sprintf(`{"quizId": %d}`, qz.ID)),
	)
	if err != nil {
		t.Fatalf("NewRequest create err = %v, want nil", err)
	}
	createReq.Header.Set("Content-Type", "application/json")
	createResp, err := client.Do(createReq)
	if err != nil {
		t.Fatalf("create game Do err = %v, want nil", err)
	}
	_ = decodeGameID(t, createResp)
	if cerr := createResp.Body.Close(); cerr != nil {
		t.Errorf("create game Body.Close err = %v, want nil", cerr)
	}

	// Skip the /questions/next call: the point of the test is that the
	// leaderboard surfaces the player BEFORE any question is issued or
	// answered. Querying /leaderboard now must already include them.
	lbURL := fmt.Sprintf("%s/api/quizzes/%s-%d/leaderboard", srv.BaseURL, qz.Slug, qz.ID)
	lbReq, err := http.NewRequestWithContext(ctx, http.MethodGet, lbURL, nil)
	if err != nil {
		t.Fatalf("NewRequest leaderboard err = %v, want nil", err)
	}
	lbResp, err := client.Do(lbReq)
	if err != nil {
		t.Fatalf("leaderboard Do err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := lbResp.Body.Close(); cerr != nil {
			t.Errorf("leaderboard Body.Close err = %v, want nil", cerr)
		}
	})

	if got, want := lbResp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("leaderboard status = %d, want %d", got, want)
	}

	var payload leaderboardEventPayload
	if err = json.NewDecoder(lbResp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode leaderboard err = %v, want nil", err)
	}

	if got, want := len(payload.Entries), 1; got != want {
		t.Fatalf("len(entries) = %d, want %d (the player who just started must surface)", got, want)
	}
	entry := payload.Entries[0]
	if got, want := entry.Score, 0; got != want {
		t.Errorf("entry.Score = %d, want %d (no answers committed yet)", got, want)
	}
	if got, want := entry.InProgress, true; got != want {
		t.Errorf("entry.InProgress = %v, want %v", got, want)
	}
	if got, want := entry.Rank, 1; got != want {
		t.Errorf("entry.Rank = %d, want %d", got, want)
	}
	if got, want := entry.IsCurrentPlayer, true; got != want {
		t.Errorf("entry.IsCurrentPlayer = %v, want %v", got, want)
	}
}

// TestLeaderboardStream_NameUpdate_RepaintsSubscribers covers the
// claim-name flow's leaderboard fan-out (#239 follow-up):
//
//  1. Seed a single-question quiz.
//  2. Client B plays it to completion so they land on the leaderboard
//     with an auto-petname username.
//  3. Client A subscribes to the leaderboard stream and drains the
//     initial snapshot (which already shows client B's row).
//  4. Client B PATCHes /api/players/me with a chosen display name.
//  5. Client A must receive a fresh event whose entry for client B
//     carries the new username.
//
// Without the fan-out wired into PATCH /api/players/me, the third step
// would never repaint subscribed clients and this test would time out
// on readSSEEvent at the end.
func TestLeaderboardStream_NameUpdate_RepaintsSubscribers(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	db, err := sql.Open("sqlite", srv.DBURI)
	if err != nil {
		t.Fatalf("sql.Open err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v, want nil", cerr)
		}
	})
	stores := store.New(db, slog.Default())

	qz := &quiz.Quiz{
		Title:             "Name Update Quiz",
		Slug:              "name-update-quiz",
		Description:       "seed for the claim-name SSE fan-out test",
		CreatedByPlayerID: seededAdminID,
		Questions: []*quiz.Question{
			{
				Text:     "What is 2+2?",
				Position: 1,
				Options: []*quiz.Option{
					{Text: "4", Correct: true},
					{Text: "5"},
				},
			},
		},
	}
	if cerr := stores.Quizzes.CreateQuiz(ctx, qz); cerr != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", cerr)
	}

	// Client B plays the quiz to completion FIRST so they appear on the
	// leaderboard (which currently only lists completed games).
	clientB := newCookieJarClient(t)
	playSingleQuestionQuizToCompletion(ctx, t, srv.BaseURL, clientB, qz)

	// Capture client B's auto-assigned username from /api/players/me so
	// we know what to compare against in the post-PATCH event.
	originalName := getMyUsername(ctx, t, srv.BaseURL, clientB)

	// Client A subscribes and drains the initial snapshot. It should
	// already see client B's row.
	streamCtx, streamCancel := context.WithTimeout(ctx, 10*time.Second)
	defer streamCancel()

	streamURL := fmt.Sprintf("%s/api/quizzes/%s-%d/leaderboard/stream", srv.BaseURL, qz.Slug, qz.ID)
	streamReq, err := http.NewRequestWithContext(streamCtx, http.MethodGet, streamURL, nil)
	if err != nil {
		t.Fatalf("NewRequest stream err = %v, want nil", err)
	}
	streamResp, err := http.DefaultClient.Do(streamReq)
	if err != nil {
		t.Fatalf("stream Do err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := streamResp.Body.Close(); cerr != nil {
			t.Errorf("stream Body.Close err = %v, want nil", cerr)
		}
	})

	scanner := bufio.NewScanner(streamResp.Body)
	initial := readSSEEvent(t, scanner)
	if got, want := len(initial.Entries), 1; got != want {
		t.Fatalf("initial event entries len = %d, want %d (client B should be on the board)", got, want)
	}
	if got, want := initial.Entries[0].Username, originalName; got != want {
		t.Errorf("initial event username = %q, want %q (auto-petname)", got, want)
	}

	// Client B claims a custom display name.
	const claimedName = "renamed-player"
	patchReq, err := http.NewRequestWithContext(
		ctx, http.MethodPatch, srv.BaseURL+"/api/players/me",
		strings.NewReader(fmt.Sprintf(`{"username": %q}`, claimedName)),
	)
	if err != nil {
		t.Fatalf("NewRequest patch err = %v, want nil", err)
	}
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, err := clientB.Do(patchReq)
	if err != nil {
		t.Fatalf("patch Do err = %v, want nil", err)
	}
	if got, want := patchResp.StatusCode, http.StatusOK; got != want {
		t.Errorf("patch status = %d, want %d", got, want)
	}
	if cerr := patchResp.Body.Close(); cerr != nil {
		t.Errorf("patch Body.Close err = %v, want nil", cerr)
	}

	// Client A should receive a second event whose row carries the new
	// username.
	second := readSSEEvent(t, scanner)
	if got, want := len(second.Entries), 1; got != want {
		t.Fatalf("post-rename entries len = %d, want %d", got, want)
	}
	if got, want := second.Entries[0].Username, claimedName; got != want {
		t.Errorf("post-rename username = %q, want %q (claim should propagate via SSE)", got, want)
	}
}

// playSingleQuestionQuizToCompletion runs the full create-game / next-question /
// submit-answer sequence with the given client so the resulting game
// is "completed" by the store's leaderboard query definition. Single-
// question quiz only: the helper assumes one /next call is enough.
func playSingleQuestionQuizToCompletion(
	ctx context.Context,
	t *testing.T,
	baseURL string,
	client *http.Client,
	qz *quiz.Quiz,
) {
	t.Helper()

	createReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/api/games",
		strings.NewReader(fmt.Sprintf(`{"quizId": %d}`, qz.ID)),
	)
	if err != nil {
		t.Fatalf("NewRequest create err = %v, want nil", err)
	}
	createReq.Header.Set("Content-Type", "application/json")
	createResp, err := client.Do(createReq)
	if err != nil {
		t.Fatalf("create game Do err = %v, want nil", err)
	}
	gameID := decodeGameID(t, createResp)
	if cerr := createResp.Body.Close(); cerr != nil {
		t.Errorf("create game Body.Close err = %v, want nil", cerr)
	}

	nextReq, err := http.NewRequestWithContext(
		ctx, http.MethodGet,
		fmt.Sprintf("%s/api/games/%s/questions/next", baseURL, gameID), nil,
	)
	if err != nil {
		t.Fatalf("NewRequest next err = %v, want nil", err)
	}
	nextResp, err := client.Do(nextReq)
	if err != nil {
		t.Fatalf("next Do err = %v, want nil", err)
	}
	pick := decodeQuestionAndPickCorrect(t, nextResp, qz)
	if cerr := nextResp.Body.Close(); cerr != nil {
		t.Errorf("next Body.Close err = %v, want nil", cerr)
	}

	answerURL := fmt.Sprintf("%s/api/games/%s/questions/%d/answers", baseURL, gameID, pick.QuestionID)
	answerBody := fmt.Sprintf(`{"optionId": %d}`, pick.OptionID)
	answerReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, answerURL, strings.NewReader(answerBody),
	)
	if err != nil {
		t.Fatalf("NewRequest answer err = %v, want nil", err)
	}
	answerReq.Header.Set("Content-Type", "application/json")
	answerResp, err := client.Do(answerReq)
	if err != nil {
		t.Fatalf("answer Do err = %v, want nil", err)
	}
	if got, want := answerResp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("answer status = %d, want %d", got, want)
	}
	if cerr := answerResp.Body.Close(); cerr != nil {
		t.Errorf("answer Body.Close err = %v, want nil", cerr)
	}
}

// playerMeResponse is the JSON shape of GET /api/players/me. Only the
// username field is modelled here.
type playerMeResponse struct {
	Username string `json:"username"`
}

// getMyUsername hits GET /api/players/me with the given cookie-jar
// client and returns the username on file. The EnsurePlayer middleware
// mints a row on first contact, so this also doubles as the "create a
// player session" probe.
func getMyUsername(ctx context.Context, t *testing.T, baseURL string, client *http.Client) string {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/players/me", nil)
	if err != nil {
		t.Fatalf("NewRequest /me err = %v, want nil", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("/me Do err = %v, want nil", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("/me Body.Close err = %v, want nil", cerr)
		}
	}()

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("/me status = %d, want %d", got, want)
	}
	var out playerMeResponse
	if derr := json.NewDecoder(resp.Body).Decode(&out); derr != nil {
		t.Fatalf("/me decode err = %v, want nil", derr)
	}
	if out.Username == "" {
		t.Fatal("/me returned empty username")
	}

	return out.Username
}

// leaderboardEventEntry mirrors one row in the SSE leaderboard payload.
// Lifted to package scope so revive's nested-structs rule is happy.
type leaderboardEventEntry struct {
	PlayerID        int64  `json:"playerId"`
	Username        string `json:"username"`
	Score           int    `json:"score"`
	Rank            int    `json:"rank"`
	IsCurrentPlayer bool   `json:"isCurrentPlayer"`
	InProgress      bool   `json:"inProgress"`
}

// leaderboardEventPayload mirrors the JSON shape the SSE handler emits.
// Local to the integration test rather than shared so the test pins the
// wire contract independently of the production type names.
type leaderboardEventPayload struct {
	QuizID  int64                   `json:"quizId"`
	Entries []leaderboardEventEntry `json:"entries"`
}

// readSSEEvent consumes one `data: ...\n\n` event from the SSE stream
// and decodes the payload. SSE events end with a blank line; we scan
// line-by-line and stitch back the `data:` payload.
func readSSEEvent(t *testing.T, scanner *bufio.Scanner) leaderboardEventPayload {
	t.Helper()

	var dataLine string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if dataLine == "" {
				continue
			}

			break
		}
		if after, ok := strings.CutPrefix(line, "data: "); ok {
			dataLine = after
		}
	}
	if dataLine == "" {
		t.Fatal("no SSE event received before stream closed or timeout")
	}

	var payload leaderboardEventPayload
	if err := json.Unmarshal([]byte(dataLine), &payload); err != nil {
		t.Fatalf("Unmarshal SSE payload err = %v, body = %q", err, dataLine)
	}

	return payload
}

// decodeGameID reads {"id": "..."} from POST /api/games. Fatal on any
// parsing problem — the test cannot proceed without a game ID.
func decodeGameID(t *testing.T, resp *http.Response) string {
	t.Helper()

	if got, want := resp.StatusCode, http.StatusCreated; got != want {
		t.Fatalf("create game status = %d, want %d", got, want)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode game err = %v, want nil", err)
	}
	if out.ID == "" {
		t.Fatal("create game returned empty id")
	}

	return out.ID
}

// streamTestQuestionOption mirrors one option in the GET /next response.
// Test-local name (with the test-file prefix) so it doesn't collide with
// the same-shaped helper in gameplay_test.go.
type streamTestQuestionOption struct {
	ID int64 `json:"id"`
}

// streamTestQuestionResponse is the decode target for GET /next. Only the
// fields this test needs are modelled.
type streamTestQuestionResponse struct {
	ID      int64                      `json:"id"`
	Options []streamTestQuestionOption `json:"options"`
}

// pickedOption is the typed return for [decodeQuestionAndPickCorrect].
// Returning a struct (instead of (int64, int64)) keeps both
// nonamedreturns and confusing-results lint rules happy.
type pickedOption struct {
	QuestionID int64
	OptionID   int64
}

// decodeQuestionAndPickCorrect reads GET /api/games/.../questions/next
// and returns the question ID plus the seeded-correct option's ID for
// that question. The API shuffles option order per-game (#297), so
// "options[0]" is no longer reliably the correct answer — the helper
// walks the seeded quiz to find the option flagged Correct instead.
func decodeQuestionAndPickCorrect(t *testing.T, resp *http.Response, qz *quiz.Quiz) pickedOption {
	t.Helper()

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("next status = %d, want %d", got, want)
	}
	var out streamTestQuestionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode next err = %v", err)
	}
	if len(out.Options) == 0 {
		t.Fatal("next returned no options")
	}

	for _, q := range qz.Questions {
		if q.ID != out.ID {
			continue
		}
		for _, o := range q.Options {
			if o.Correct {
				return pickedOption{QuestionID: out.ID, OptionID: o.ID}
			}
		}
	}
	t.Fatalf("no Correct option in seeded quiz %d for question %d", qz.ID, out.ID)

	return pickedOption{}
}

// newCookieJarClient returns an http.Client with a fresh cookie jar so
// the test's two HTTP actors (SSE subscriber + answer submitter) are
// treated as distinct anonymous players by the EnsurePlayer middleware.
func newCookieJarClient(t *testing.T) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}

	return &http.Client{Jar: jar}
}
