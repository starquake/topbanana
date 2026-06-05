package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
)

// sessionStateRes is the decode target for GET /api/sessions/{code}/state -
// the frozen MP-1 lobby DTO (#678).
type sessionStateRes struct {
	JoinCode string                  `json:"joinCode"`
	Phase    string                  `json:"phase"`
	HostID   int64                   `json:"hostId"`
	Players  []sessionStatePlayerRes `json:"players"`
	Quiz     sessionStateQuizRes     `json:"quiz"`
}

type sessionStatePlayerRes struct {
	PlayerID    int64  `json:"playerId"`
	DisplayName string `json:"displayName"`
	IsReady     bool   `json:"isReady"`
}

type sessionStateQuizRes struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	QuestionCount int    `json:"questionCount"`
}

// newAnonClient returns a cookie-jar-backed client so EnsurePlayer mints a
// distinct anonymous players row per client (each gets its own session
// cookie on the first /api call).
func newAnonClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}

	return &http.Client{Jar: jar}
}

// seedLiveQuiz seeds a mode='live' quiz attributed to the seeded admin.
func seedLiveQuiz(ctx context.Context, t *testing.T, quizzes quiz.Store, slug string) *quiz.Quiz {
	t.Helper()
	qz := &quiz.Quiz{
		Title:             "Live " + slug,
		Slug:              slug,
		Description:       "hosted only",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeLive,
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "A", Correct: true}, {Text: "B"}}},
			{Text: "Q2", Position: 2, Options: []*quiz.Option{{Text: "C", Correct: true}, {Text: "D"}}},
		},
	}
	if err := quizzes.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz live err = %v, want nil", err)
	}

	return qz
}

// createSession opens a session via POST /api/sessions on the host client
// and returns the join code.
func createSession(ctx context.Context, t *testing.T, client *http.Client, baseURL string, quizID int64) string {
	t.Helper()
	resp := httpPostJSON(ctx, t, client, baseURL+"/api/sessions", fmt.Sprintf(`{"quizId": %d}`, quizID))
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusCreated; got != want {
		t.Fatalf("create session status = %d, want %d", got, want)
	}
	var body struct {
		JoinCode string `json:"joinCode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode create session: %v", err)
	}
	if body.JoinCode == "" {
		t.Fatal("create session returned empty join code")
	}

	return body.JoinCode
}

// joinSession joins a session under displayName and returns the resolved
// display name (which may be a petname fallback on collision).
func joinSession(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, code, displayName string,
) string {
	t.Helper()
	body := fmt.Sprintf(`{"displayName": %q}`, displayName)
	resp := httpPostJSON(ctx, t, client, fmt.Sprintf("%s/api/sessions/%s/join", baseURL, code), body)
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("join status = %d, want %d", got, want)
	}
	var res struct {
		DisplayName string `json:"displayName"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode join: %v", err)
	}

	return res.DisplayName
}

// getSessionState reads GET /api/sessions/{code}/state on the client.
func getSessionState(ctx context.Context, t *testing.T, client *http.Client, baseURL, code string) sessionStateRes {
	t.Helper()
	resp := httpGet(ctx, t, client, fmt.Sprintf("%s/api/sessions/%s/state", baseURL, code))
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("state status = %d, want %d", got, want)
	}
	var state sessionStateRes
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatalf("decode state: %v", err)
	}

	return state
}

// setReady toggles the client's ready flag and asserts the 204.
func setReady(ctx context.Context, t *testing.T, client *http.Client, baseURL, code string, ready bool) {
	t.Helper()
	body := fmt.Sprintf(`{"ready": %t}`, ready)
	resp := httpPostJSON(ctx, t, client, fmt.Sprintf("%s/api/sessions/%s/ready", baseURL, code), body)
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusNoContent; got != want {
		t.Fatalf("ready status = %d, want %d", got, want)
	}
}

