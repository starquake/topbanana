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

// hostJoinURL returns the join URL the big screen's QR encodes, given the
// server base URL and code. Mirrors host.joinPathPrefix; kept local so the
// test fails loudly if the host package changes the path without updating
// the player join contract.
func hostJoinURL(baseURL, code string) string {
	return baseURL + "/join/" + code
}

// httpPostForm posts a urlencoded form on the client and returns the
// response. The caller closes the body.
func httpPostForm(
	ctx context.Context, t *testing.T, client *http.Client, target string, form url.Values,
) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest %s err = %v, want nil", target, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s err = %v, want nil", target, err)
	}

	return resp
}

// seedSoloQuiz seeds a mode='solo' quiz attributed to the seeded admin.
func seedSoloQuiz(ctx context.Context, t *testing.T, quizzes quiz.Store, slug string) *quiz.Quiz {
	t.Helper()
	qz := &quiz.Quiz{
		Title:             "Solo " + slug,
		Slug:              slug,
		Description:       "self-paced",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeSolo,
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "A", Correct: true}, {Text: "B"}}},
		},
	}
	if err := quizzes.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz solo err = %v, want nil", err)
	}

	return qz
}

// getHostBigScreenHTML fetches GET /host/{code} on the (host) client and returns
// the response status and body.
func getHostBigScreenHTML(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, code string,
) (int, string) {
	t.Helper()
	resp := httpGet(ctx, t, client, baseURL+"/host/"+code)
	defer closeBody(t, resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read host big-screen body: %v", err)
	}

	return resp.StatusCode, string(body)
}

// TestHostBigScreen_RendersCodeQuizAndQR drives the host flow: a host opens a
// session, then loads the big screen and finds the room code, the quiz title,
// and a server-rendered QR SVG that encodes the join URL.
func TestHostBigScreen_RendersCodeQuizAndQR(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-render")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "host-render-host", "host-render-pass-123")

	code := createSession(ctx, t, host, baseURL, qz.ID)

	status, body := getHostBigScreenHTML(ctx, t, host, baseURL, code)
	if got, want := status, http.StatusOK; got != want {
		t.Fatalf("host big screen status = %d, want %d", got, want)
	}
	if !strings.Contains(body, code) {
		t.Errorf("host big screen missing room code %q", code)
	}
	if !strings.Contains(body, qz.Title) {
		t.Errorf("host big screen missing quiz title %q", qz.Title)
	}
	if !strings.Contains(body, "<svg") || !strings.Contains(body, "Join QR code") {
		t.Error("host big screen missing the server-rendered QR svg")
	}
	if want := hostJoinURL(baseURL, code); !strings.Contains(body, want) {
		t.Errorf("host big screen missing join url %q (the QR deep link)", want)
	}
	// The typed-code guidance points players at the bare enter-code URL (host
	// + /join, no scheme, no code) rather than the deep link (#750). Assert
	// the bare URL and the guidance text together as one fragment so the check
	// pins the guidance line, not the scan card's deep link (which also
	// contains the bare host+/join as a prefix substring).
	entryDisplay := strings.TrimPrefix(strings.TrimPrefix(baseURL+"/join", "https://"), "http://")
	if want := ">" + entryDisplay + "</span> and enter the code above"; !strings.Contains(body, want) {
		t.Errorf("host big screen missing typed-code guidance %q", want)
	}
	// The mid-game join hint (#852, enlarged + moved bottom-center #864) keeps the
	// join URL + code on the big screen while a quiz is running (shown via
	// showsJoinHint() in the in-game phases), so a latecomer reading the TV can
	// still join. Its markup ships at GET regardless of phase; pin the strip by
	// its hook and its distinct "with code" lead-in line (the lobby's typed-code
	// guidance above uses different wording), with the code in a separate element.
	if !strings.Contains(body, "data-join-hint") {
		t.Error("host big screen missing the mid-game join hint strip (#852)")
	}
	if want := ">" + entryDisplay + "</span> with code"; !strings.Contains(body, want) {
		t.Errorf("host big screen join hint missing the join URL lead-in line %q", want)
	}
	// The host can close the room from the big screen: the End session control is
	// rendered across the live phases (#836).
	if !strings.Contains(body, `data-testid="end-session-form"`) {
		t.Error("host big screen missing the End session control")
	}
	// The big-screen brand logo itself returns to the admin console (#850):
	// the header's only navigation is the home logo, which now points at
	// /admin, so the separate "Manage" cross-link (#844) is gone. The big
	// screen deliberately has no account cluster / log out.
	if !strings.Contains(body, `<a href="/admin" aria-label="Top Banana!"`) {
		t.Error("host big screen brand logo should link to /admin")
	}
	if strings.Contains(body, ">Manage</a>") {
		t.Error("host big screen header should not render the redundant Manage link (#850)")
	}
	if strings.Contains(body, `action="/logout"`) {
		t.Error(
			"host big screen should not render a log-out form (the big screen is a shared screen, not a session surface)",
		)
	}
	// A preselected-quiz lobby seeds the component's hasQuiz true so it renders
	// its Start controls without flashing the staging picker before the first
	// state read (#836 no-flash hydration).
	if !strings.Contains(body, "hostBigScreen(") || !strings.Contains(body, ", true)") {
		t.Error("host big screen should seed hasQuiz=true into the component for a preselected quiz")
	}
}

