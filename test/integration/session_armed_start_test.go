package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// armedStartStateRes decodes the parts of GET /state this file asserts on: the
// phase and the armed last-call deadline (#735). startAt is omitted from the
// JSON when no countdown is armed, so a nil pointer means "not armed".
type armedStartStateRes struct {
	Phase   string     `json:"phase"`
	StartAt *time.Time `json:"startAt"`
}

// armStart posts the host arm-start and asserts the status.
func armStart(ctx context.Context, t *testing.T, client *http.Client, baseURL, code string, wantStatus int) {
	t.Helper()
	resp := httpPostJSON(ctx, t, client, fmt.Sprintf("%s/api/sessions/%s/arm-start", baseURL, code), `{}`)
	defer closeBody(t, resp.Body)
	if got := resp.StatusCode; got != wantStatus {
		t.Fatalf("arm-start status = %d, want %d", got, wantStatus)
	}
}

// cancelStart posts the host cancel-start and asserts the status.
func cancelStart(ctx context.Context, t *testing.T, client *http.Client, baseURL, code string, wantStatus int) {
	t.Helper()
	resp := httpPostJSON(ctx, t, client, fmt.Sprintf("%s/api/sessions/%s/cancel-start", baseURL, code), `{}`)
	defer closeBody(t, resp.Body)
	if got := resp.StatusCode; got != wantStatus {
		t.Fatalf("cancel-start status = %d, want %d", got, wantStatus)
	}
}

// getArmedStartState reads GET /state on the client and decodes the phase +
// armed deadline.
func getArmedStartState(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, code string,
) armedStartStateRes {
	t.Helper()
	resp := httpGet(ctx, t, client, fmt.Sprintf("%s/api/sessions/%s/state", baseURL, code))
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("state status = %d, want %d", got, want)
	}
	var state armedStartStateRes
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatalf("decode state: %v", err)
	}

	return state
}

// TestSessionArmedStart_ArmAndCancel drives the host-armed last-call countdown
// (#735): arming surfaces the deadline in /state, a non-host is forbidden, and
// cancelling clears the deadline.
func TestSessionArmedStart_ArmAndCancel(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "armed-start")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "armed-host", "armed-host-pass-123")

	code := createSession(ctx, t, host, baseURL, qz.ID)

	player := newAnonClient(t)
	joinSession(ctx, t, player, baseURL, code, "Armed-Alice")

	// Before arming, /state carries no deadline.
	if got := getArmedStartState(ctx, t, player, baseURL, code); got.StartAt != nil {
		t.Errorf("startAt before arming = %v, want nil", got.StartAt)
	}

	// A non-host (the joined player) cannot arm: 403.
	armStart(ctx, t, player, baseURL, code, http.StatusForbidden)

	// The host arms: 204, and the deadline now surfaces to every participant.
	armStart(ctx, t, host, baseURL, code, http.StatusNoContent)
	armed := getArmedStartState(ctx, t, player, baseURL, code)
	if got, want := armed.Phase, "lobby"; got != want {
		t.Errorf("phase while armed = %q, want %q", got, want)
	}
	if armed.StartAt == nil {
		t.Fatal("startAt after host arm = nil, want a deadline")
	}

	// A non-host cannot cancel: 403.
	cancelStart(ctx, t, player, baseURL, code, http.StatusForbidden)

	// The host cancels: 204, and the deadline is cleared in /state.
	cancelStart(ctx, t, host, baseURL, code, http.StatusNoContent)
	if got := getArmedStartState(ctx, t, player, baseURL, code).StartAt; got != nil {
		t.Errorf("startAt after host cancel = %v, want nil", got)
	}
}

// TestSessionArmedStart_LobbyPhaseOnly pins that arm / cancel are idempotent
// no-ops (204) once the session has left the lobby - the host started the game,
// so there is no countdown to arm or cancel.
func TestSessionArmedStart_LobbyPhaseOnly(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "armed-start-phase")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "armed-phase-host", "armed-phase-pass-123")

	code := createSession(ctx, t, host, baseURL, qz.ID)

	player := newAnonClient(t)
	joinSession(ctx, t, player, baseURL, code, "Phase-Alice")

	// Host starts the game now.
	startSession(ctx, t, host, baseURL, code)

	// Arm / cancel after the game has begun are idempotent no-ops (204), never
	// re-arm the now-running game.
	armStart(ctx, t, host, baseURL, code, http.StatusNoContent)
	cancelStart(ctx, t, host, baseURL, code, http.StatusNoContent)
	if got := getArmedStartState(ctx, t, player, baseURL, code).StartAt; got != nil {
		t.Errorf("startAt after start = %v, want nil (no countdown once running)", got)
	}
}

// createEmptyRoom opens a host room with no quiz (quizId omitted) and returns
// its join code.
func createEmptyRoom(ctx context.Context, t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	resp := httpPostJSON(ctx, t, client, baseURL+"/api/sessions", `{}`)
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusCreated; got != want {
		t.Fatalf("create empty room status = %d, want %d", got, want)
	}
	var body struct {
		JoinCode string `json:"joinCode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode create empty room: %v", err)
	}
	if body.JoinCode == "" {
		t.Fatal("create empty room returned empty join code")
	}

	return body.JoinCode
}

// startQuizlessRoom posts the host start and asserts it is refused with 409.
func startQuizlessRoom(ctx context.Context, t *testing.T, client *http.Client, baseURL, code string) {
	t.Helper()
	resp := httpPostJSON(ctx, t, client, fmt.Sprintf("%s/api/sessions/%s/start", baseURL, code), `{}`)
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusConflict; got != want {
		t.Errorf("start quiz-less room status = %d, want %d", got, want)
	}
}

// TestSession_StartAndArmRejectQuizlessRoom pins that POST /start and /arm-start
// on a quiz-less room return 409 and leave it a plain lobby (#1177).
func TestSession_StartAndArmRejectQuizlessRoom(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "quizless-host", "quizless-host-pass-123")

	code := createEmptyRoom(ctx, t, host, baseURL)

	startQuizlessRoom(ctx, t, host, baseURL, code)
	armStart(ctx, t, host, baseURL, code, http.StatusConflict)

	// Not wedged: still a lobby with no armed countdown.
	after := getArmedStartState(ctx, t, host, baseURL, code)
	if got, want := after.Phase, "lobby"; got != want {
		t.Errorf("phase after refused start = %q, want %q", got, want)
	}
	if after.StartAt != nil {
		t.Errorf("startAt after refused arm = %v, want nil", after.StartAt)
	}
}
