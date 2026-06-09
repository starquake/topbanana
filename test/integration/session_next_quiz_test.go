package integration_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"testing"
)

// driveGameToIntermission starts the hosted single-question game and plays it to
// its end-of-game intermission: the host starts, the player answers the seeded
// correct option once the window opens, and the runner closes the question,
// reveals, and ends the game into intermission (#836). Used by the next-quiz
// test to reach the only phase from which the host can arm the next quiz.
func driveGameToIntermission(
	ctx context.Context, t *testing.T, host, player *http.Client, baseURL, code string,
) {
	t.Helper()
	startSession(ctx, t, host, baseURL, code)

	state := waitForPhase(ctx, t, player, baseURL, code, "question")
	if state.Question == nil || len(state.Question.Options) == 0 {
		t.Fatal("question phase missing an option to answer")
	}
	pick := state.Question.Options[0].ID
	waitForAnswersOpen(ctx, t, player, baseURL, code)
	answerSession(ctx, t, player, baseURL, code, pick, http.StatusNoContent)

	waitForPhase(ctx, t, player, baseURL, code, "intermission")
}

// postNextQuiz posts the host next-quiz control with a CSRF token seeded from
// the lobby page, returning the response for the caller to assert on.
func postNextQuiz(
	ctx context.Context, t *testing.T, host *http.Client, baseURL, code string, quizID int64,
) *http.Response {
	t.Helper()
	token := fetchCSRFToken(ctx, t, host, baseURL+"/host/"+code)

	return httpPostForm(ctx, t, host, baseURL+"/host/"+code+"/next-quiz", url.Values{
		"csrf_token": {token},
		"quiz_id":    {strconv.FormatInt(quizID, 10)},
	})
}

// TestHostNextQuiz_RearmsRoomOntoNewQuiz pins the persistent-rooms re-arm path
// (#836): a host plays one game to intermission, then posts the next-quiz
// control with a second live quiz. The room re-arms onto that quiz - bumping
// game_seq and pointing at the new quiz id - and the runner drives the new game,
// all without anyone re-entering a code. A double-post from intermission's stale
// tab is harmless (the room has already left intermission), and the redirect
// always lands back on the lobby.
func TestHostNextQuiz_RearmsRoomOntoNewQuiz(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegrationWithEnv(t, map[string]string{
		"SESSION_RUNNER_BEAT": "250ms",
		"REVEAL_DELAY":        "200ms",
	})
	baseURL := setup.BaseURL

	game1 := seedRunnerLiveQuiz(ctx, t, setup.Stores.Quizzes, "next-quiz-g1")
	game2 := seedRunnerLiveQuiz(ctx, t, setup.Stores.Quizzes, "next-quiz-g2")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "next-quiz-host", "next-quiz-pass-123")
	code := createSession(ctx, t, host, baseURL, game1.ID)

	player := newAnonClient(t)
	joinSession(ctx, t, player, baseURL, code, "Solo")

	// Game 1 runs to intermission, the between-games screen.
	driveGameToIntermission(ctx, t, host, player, baseURL, code)

	// The host arms the next quiz: a 303 back to the lobby.
	resp := postNextQuiz(ctx, t, host, baseURL, code, game2.ID)
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("next-quiz status = %d, want %d", got, want)
	}
	if got, want := resp.Header.Get("Location"), "/host/"+code; got != want {
		t.Errorf("next-quiz redirect = %q, want %q", got, want)
	}

	// The room re-armed onto game 2: a new game_seq and the new quiz id.
	sess, err := setup.Stores.LiveSessions.GetSessionByJoinCode(ctx, code)
	if err != nil {
		t.Fatalf("GetSessionByJoinCode err = %v, want nil", err)
	}
	if sess.QuizID == nil {
		t.Fatalf("re-armed QuizID = nil, want %d (the new quiz)", game2.ID)
	}
	if got, want := *sess.QuizID, game2.ID; got != want {
		t.Errorf("re-armed QuizID = %d, want %d (the new quiz)", got, want)
	}
	if got, want := sess.GameSeq, int64(2); got != want {
		t.Errorf("re-armed GameSeq = %d, want %d", got, want)
	}

	// The runner drives the re-armed game: the player (still joined, never
	// re-entered the code) reaches game 2's question phase.
	state := waitForPhase(ctx, t, player, baseURL, code, "question")
	if state.Question == nil {
		t.Fatal("re-armed game 2 has no question in state")
	}

	// A stale-tab double-post from the now-running game is harmless: it is not in
	// intermission, so the control is a no-op that still redirects to the lobby.
	again := postNextQuiz(ctx, t, host, baseURL, code, game1.ID)
	defer closeBody(t, again.Body)
	if got, want := again.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("repeat next-quiz status = %d, want %d (mid-game no-op)", got, want)
	}
	midGame, err := setup.Stores.LiveSessions.GetSessionByJoinCode(ctx, code)
	if err != nil {
		t.Fatalf("GetSessionByJoinCode (mid-game) err = %v, want nil", err)
	}
	if got, want := midGame.GameSeq, int64(2); got != want {
		t.Errorf("GameSeq after mid-game next-quiz = %d, want %d (unchanged)", got, want)
	}
}

