package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
)

// getHostQuizListHTML fetches GET /host/quizzes on the (host) client and
// returns the response status and body.
func getHostQuizListHTML(
	ctx context.Context, t *testing.T, client *http.Client, baseURL string,
) (int, string) {
	t.Helper()
	resp := httpGet(ctx, t, client, baseURL+"/host/quizzes")
	defer closeBody(t, resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read host quiz-list body: %v", err)
	}

	return resp.StatusCode, string(body)
}

// TestHostQuizList_ListsRunnableLiveQuizzes pins the host quiz picker (#889): a
// signed-in host GETs /host/quizzes and sees only the runnable quizzes - live
// mode with at least one question. A solo quiz and an empty live quiz are both
// filtered out, and each listed card carries the "Host this" form posting its
// quiz_id to /host.
func TestHostQuizList_ListsRunnableLiveQuizzes(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	live := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-list-live")
	solo := seedSoloQuiz(ctx, t, setup.Stores.Quizzes, "host-list-solo")
	emptyLive := &quiz.Quiz{
		Title:             "Empty live host-list",
		Slug:              "host-list-empty",
		Description:       "no questions yet",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeLive,
	}
	if err := setup.Stores.Quizzes.CreateQuiz(ctx, emptyLive); err != nil {
		t.Fatalf("CreateQuiz empty live err = %v, want nil", err)
	}

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "host-list-host", "host-list-pass-123")

	status, body := getHostQuizListHTML(ctx, t, host, baseURL)
	if got, want := status, http.StatusOK; got != want {
		t.Fatalf("host quiz-list status = %d, want %d", got, want)
	}

	// The runnable live quiz appears, with a form posting its quiz_id to /host.
	if !strings.Contains(body, live.Title) {
		t.Errorf("host quiz-list missing runnable live quiz title %q", live.Title)
	}
	if !strings.Contains(body, `action="/host"`) {
		t.Error(`host quiz-list missing form action="/host"`)
	}
	if want := `name="quiz_id" value="` + strconv.FormatInt(live.ID, 10) + `"`; !strings.Contains(body, want) {
		t.Errorf("host quiz-list missing hidden input %q", want)
	}
	if !strings.Contains(body, "Host this") {
		t.Error(`host quiz-list missing "Host this" action`)
	}

	// The solo quiz and the empty live quiz are both filtered out.
	if strings.Contains(body, solo.Title) {
		t.Errorf("host quiz-list shows solo quiz %q, want it filtered out", solo.Title)
	}
	if strings.Contains(body, emptyLive.Title) {
		t.Errorf("host quiz-list shows empty live quiz %q, want it filtered out", emptyLive.Title)
	}
}

// TestHostQuizList_ChangeQuizWhenRunning pins the running-game branch of the
// host picker (#889 slice 4): with a game in flight each card swaps its plain
// "Host this" form for a "Change quiz" button that opens the confirm-and-restart
// modal, so a pick never silently no-ops over the running game (#851). The modal
// posts restart=true to /host.
func TestHostQuizList_ChangeQuizWhenRunning(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegrationWithEnv(t, map[string]string{
		"SESSION_RUNNER_BEAT": "250ms",
		"REVEAL_DELAY":        "200ms",
	})
	baseURL := setup.BaseURL

	quizA := seedRunnerLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-change-a")
	quizB := seedRunnerLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-change-b")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "host-change-host", "host-change-pass-123")

	// Open a room on quiz A and start it so a game is in flight.
	codeA := createSession(ctx, t, host, baseURL, quizA.ID)
	player := newAnonClient(t)
	joinSession(ctx, t, player, baseURL, codeA, "Solo")
	startSession(ctx, t, host, baseURL, codeA)
	waitForPhase(ctx, t, player, baseURL, codeA, "round_intro")

	status, body := getHostQuizListHTML(ctx, t, host, baseURL)
	if got, want := status, http.StatusOK; got != want {
		t.Fatalf("host quiz-list (running) status = %d, want %d", got, want)
	}

	// The running state replaces "Host this" with "Change quiz" and mounts the
	// confirm-and-restart modal for the other quiz, posting restart=true.
	if !strings.Contains(body, "Change quiz") {
		t.Error(`host quiz-list (running) missing "Change quiz" action`)
	}
	restartModal := "modal-restart-host-" + strconv.FormatInt(quizB.ID, 10)
	for _, want := range []string{restartModal, `name="restart"`, `value="true"`, "End and start"} {
		if !strings.Contains(body, want) {
			t.Errorf("host quiz-list (running) missing %q", want)
		}
	}
	if strings.Contains(body, "Host this") {
		t.Error(`host quiz-list (running) still shows "Host this", want it replaced by "Change quiz"`)
	}
}

