package integration_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/livesession"
)

// TestHostSession_CreatesEmptyRoom pins the session-first create (#836): a host
// posts to /host with no quiz_id and opens an empty room (quiz_id NULL, the "no
// game running yet" staging state), getting a 303 to /host/{code} they can load.
func TestHostSession_CreatesEmptyRoom(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "host-empty-room", "host-empty-pass-123")

	// Seed the CSRF nonce from a page the host can load, then post with no quiz_id.
	token := fetchCSRFToken(ctx, t, host, baseURL+"/admin/quizzes")
	resp := httpPostForm(ctx, t, host, baseURL+"/host", url.Values{
		"csrf_token": {token},
	})
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("host empty-room create status = %d, want %d", got, want)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/host/") {
		t.Fatalf("host empty-room redirect = %q, want a /host/{code} target", loc)
	}
	code := strings.TrimPrefix(loc, "/host/")
	if code == "" {
		t.Fatal("host empty-room redirected to /host/ with no code")
	}

	// The room exists with no quiz and is in the lobby.
	sess, err := setup.Stores.LiveSessions.GetSessionByJoinCode(ctx, code)
	if err != nil {
		t.Fatalf("GetSessionByJoinCode err = %v, want nil", err)
	}
	if sess.QuizID != nil {
		t.Errorf("empty room QuizID = %d, want nil (no game running yet)", *sess.QuizID)
	}
	if got, want := sess.Phase, livesession.PhaseLobby; got != want {
		t.Errorf("empty room phase = %q, want %q", got, want)
	}

	// The host can load the lobby it was redirected to, and the empty room
	// renders the staging picker (so the host can pick the first quiz from the
	// lobby) plus the End session control (so the host can close the room).
	status, body := getHostLobbyHTML(ctx, t, host, baseURL, code)
	if status != http.StatusOK {
		t.Errorf("empty-room lobby status = %d, want %d", status, http.StatusOK)
	}
	for _, want := range []string{
		"data-start-quiz-picker",
		"data-next-quiz-form",
		"data-end-session-form",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("empty-room lobby missing %q", want)
		}
	}
	// The empty room seeds the component's hasQuiz false so it renders the
	// staging picker straight away rather than the Start controls (#836).
	if !strings.Contains(body, "hostLobby(") || !strings.Contains(body, ", false)") {
		t.Error("empty-room lobby should seed hasQuiz=false into the component")
	}

	// The JSON state read the host page polls must not panic on a quiz-less room:
	// it returns 200 with the quiz field omitted (the host is a participant).
	resp = httpGet(ctx, t, host, baseURL+"/api/sessions/"+code+"/state")
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("empty-room state status = %d, want %d", got, want)
	}
	stateBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read empty-room state body err = %v, want nil", err)
	}
	if strings.Contains(string(stateBody), `"quiz"`) {
		t.Errorf("empty-room state body = %s, want no quiz field (omitted for a quiz-less room)", stateBody)
	}
}

// TestHostSession_EndClosesRoom pins the End happy path (#836): the owning host
// posts to /host/{code}/end, the room is terminally finished, and the host is
// 303-redirected back to the lobby.
func TestHostSession_EndClosesRoom(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-end")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "host-end-host", "host-end-pass-123")
	code := createSession(ctx, t, host, baseURL, qz.ID)

	token := fetchCSRFToken(ctx, t, host, baseURL+"/host/"+code)
	resp := httpPostForm(ctx, t, host, baseURL+"/host/"+code+"/end", url.Values{
		"csrf_token": {token},
	})
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("host end status = %d, want %d", got, want)
	}
	if got, want := resp.Header.Get("Location"), "/host/"+code; got != want {
		t.Errorf("host end redirect = %q, want %q", got, want)
	}

	ended, err := setup.Stores.LiveSessions.GetSessionByJoinCode(ctx, code)
	if err != nil {
		t.Fatalf("GetSessionByJoinCode err = %v, want nil", err)
	}
	if got, want := ended.Phase, livesession.PhaseFinished; got != want {
		t.Errorf("room phase after end = %q, want %q", got, want)
	}
}

// TestHostSession_EndAuthz pins the End access rules (#836): an anonymous visitor
// without a CSRF token is forbidden, and a foreign host (a second host who does
// not own this room) 404s, so the code stays opaque to a host who does not own it.
func TestHostSession_EndAuthz(t *testing.T) {
	t.Parallel()

	const foreignEmail = "host-end-authz-other@example.test"
	ctx, setup := setupIntegrationWithEnv(t, map[string]string{"ADMIN_EMAILS": foreignEmail})
	baseURL := setup.BaseURL
	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-end-authz")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "host-end-authz-host", "host-end-authz-123")
	code := createSession(ctx, t, host, baseURL, qz.ID)

	foreign := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyViaLinkAndMint(ctx, t, foreign, baseURL, setup.DBURI, "host-end-authz-other", "host-end-other-123")

	t.Run("anonymous visitor without a CSRF token is forbidden", func(t *testing.T) {
		t.Parallel()
		anon := &http.Client{
			Jar:           mustJar(t),
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		}
		// The CSRF middleware wraps the auth gate (it runs first), so a tokenless
		// anon POST is rejected before any auth-state-leaking redirect to /login.
		resp := httpPostForm(ctx, t, anon, baseURL+"/host/"+code+"/end", url.Values{})
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusForbidden; got != want {
			t.Errorf("anon end status = %d, want %d", got, want)
		}
	})

	t.Run("a foreign host cannot end another host's room", func(t *testing.T) {
		t.Parallel()
		// The foreign host cannot GET the lobby (it 404s), so seed their CSRF nonce
		// from a page they can load - the admin quiz list.
		token := fetchCSRFToken(ctx, t, foreign, baseURL+"/admin/quizzes")
		resp := httpPostForm(ctx, t, foreign, baseURL+"/host/"+code+"/end", url.Values{
			"csrf_token": {token},
		})
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("foreign host end status = %d, want %d", got, want)
		}
	})
}