// TestHostBigScreen_RendersPickQuizLink pins the list-driven pick flow (#851,
// #889): an empty staging room renders the "pick a live quiz" link to
// /host/quizzes (where the host picks a quiz and "Host this" arms it back in
// this room), and the old in-lobby dropdown picker is gone.
func TestHostBigScreen_RendersPickQuizLink(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "host-picker-host", "host-picker-pass-123")

	// Open an empty staging room (no quiz) so the lobby shows the pick-quiz link.
	token := fetchCSRFToken(ctx, t, host, baseURL+"/admin/quizzes")
	resp := httpPostForm(ctx, t, host, baseURL+"/host", url.Values{"csrf_token": {token}})
	defer closeBody(t, resp.Body)
	code := strings.TrimPrefix(resp.Header.Get("Location"), "/host/")
	if code == "" {
		t.Fatal("empty-room create did not redirect to /host/{code}")
	}

	status, body := getHostBigScreenHTML(ctx, t, host, baseURL, code)
	if got, want := status, http.StatusOK; got != want {
		t.Fatalf("host big screen status = %d, want %d", got, want)
	}
	// The new list-driven flow: a link to the host quiz list.
	if !strings.Contains(body, `data-testid="pick-quiz-link"`) {
		t.Error("host big screen missing the pick-a-live-quiz link")
	}
	if !strings.Contains(body, `href="/host/quizzes"`) {
		t.Error("host big screen pick-quiz link should point at /host/quizzes")
	}
	// The old in-lobby dropdown picker is gone.
	for _, gone := range []string{"data-start-quiz-picker", "data-next-quiz-form", "data-next-quiz-select"} {
		if strings.Contains(body, gone) {
			t.Errorf("host big screen should no longer render the removed dropdown picker %q", gone)
		}
	}
}

// TestHostBigScreen_StateReflectsLiveJoinAndReady is the integration backbone for
// the live big-screen view: the page refreshes off GET /api/sessions/{code}/state, so
// a player joining and readying via REST (MP-4's join UI does not exist in
// this slice) must surface on the host's authoritative state read - which is
// exactly what the big-screen JS polls. The e2e test asserts the DOM updates; here
// we pin the data path the host page consumes.
func TestHostBigScreen_StateReflectsLiveJoinAndReady(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-live")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "host-live-host", "host-live-pass-123")
	code := createSession(ctx, t, host, baseURL, qz.ID)

	// Empty lobby first: the host can read state with no players.
	if got, want := len(getSessionState(ctx, t, host, baseURL, code).Players), 0; got != want {
		t.Fatalf("initial player count = %d, want %d", got, want)
	}

	// A player joins and readies up via REST.
	alice := newAnonClient(t)
	joinSession(ctx, t, alice, baseURL, code, "Alice")
	setReady(ctx, t, alice, baseURL, code, true)

	// The host's state read (the lobby's data source) now shows Alice ready.
	state := getSessionState(ctx, t, host, baseURL, code)
	if got, want := len(state.Players), 1; got != want {
		t.Fatalf("player count = %d, want %d", got, want)
	}
	if got, want := state.Players[0].DisplayName, "Alice"; got != want {
		t.Errorf("player name = %q, want %q", got, want)
	}
	if !state.Players[0].IsReady {
		t.Error("Alice should be ready in the host's state read")
	}
}