// TestSessionLobby_HappyPath drives the full MP-1 lobby loop end to end: a
// host opens a session for a live quiz, two anonymous players join, ready
// toggles surface in GET /state, and the state DTO carries the frozen
// shape (phase, hostId, roster, quiz meta).
func TestSessionLobby_HappyPath(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "lobby-happy")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "lobby-host", "lobby-host-pass-123")

	code := createSession(ctx, t, host, baseURL, qz.ID)

	alice := newAnonClient(t)
	bob := newAnonClient(t)
	joinSession(ctx, t, alice, baseURL, code, "Alice")
	joinSession(ctx, t, bob, baseURL, code, "Bob")

	// Alice marks ready; the state read by Bob reflects it.
	setReady(ctx, t, alice, baseURL, code, true)

	state := getSessionState(ctx, t, bob, baseURL, code)
	if got, want := state.Phase, "lobby"; got != want {
		t.Errorf("phase = %q, want %q", got, want)
	}
	if got, want := state.JoinCode, code; got != want {
		t.Errorf("joinCode = %q, want %q", got, want)
	}
	if state.HostID == 0 {
		t.Error("hostId = 0, want the host player id")
	}
	if got, want := state.Quiz.ID, qz.ID; got != want {
		t.Errorf("quiz.id = %d, want %d", got, want)
	}
	if got, want := state.Quiz.QuestionCount, 2; got != want {
		t.Errorf("quiz.questionCount = %d, want %d", got, want)
	}
	if got, want := len(state.Players), 2; got != want {
		t.Fatalf("len(players) = %d, want %d", got, want)
	}
	ready := map[string]bool{}
	for _, p := range state.Players {
		ready[p.DisplayName] = p.IsReady
	}
	if !ready["Alice"] {
		t.Error("Alice should be ready in state")
	}
	if ready["Bob"] {
		t.Error("Bob should not be ready in state")
	}
}

// TestSessionLobby_JoinCodeIsCaseInsensitive pins that a code entered in a
// different case still resolves: codes are minted uppercase but read off a
// TV / typed by hand, so join and state lookups normalize the inbound code
// rather than 404ing on a lowercase entry.
func TestSessionLobby_JoinCodeIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "lobby-case")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "lobby-case-host", "lobby-case-pass-123")

	code := createSession(ctx, t, host, baseURL, qz.ID)
	lower := strings.ToLower(code)
	if lower == code {
		t.Fatalf("join code %q has no lowercase form to exercise", code)
	}

	// A player joins using the lowercased code; joinSession asserts the 200.
	alice := newAnonClient(t)
	joinSession(ctx, t, alice, baseURL, lower, "Alice")

	// State read with the lowercased code resolves and echoes the canonical
	// (uppercase) code.
	state := getSessionState(ctx, t, alice, baseURL, lower)
	if got, want := state.JoinCode, code; got != want {
		t.Errorf("joinCode = %q, want canonical %q", got, want)
	}
}

// TestSessionLobby_DisplayNameCollisionFallsBackToPetname pins that a
// second joiner asking for an already-taken display name still lands in
// the lobby under a different (petname) name.
func TestSessionLobby_DisplayNameCollisionFallsBackToPetname(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "lobby-collision")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "collision-host", "collision-host-pass-123")
	code := createSession(ctx, t, host, baseURL, qz.ID)

	first := newAnonClient(t)
	second := newAnonClient(t)
	got1 := joinSession(ctx, t, first, baseURL, code, "Twins")
	got2 := joinSession(ctx, t, second, baseURL, code, "Twins")

	if got, want := got1, "Twins"; got != want {
		t.Errorf("first join name = %q, want %q", got, want)
	}
	if got2 == "Twins" {
		t.Error("second join kept the colliding name; want a petname fallback")
	}

	state := getSessionState(ctx, t, first, baseURL, code)
	if got, want := len(state.Players), 2; got != want {
		t.Errorf("len(players) = %d, want %d (both joined despite collision)", got, want)
	}
}

