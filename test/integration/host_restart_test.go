package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/livesession"
)

// postHostRestart posts the confirm-and-restart control (#853): /host with the
// picked quiz_id plus restart=true, seeding a CSRF token from a page the host can
// load. Returns the response for the caller to assert on.
func postHostRestart(
	ctx context.Context, t *testing.T, host *http.Client, baseURL string, quizID int64,
) *http.Response {
	t.Helper()
	token := fetchCSRFToken(ctx, t, host, baseURL+"/admin/quizzes")

	return httpPostForm(ctx, t, host, baseURL+"/host", url.Values{
		"csrf_token": {token},
		"quiz_id":    {strconv.FormatInt(quizID, 10)},
		"restart":    {"true"},
	})
}

// postHostLive posts the plain Host live control (#851): /host with the picked
// quiz_id and no restart flag.
func postHostLive(
	ctx context.Context, t *testing.T, host *http.Client, baseURL string, quizID int64,
) *http.Response {
	t.Helper()
	token := fetchCSRFToken(ctx, t, host, baseURL+"/admin/quizzes")

	return httpPostForm(ctx, t, host, baseURL+"/host", url.Values{
		"csrf_token": {token},
		"quiz_id":    {strconv.FormatInt(quizID, 10)},
	})
}

// getQuizViewHTML fetches GET /admin/quizzes/{id} on the (host) client and
// returns the response status and body.
func getQuizViewHTML(
	ctx context.Context, t *testing.T, client *http.Client, baseURL string, quizID int64,
) (int, string) {
	t.Helper()
	resp := httpGet(ctx, t, client, baseURL+"/admin/quizzes/"+strconv.FormatInt(quizID, 10))
	defer closeBody(t, resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read quiz view body: %v", err)
	}

	return resp.StatusCode, string(body)
}

// TestHostRestart_QuizViewGatesModal pins the quiz-view gating end-to-end (#853):
// with a game in flight, GET /admin/quizzes/{id} renders the restart modal and
// the hidden restart=true field; with no running game the same view shows the
// plain Host live form with neither.
func TestHostRestart_QuizViewGatesModal(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegrationWithEnv(t, map[string]string{
		"SESSION_RUNNER_BEAT": "250ms",
		"REVEAL_DELAY":        "200ms",
	})
	baseURL := setup.BaseURL

	quizA := seedRunnerLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-view-a")
	quizB := seedRunnerLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-view-b")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "host-view-host", "host-view-pass-123")

	restartField := `name="restart"`
	restartModal := "modal-restart-host-" + strconv.FormatInt(quizB.ID, 10)

	// With no running session, quiz B's view shows the plain Host live form and no
	// restart affordance.
	status, body := getQuizViewHTML(ctx, t, host, baseURL, quizB.ID)
	if status != http.StatusOK {
		t.Fatalf("quiz view (no running) status = %d, want %d", status, http.StatusOK)
	}
	if !strings.Contains(body, "Host live") {
		t.Error("quiz view (no running) missing the Host live control")
	}
	if strings.Contains(body, restartField) {
		t.Error("quiz view (no running) rendered the restart hidden field")
	}
	if strings.Contains(body, restartModal) {
		t.Error("quiz view (no running) rendered the restart modal")
	}

	// Open a room on quiz A and start it so a game is in flight.
	codeA := createSession(ctx, t, host, baseURL, quizA.ID)
	player := newAnonClient(t)
	joinSession(ctx, t, player, baseURL, codeA, "Solo")
	startSession(ctx, t, host, baseURL, codeA)
	waitForPhase(ctx, t, player, baseURL, codeA, "round_intro")

	// Now quiz B's view gates Host live behind the confirm-and-restart modal.
	status, body = getQuizViewHTML(ctx, t, host, baseURL, quizB.ID)
	if status != http.StatusOK {
		t.Fatalf("quiz view (running) status = %d, want %d", status, http.StatusOK)
	}
	for _, want := range []string{restartModal, restartField, `value="true"`, "End and start"} {
		if !strings.Contains(body, want) {
			t.Errorf("quiz view (running) missing %q", want)
		}
	}
}

// TestHostRestart_EndsRunningAndOpensNewRoom pins the confirm-and-restart happy
// path end-to-end (#853): with a game running on quiz A, posting /host with
// restart=true and quiz B ends the running session and 303-redirects the host to
// a NEW lobby (a different join code) hosting quiz B, leaving the old session
// finished.
func TestHostRestart_EndsRunningAndOpensNewRoom(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegrationWithEnv(t, map[string]string{
		"SESSION_RUNNER_BEAT": "250ms",
		"REVEAL_DELAY":        "200ms",
	})
	baseURL := setup.BaseURL

	quizA := seedRunnerLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-restart-a")
	quizB := seedRunnerLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-restart-b")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "host-restart-host", "host-restart-pass-123")

	// Open a room on quiz A and start it so a game is in flight (round_intro).
	codeA := createSession(ctx, t, host, baseURL, quizA.ID)
	player := newAnonClient(t)
	joinSession(ctx, t, player, baseURL, codeA, "Solo")
	startSession(ctx, t, host, baseURL, codeA)
	waitForPhase(ctx, t, player, baseURL, codeA, "round_intro")

	// The host confirms the restart onto quiz B: a 303 to a NEW lobby.
	resp := postHostRestart(ctx, t, host, baseURL, quizB.ID)
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("restart status = %d, want %d", got, want)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/host/") {
		t.Fatalf("restart redirect = %q, want a /host/{code} target", loc)
	}
	codeB := strings.TrimPrefix(loc, "/host/")
	if codeB == codeA {
		t.Fatalf("restart reused the old join code %q, want a new room", codeB)
	}

	// The new room hosts quiz B and is in the lobby (the host starts it later).
	newRoom, err := setup.Stores.LiveSessions.GetSessionByJoinCode(ctx, codeB)
	if err != nil {
		t.Fatalf("GetSessionByJoinCode (new) err = %v, want nil", err)
	}
	if newRoom.QuizID == nil {
		t.Fatalf("new room QuizID = nil, want %d (quiz B)", quizB.ID)
	}
	if got, want := *newRoom.QuizID, quizB.ID; got != want {
		t.Errorf("new room QuizID = %d, want %d (quiz B)", got, want)
	}
	if got, want := newRoom.Phase, livesession.PhaseLobby; got != want {
		t.Errorf("new room phase = %q, want %q", got, want)
	}

	// The old session is terminally finished.
	old, err := setup.Stores.LiveSessions.GetSessionByJoinCode(ctx, codeA)
	if err != nil {
		t.Fatalf("GetSessionByJoinCode (old) err = %v, want nil", err)
	}
	if got, want := old.Phase, livesession.PhaseFinished; got != want {
		t.Errorf("old session phase = %q, want %q (ended on restart)", got, want)
	}
}