// TestHostLive_CreatesSessionAndRedirects exercises the "Host live" entry: with
// no active room, POST /host with a live quiz id opens a session and
// 303-redirects the host to /host/{code} (StartHosting case 1, #851).
func TestHostLive_CreatesSessionAndRedirects(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-hostlive")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "hostlive-host", "hostlive-pass-123")

	// Seed the CSRF nonce on the jar from the quiz view, then post the entry.
	token := fetchCSRFToken(ctx, t, host, baseURL+"/admin/quizzes/"+strconv.FormatInt(qz.ID, 10))
	resp := httpPostForm(ctx, t, host, baseURL+"/host", url.Values{
		"csrf_token": {token},
		"quiz_id":    {strconv.FormatInt(qz.ID, 10)},
	})
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("host live status = %d, want %d", got, want)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/host/") {
		t.Fatalf("host live redirect = %q, want a /host/{code} target", loc)
	}
	code := strings.TrimPrefix(loc, "/host/")
	if code == "" {
		t.Fatal("host live redirected to /host/ with no code")
	}
	// The host can load the lobby it was redirected to.
	if status, _ := getHostBigScreenHTML(ctx, t, host, baseURL, code); status != http.StatusOK {
		t.Errorf("redirected lobby status = %d, want %d", status, http.StatusOK)
	}
}

// TestHostLive_ArmsExistingEmptyRoom pins the one-room-per-host orchestration
// (StartHosting case 2, #851): with an active empty staging room, POST /host
// with a live quiz_id arms+starts THAT same room rather than spawning a second
// one - the redirect goes to the existing join code and no new session is added.
func TestHostLive_ArmsExistingEmptyRoom(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-hostlive-reuse")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "hostlive-reuse-host", "hostlive-reuse-123")

	// Open an empty staging room first (no quiz_id).
	token := fetchCSRFToken(ctx, t, host, baseURL+"/admin/quizzes")
	openResp := httpPostForm(ctx, t, host, baseURL+"/host", url.Values{"csrf_token": {token}})
	defer closeBody(t, openResp.Body)
	emptyCode := strings.TrimPrefix(openResp.Header.Get("Location"), "/host/")
	if emptyCode == "" {
		t.Fatal("empty-room create did not redirect to /host/{code}")
	}

	// Now "Host live" the quiz: it must arm the EXISTING empty room, not open a
	// second one.
	resp := httpPostForm(ctx, t, host, baseURL+"/host", url.Values{
		"csrf_token": {token},
		"quiz_id":    {strconv.FormatInt(qz.ID, 10)},
	})
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("host live (reuse) status = %d, want %d", got, want)
	}
	if got, want := resp.Header.Get("Location"), "/host/"+emptyCode; got != want {
		t.Errorf("host live (reuse) redirect = %q, want %q (the existing room)", got, want)
	}

	// The existing room is now armed onto the picked quiz (no second room
	// spawned) but still in the lobby, NOT started (#863): the host presses Start
	// when players are in.
	sess, err := setup.Stores.LiveSessions.GetSessionByJoinCode(ctx, emptyCode)
	if err != nil {
		t.Fatalf("GetSessionByJoinCode err = %v, want nil", err)
	}
	if sess.QuizID == nil {
		t.Fatalf("reused room QuizID = nil, want %d", qz.ID)
	}
	if got, want := *sess.QuizID, qz.ID; got != want {
		t.Errorf("reused room QuizID = %d, want %d (the picked quiz)", got, want)
	}
	if got, want := string(sess.Phase), "lobby"; got != want {
		t.Errorf("reused room Phase = %q, want %q (armed but waiting in the lobby, #863)", got, want)
	}
	if sess.StartedAt != nil {
		t.Errorf(
			"reused room StartedAt = %v, want nil (not started until the host presses Start, #863)",
			sess.StartedAt,
		)
	}
}

// TestHostLive_RejectsSoloQuiz pins that the "Host live" entry only opens live
// quizzes: a solo quiz id bounces back to the quiz list rather than opening a
// dead lobby (#851).
func TestHostLive_RejectsSoloQuiz(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	soloQz := seedSoloQuiz(ctx, t, setup.Stores.Quizzes, "host-solo")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "host-solo-host", "host-solo-pass-123")

	token := fetchCSRFToken(ctx, t, host, baseURL+"/admin/quizzes/"+strconv.FormatInt(soloQz.ID, 10))
	resp := httpPostForm(ctx, t, host, baseURL+"/host", url.Values{
		"csrf_token": {token},
		"quiz_id":    {strconv.FormatInt(soloQz.ID, 10)},
	})
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("solo host live status = %d, want %d", got, want)
	}
	if got, want := resp.Header.Get("Location"), "/admin/quizzes"; got != want {
		t.Errorf("solo host live redirect = %q, want %q", got, want)
	}
}