// TestHostQuizList_GatesAnonymous pins the host gate on the picker: an
// unauthenticated GET /host/quizzes is bounced to login, the same as the other
// host GET routes (mirrors TestHostBigScreen_Authz).
func TestHostQuizList_GatesAnonymous(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	anon := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp := httpGet(ctx, t, anon, baseURL+"/host/quizzes")
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("anon host quiz-list status = %d, want %d", got, want)
	}
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Errorf("anon host quiz-list redirect = %q, want /login", loc)
	}
}

// hostCreateRoom signs the host into a fresh room via POST /host (optionally
// arming quizID) and returns the room's join code from the redirect.
func hostCreateRoom(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, quizID string,
) string {
	t.Helper()
	form := url.Values{"csrf_token": {fetchCSRFToken(ctx, t, client, baseURL+"/admin/quizzes")}}
	if quizID != "" {
		form.Set("quiz_id", quizID)
	}
	resp := httpPostForm(ctx, t, client, baseURL+"/host", form)
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("POST /host status = %d, want %d", got, want)
	}
	code := strings.TrimPrefix(resp.Header.Get("Location"), "/host/")
	if code == "" {
		t.Fatalf("POST /host redirect = %q, want /host/{code}", resp.Header.Get("Location"))
	}

	return code
}

// TestHostQuizList_LiveSessionIndicator pins the persistent "Session live"
// header indicator (#889 slice 3): the host picker surfaces the host's active
// room (with its armed quiz title, or none for an empty room) and links back to
// it, and shows nothing when the host has no room. One host evolves its single
// room across the cases (a host has at most one active room, and signing in
// several hosts from one IP trips the login rate limiter); StartHosting is
// one-room-aware, so arming a quiz reuses the empty room rather than opening a
// new one.
func TestHostQuizList_LiveSessionIndicator(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	const marker = `data-testid="session-live-indicator"`

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "host-indicator", "host-indicator-pass-123")

	// No room yet: no indicator.
	if _, body := getHostQuizListHTML(ctx, t, host, baseURL); strings.Contains(body, marker) {
		t.Error("host quiz-list shows the live-session indicator with no active room")
	}

	// Open an empty room: the indicator appears, names no quiz, and links back.
	emptyCode := hostCreateRoom(ctx, t, host, baseURL, "")
	_, body := getHostQuizListHTML(ctx, t, host, baseURL)
	if !strings.Contains(body, marker) {
		t.Fatal("host quiz-list missing the live-session indicator for an empty room")
	}
	if !strings.Contains(body, "Session live") {
		t.Error("indicator missing the 'Session live' label")
	}
	if want := `href="/host/` + emptyCode + `"`; !strings.Contains(body, want) {
		t.Errorf("indicator missing link %q back to the room", want)
	}

	// Arm a live quiz in the same room: the indicator now names the quiz. Scope
	// the title check to the indicator anchor - the armed quiz also renders as a
	// picker card, so an unscoped body match would pass even if the indicator
	// failed to resolve the title.
	live := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-indicator-quiz")
	armedCode := hostCreateRoom(ctx, t, host, baseURL, strconv.FormatInt(live.ID, 10))
	_, body = getHostQuizListHTML(ctx, t, host, baseURL)
	indicator := body
	if i := strings.Index(indicator, marker); i >= 0 {
		indicator = indicator[i:]
		if end := strings.Index(indicator, "</a>"); end >= 0 {
			indicator = indicator[:end]
		}
	}
	if !strings.Contains(indicator, live.Title) {
		t.Errorf("live-session indicator missing armed quiz title %q", live.Title)
	}
	if want := `href="/host/` + armedCode + `"`; !strings.Contains(body, want) {
		t.Errorf("indicator missing link %q back to the room", want)
	}
}