// TestHostRestart_PlainHostLiveWhileRunningIsNoOp pins that a plain Host live
// post (no restart flag) while a game is running is the #851 in-flight no-op: it
// 303s back to the SAME running room and the running session is untouched, so a
// stray pick never disrupts a live game.
func TestHostRestart_PlainHostLiveWhileRunningIsNoOp(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegrationWithEnv(t, map[string]string{
		"SESSION_RUNNER_BEAT": "250ms",
		"REVEAL_DELAY":        "200ms",
	})
	baseURL := setup.BaseURL

	quizA := seedRunnerLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-noop-a")
	quizB := seedRunnerLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-noop-b")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "host-noop-host", "host-noop-pass-123")

	codeA := createSession(ctx, t, host, baseURL, quizA.ID)
	player := newAnonClient(t)
	joinSession(ctx, t, player, baseURL, codeA, "Solo")
	startSession(ctx, t, host, baseURL, codeA)
	waitForPhase(ctx, t, player, baseURL, codeA, "round_intro")

	// A plain Host live (no restart) while running returns the running room.
	resp := postHostLive(ctx, t, host, baseURL, quizB.ID)
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("plain host-live status = %d, want %d", got, want)
	}
	if got, want := resp.Header.Get("Location"), "/host/"+codeA; got != want {
		t.Errorf("plain host-live redirect = %q, want %q (the running room)", got, want)
	}

	// The running room is untouched: still on quiz A, not finished.
	still, err := setup.Stores.LiveSessions.GetSessionByJoinCode(ctx, codeA)
	if err != nil {
		t.Fatalf("GetSessionByJoinCode err = %v, want nil", err)
	}
	if still.QuizID == nil {
		t.Fatal("running room QuizID = nil, want quiz A")
	}
	if got, want := *still.QuizID, quizA.ID; got != want {
		t.Errorf("running room QuizID = %d, want %d (stray pick must not re-arm)", got, want)
	}
	if still.Phase == livesession.PhaseFinished {
		t.Error("running room was finished by a plain host-live, want it left running")
	}
}

// TestHostRestart_SoloLeavesRunningUntouched pins that a restart onto a solo quiz
// bounces to the quiz list and never ends the running game (#853): the target is
// validated before anything is torn down, so the host is not stranded.
func TestHostRestart_SoloLeavesRunningUntouched(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegrationWithEnv(t, map[string]string{
		"SESSION_RUNNER_BEAT": "250ms",
		"REVEAL_DELAY":        "200ms",
	})
	baseURL := setup.BaseURL

	quizA := seedRunnerLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-restart-solo-a")
	solo := seedSoloQuiz(ctx, t, setup.Stores.Quizzes, "host-restart-solo-b")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "host-restart-solo-host", "host-restart-solo-123")

	codeA := createSession(ctx, t, host, baseURL, quizA.ID)
	player := newAnonClient(t)
	joinSession(ctx, t, player, baseURL, codeA, "Solo")
	startSession(ctx, t, host, baseURL, codeA)
	waitForPhase(ctx, t, player, baseURL, codeA, "round_intro")

	// A restart onto a solo (unhostable) quiz bounces to the quiz list.
	resp := postHostRestart(ctx, t, host, baseURL, solo.ID)
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("solo restart status = %d, want %d", got, want)
	}
	if got, want := resp.Header.Get("Location"), "/admin/quizzes"; got != want {
		t.Errorf("solo restart redirect = %q, want %q", got, want)
	}

	// The running session is untouched: still on quiz A, not finished.
	still, err := setup.Stores.LiveSessions.GetSessionByJoinCode(ctx, codeA)
	if err != nil {
		t.Fatalf("GetSessionByJoinCode err = %v, want nil", err)
	}
	if still.Phase == livesession.PhaseFinished {
		t.Error("running session was finished by a rejected solo restart, want it left running")
	}
	if still.QuizID == nil {
		t.Fatal("running room QuizID = nil, want quiz A")
	}
	if got, want := *still.QuizID, quizA.ID; got != want {
		t.Errorf("running room QuizID = %d, want %d (unchanged)", got, want)
	}
}