// TestHostBigScreen_Authz pins the host-surface access rules: an anonymous
// visitor is bounced to login, a foreign host's session 404s, the owning host
// can start the game (303 back to the big screen), and a foreign or unknown code
// 404s on start so the code stays opaque.
func TestHostBigScreen_Authz(t *testing.T) {
	t.Parallel()

	// A foreign host (a second host who does not own this session) registers
	// against the same server. ADMIN_EMAILS promotes them to admin so they
	// clear the RequireGameHost gate - the point of the check is that even a
	// legitimate host gets 404 on a session they do not own, not that a plain
	// player is gated. The promotion now happens at verify time (#785), so
	// the foreign host proves its email through the real /verify-email link
	// (registerVerifyViaLinkAndMint) rather than a direct DB stamp. It is
	// registered + minted in the parent body (before the parallel subtests)
	// so its DB writes are serialized and it sidesteps the per-IP login
	// cooldown.
	const foreignEmail = "host-authz-other@example.test"
	ctx, setup := setupIntegrationWithEnv(t, map[string]string{"ADMIN_EMAILS": foreignEmail})
	baseURL := setup.BaseURL
	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-authz")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "host-authz-host", "host-authz-pass-123")
	code := createSession(ctx, t, host, baseURL, qz.ID)

	foreign := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyViaLinkAndMint(ctx, t, foreign, baseURL, setup.DBURI, "host-authz-other", "host-authz-other-123")

	t.Run("anonymous visitor is redirected to login", func(t *testing.T) {
		t.Parallel()
		anon := &http.Client{
			Jar:           mustJar(t),
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		}
		resp := httpGet(ctx, t, anon, baseURL+"/host/"+code)
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
			t.Errorf("anon host big screen status = %d, want %d", got, want)
		}
		if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/login") {
			t.Errorf("anon host big screen redirect = %q, want /login", loc)
		}
	})

	t.Run("a foreign host cannot open another host's big screen", func(t *testing.T) {
		t.Parallel()
		status, _ := getHostBigScreenHTML(ctx, t, foreign, baseURL, code)
		if got, want := status, http.StatusNotFound; got != want {
			t.Errorf("foreign host big screen status = %d, want %d", got, want)
		}
	})

	t.Run("a foreign host cannot start another host's session", func(t *testing.T) {
		t.Parallel()
		// The foreign host cannot GET the lobby (it 404s), so seed their CSRF
		// nonce from a page they can load - the admin quiz list.
		token := fetchCSRFToken(ctx, t, foreign, baseURL+"/admin/quizzes")
		resp := httpPostForm(ctx, t, foreign, baseURL+"/host/"+code+"/start", url.Values{
			"csrf_token": {token},
		})
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("foreign host start status = %d, want %d", got, want)
		}
	})

	t.Run("start on an unknown code is 404 for the host", func(t *testing.T) {
		t.Parallel()
		token := fetchCSRFToken(ctx, t, host, baseURL+"/host/"+code)
		resp := httpPostForm(ctx, t, host, baseURL+"/host/NOPE99/start", url.Values{
			"csrf_token": {token},
		})
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("start unknown-code status = %d, want %d", got, want)
		}
	})
}

// TestHostStart_BeginsSessionAndRedirects pins the host start happy path: the
// owning host posts to /host/{code}/start, the session is marked started, and
// the host is 303-redirected back to the lobby. A second start is idempotent
// (already-started is treated as success), so a double click does not error.
func TestHostStart_BeginsSessionAndRedirects(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-start")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "host-start-host", "host-start-pass-123")
	code := createSession(ctx, t, host, baseURL, qz.ID)

	token := fetchCSRFToken(ctx, t, host, baseURL+"/host/"+code)
	start := func() *http.Response {
		return httpPostForm(ctx, t, host, baseURL+"/host/"+code+"/start", url.Values{
			"csrf_token": {token},
		})
	}

	resp := start()
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("start status = %d, want %d", got, want)
	}
	if got, want := resp.Header.Get("Location"), "/host/"+code; got != want {
		t.Errorf("start redirect = %q, want %q", got, want)
	}

	again := start()
	defer closeBody(t, again.Body)
	if got, want := again.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("repeat start status = %d, want %d (already-started is idempotent)", got, want)
	}
}
