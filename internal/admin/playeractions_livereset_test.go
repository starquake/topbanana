package admin_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
)

// postResetLiveQuiz drives HandlePlayerResetLiveQuiz against the
// (player, quiz) pair, posing as the seeded admin.
func postResetLiveQuiz(
	t *testing.T, env *adminEnv, playerID, quizID int64,
) *httptest.ResponseRecorder {
	t.Helper()
	handler := HandlePlayerResetLiveQuiz(slog.New(slog.DiscardHandler), env.admin, newCredFlash(t))

	path := "/admin/players/" + strconv.FormatInt(playerID, 10) +
		"/live-quizzes/" + strconv.FormatInt(quizID, 10) + "/reset"
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, nil)
	req.SetPathValue("playerID", strconv.FormatInt(playerID, 10))
	req.SetPathValue("quizID", strconv.FormatInt(quizID, 10))
	req = req.WithContext(auth.WithPlayer(req.Context(), &auth.Player{ID: testAdminID, Role: auth.RoleAdmin}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func TestHandlePlayerResetLiveQuiz_ClearsParticipationAndAudits(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	qz := env.seedQuiz(t, liveQuiz())
	player := env.seedPlayer(t, "alice")
	env.seedFinishedLiveSession(t, qz, player, "RST234")

	// The gate is closed before the reset.
	played, err := env.sessions.PlayerFinishedSessionForQuiz(t.Context(), player, qz.ID)
	if err != nil {
		t.Fatalf("PlayerFinishedSessionForQuiz err = %v, want nil", err)
	}
	if got, want := played, true; got != want {
		t.Fatalf("played before reset = %v, want %v", got, want)
	}

	rec := postResetLiveQuiz(t, env, player, qz.ID)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Location"),
		"/admin/players/"+strconv.FormatInt(player, 10); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}

	// The gate is clear and the participation row is gone.
	played, err = env.sessions.PlayerFinishedSessionForQuiz(t.Context(), player, qz.ID)
	if err != nil {
		t.Fatalf("PlayerFinishedSessionForQuiz err = %v, want nil", err)
	}
	if got, want := played, false; got != want {
		t.Errorf("played after reset = %v, want %v", got, want)
	}
	plays, err := env.admin.ListFinishedSessionPlaysForPlayer(t.Context(), player, 20)
	if err != nil {
		t.Fatalf("ListFinishedSessionPlaysForPlayer err = %v, want nil", err)
	}
	if got, want := len(plays), 0; got != want {
		t.Errorf("plays after reset = %d, want %d", got, want)
	}

	entries := env.auditEntries(t, player)
	if got, want := len(entries), 1; got != want {
		t.Fatalf("audit entries = %d, want %d", got, want)
	}
	if got, want := entries[0].Action, auth.AdminActionLiveQuizReset; got != want {
		t.Errorf("audit action = %q, want %q", got, want)
	}
	if got, want := entries[0].Payload, `"quiz_id":"`+strconv.FormatInt(qz.ID, 10)+`"`; !strings.Contains(got, want) {
		t.Errorf("audit payload = %q, should contain %q", got, want)
	}
}

func TestHandlePlayerResetLiveQuiz_NoParticipationIsNoOp(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	qz := env.seedQuiz(t, liveQuiz())
	player := env.seedPlayer(t, "alice")

	// No finished session for this player; the reset is an idempotent
	// 303 that still records the action.
	rec := postResetLiveQuiz(t, env, player, qz.ID)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := len(env.auditEntries(t, player)), 1; got != want {
		t.Errorf("audit entries = %d, want %d", got, want)
	}
}

func TestHandlePlayerResetLiveQuiz_404WhenPlayerMissing(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	qz := env.seedQuiz(t, liveQuiz())

	rec := postResetLiveQuiz(t, env, 999999, qz.ID)

	if got, want := rec.Code, http.StatusNotFound; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

func TestHandlePlayerResetLiveQuiz_500WhenStoreClosed(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	qz := env.seedQuiz(t, liveQuiz())
	player := env.seedPlayer(t, "alice")
	env.seedFinishedLiveSession(t, qz, player, "RST500")
	env.closeStore(t)

	rec := postResetLiveQuiz(t, env, player, qz.ID)

	// loadActionTarget is the first store hit; on a closed DB it returns a
	// driver error (not ErrPlayerNotFound), which the handler surfaces as a
	// 500 rather than a success 303.
	if got, want := rec.Code, http.StatusInternalServerError; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}