// TestHostNextQuiz_Authz pins the next-quiz access rules: an anonymous visitor
// is bounced to login and a foreign host (who does not own the room) 404s, so a
// host cannot re-arm a room they do not own by guessing its code.
func TestHostNextQuiz_Authz(t *testing.T) {
	t.Parallel()

	const foreignEmail = "next-quiz-authz-other@example.test"
	ctx, setup := setupIntegrationWithEnv(t, map[string]string{
		"SESSION_RUNNER_BEAT": "250ms",
		"REVEAL_DELAY":        "200ms",
		"ADMIN_EMAILS":        foreignEmail,
	})
	baseURL := setup.BaseURL

	game1 := seedRunnerLiveQuiz(ctx, t, setup.Stores.Quizzes, "next-quiz-authz-g1")
	game2 := seedRunnerLiveQuiz(ctx, t, setup.Stores.Quizzes, "next-quiz-authz-g2")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "next-quiz-authz-host", "next-quiz-authz-123")
	code := createSession(ctx, t, host, baseURL, game1.ID)

	player := newAnonClient(t)
	joinSession(ctx, t, player, baseURL, code, "Solo")
	driveGameToIntermission(ctx, t, host, player, baseURL, code)

	foreign := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyViaLinkAndMint(ctx, t, foreign, baseURL, setup.DBURI, "next-quiz-authz-other", "next-quiz-other-123")

	t.Run("anonymous visitor without a CSRF token is forbidden", func(t *testing.T) {
		t.Parallel()
		anon := &http.Client{
			Jar:           mustJar(t),
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		}
		// The CSRF middleware wraps the auth gate (it runs first, by design, so an
		// unauthenticated request without a valid token is rejected before any
		// auth-state-leaking redirect to /login). A fresh anon jar carries no
		// nonce, so the tokenless POST is forbidden.
		resp := httpPostForm(ctx, t, anon, baseURL+"/host/"+code+"/next-quiz", url.Values{
			"quiz_id": {strconv.FormatInt(game2.ID, 10)},
		})
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusForbidden; got != want {
			t.Errorf("anon next-quiz status = %d, want %d", got, want)
		}
	})

	t.Run("a foreign host cannot re-arm another host's room", func(t *testing.T) {
		t.Parallel()
		// The foreign host cannot GET the lobby (it 404s), so seed their CSRF
		// nonce from a page they can load - the admin quiz list.
		token := fetchCSRFToken(ctx, t, foreign, baseURL+"/admin/quizzes")
		resp := httpPostForm(ctx, t, foreign, baseURL+"/host/"+code+"/next-quiz", url.Values{
			"csrf_token": {token},
			"quiz_id":    {strconv.FormatInt(game2.ID, 10)},
		})
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("foreign host next-quiz status = %d, want %d", got, want)
		}
	})
}