// TestSessionLobby_JoinAfterStartIs409 pins the late-join gate (MP-10): once
// the host starts the session the lobby closes, so a fresh player joining the
// running session gets a 409 rather than landing mid-game (v1 has no late
// join).
func TestSessionLobby_JoinAfterStartIs409(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "lobby-late-join")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "late-join-host", "late-join-pass-123")

	code := createSession(ctx, t, host, baseURL, qz.ID)

	// An early player joins the lobby; the host then starts the game.
	early := newAnonClient(t)
	joinSession(ctx, t, early, baseURL, code, "Early")
	startSession(ctx, t, host, baseURL, code)

	// A latecomer joining the now-running session is rejected with 409.
	late := newAnonClient(t)
	resp := httpPostJSON(ctx, t, late, fmt.Sprintf("%s/api/sessions/%s/join", baseURL, code), `{"displayName":"Late"}`)
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusConflict; got != want {
		t.Errorf("late join status = %d, want %d", got, want)
	}
}

// TestSessionLobby_JoinCodesAreUnique opens two sessions and asserts the
// generated join codes differ.
func TestSessionLobby_JoinCodesAreUnique(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "lobby-unique")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "unique-host", "unique-host-pass-123")

	code1 := createSession(ctx, t, host, baseURL, qz.ID)
	code2 := createSession(ctx, t, host, baseURL, qz.ID)
	if code1 == code2 {
		t.Errorf("two sessions share join code %q, want distinct", code1)
	}
}

// TestSessionLobby_Authz pins the per-endpoint access rules: only a
// host/admin can create; a solo quiz and an unknown quiz are rejected; a
// non-participant cannot read state or set ready.
func TestSessionLobby_Authz(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	liveQz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "authz-live")

	soloQz := &quiz.Quiz{
		Title:             "Authz Solo",
		Slug:              "authz-solo",
		Description:       "solo",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeSolo,
		Questions: []*quiz.Question{
			{Text: "Q", Position: 1, Options: []*quiz.Option{{Text: "A", Correct: true}, {Text: "B"}}},
		},
	}
	if err := setup.Stores.Quizzes.CreateQuiz(ctx, soloQz); err != nil {
		t.Fatalf("CreateQuiz solo err = %v, want nil", err)
	}

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "authz-host", "authz-host-pass-123")

	t.Run("anonymous player cannot create a session", func(t *testing.T) {
		t.Parallel()
		anon := newAnonClient(t)
		resp := httpPostJSON(ctx, t, anon, baseURL+"/api/sessions", fmt.Sprintf(`{"quizId": %d}`, liveQz.ID))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusForbidden; got != want {
			t.Errorf("anon create status = %d, want %d", got, want)
		}
	})

	t.Run("host cannot host a solo quiz", func(t *testing.T) {
		t.Parallel()
		resp := httpPostJSON(ctx, t, host, baseURL+"/api/sessions", fmt.Sprintf(`{"quizId": %d}`, soloQz.ID))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("solo create status = %d, want %d", got, want)
		}
	})

	t.Run("host cannot host an unknown quiz", func(t *testing.T) {
		t.Parallel()
		resp := httpPostJSON(ctx, t, host, baseURL+"/api/sessions", `{"quizId": 999999}`)
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("unknown create status = %d, want %d", got, want)
		}
	})

	t.Run("non-participant cannot read state or set ready", func(t *testing.T) {
		t.Parallel()
		code := createSession(ctx, t, host, baseURL, liveQz.ID)
		stranger := newAnonClient(t)

		stateResp := httpGet(ctx, t, stranger, fmt.Sprintf("%s/api/sessions/%s/state", baseURL, code))
		defer closeBody(t, stateResp.Body)
		if got, want := stateResp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("stranger state status = %d, want %d", got, want)
		}

		readyResp := httpPostJSON(
			ctx, t, stranger, fmt.Sprintf("%s/api/sessions/%s/ready", baseURL, code), `{"ready": true}`,
		)
		defer closeBody(t, readyResp.Body)
		if got, want := readyResp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("stranger ready status = %d, want %d", got, want)
		}
	})

	t.Run("state on an unknown join code is 404", func(t *testing.T) {
		t.Parallel()
		stranger := newAnonClient(t)
		resp := httpGet(ctx, t, stranger, baseURL+"/api/sessions/NOPE99/state")
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("unknown-code state status = %d, want %d", got, want)
		}
	})
}

// mustJar builds a cookie jar or fails the test.
func mustJar(t *testing.T) *cookiejar.Jar {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}

	return jar
}
