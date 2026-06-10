package livesession_test

import (
	"errors"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/livesession"
	"github.com/starquake/topbanana/internal/quiz"
)

// restartStart is the fixed clock origin the confirm-and-restart tests open
// their rooms at; any instant works since these tests assert through the store.
var restartStart = time.Date(2026, time.June, 10, 12, 0, 0, 0, time.UTC)

// TestService_HostHasRunningGame_FalseWhenNoSession pins HostHasRunningGame
// (#853): with no active room for the host, there is no game in flight.
func TestService_HostHasRunningGame_FalseWhenNoSession(t *testing.T) {
	t.Parallel()

	h := newEmptyRoomHarness(t, restartStart)
	ctx := t.Context()

	const hostID int64 = 1
	running, err := h.service.HostHasRunningGame(ctx, hostID)
	if err != nil {
		t.Fatalf("HostHasRunningGame (no session) err = %v, want nil", err)
	}
	if running {
		t.Error("HostHasRunningGame = true with no session, want false")
	}
}

// TestService_HostHasRunningGame_FalseWhenStaging pins that the staging phases a
// pick can simply arm - an empty lobby, an armed-but-not-started lobby, and the
// between-games intermission - all report no game in flight (#853), so the quiz
// view shows the plain Host live control rather than the confirm prompt.
func TestService_HostHasRunningGame_FalseWhenStaging(t *testing.T) {
	t.Parallel()

	const hostID int64 = 1

	t.Run("empty staging lobby", func(t *testing.T) {
		t.Parallel()
		h := newEmptyRoomHarness(t, restartStart)
		ctx := t.Context()
		if _, err := h.service.CreateSession(ctx, nil, hostID); err != nil {
			t.Fatalf("CreateSession (empty) err = %v, want nil", err)
		}
		if got, want := runningGame(t, h), false; got != want {
			t.Errorf("HostHasRunningGame (empty lobby) = %v, want %v", got, want)
		}
	})

	t.Run("armed but not started lobby", func(t *testing.T) {
		t.Parallel()
		h := newEmptyRoomHarness(t, restartStart)
		ctx := t.Context()
		qz := seedRunnerQuizSlug(t, h.quizStore, "restart-armed-lobby", [][]bool{{true}})
		if _, err := h.service.CreateSession(ctx, &qz.ID, hostID); err != nil {
			t.Fatalf("CreateSession (armed) err = %v, want nil", err)
		}
		// CreateSession leaves the room in the lobby with no started_at: the host
		// has a quiz preselected but the game has not begun, so a pick can still
		// just arm it.
		if got, want := runningGame(t, h), false; got != want {
			t.Errorf("HostHasRunningGame (armed lobby) = %v, want %v", got, want)
		}
	})

	t.Run("between-games intermission", func(t *testing.T) {
		t.Parallel()
		h := newEmptyRoomHarness(t, restartStart)
		ctx := t.Context()
		qz := seedRunnerQuizSlug(t, h.quizStore, "restart-intermission", [][]bool{{true}})
		sess, err := h.service.CreateSession(ctx, &qz.ID, hostID)
		if err != nil {
			t.Fatalf("CreateSession err = %v, want nil", err)
		}
		// Drive the game in flight, then end it into intermission via the store so
		// the room sits at the between-games screen the host can re-arm from.
		if err = h.service.StartQuiz(ctx, sess.JoinCode, hostID, qz.ID); err != nil {
			t.Fatalf("StartQuiz err = %v, want nil", err)
		}
		if err = h.store.Intermission(ctx, sess.ID); err != nil {
			t.Fatalf("Intermission err = %v, want nil", err)
		}
		if got, want := runningGame(t, h), false; got != want {
			t.Errorf("HostHasRunningGame (intermission) = %v, want %v", got, want)
		}
	})
}

// TestService_HostHasRunningGame_TrueWhenInFlight pins that a started, in-flight
// game reports running (#853), so the quiz view gates the Host live control
// behind the confirm-and-restart prompt.
func TestService_HostHasRunningGame_TrueWhenInFlight(t *testing.T) {
	t.Parallel()

	h := newEmptyRoomHarness(t, restartStart)
	ctx := t.Context()

	const hostID int64 = 1
	qz := seedRunnerQuizSlug(t, h.quizStore, "restart-inflight", [][]bool{{true}})
	sess, err := h.service.CreateSession(ctx, &qz.ID, hostID)
	if err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	// The runner drives the game into round_intro: it has left the lobby staging,
	// so a pick cannot simply arm it.
	if err = h.service.StartQuiz(ctx, sess.JoinCode, hostID, qz.ID); err != nil {
		t.Fatalf("StartQuiz err = %v, want nil", err)
	}
	if got, want := runningGame(t, h), true; got != want {
		t.Errorf("HostHasRunningGame (in flight) = %v, want %v", got, want)
	}
}

// TestService_RestartHosting_EndsRunningAndOpensNewRoom pins the confirm-and-
// restart happy path (#853): with a game in flight on quiz A, RestartHosting
// hosting quiz B finishes the running session and returns a NEW room (different
// join code) armed onto B.
func TestService_RestartHosting_EndsRunningAndOpensNewRoom(t *testing.T) {
	t.Parallel()

	h := newEmptyRoomHarness(t, restartStart)
	ctx := t.Context()

	const hostID int64 = 1
	quizA := seedRunnerQuizSlug(t, h.quizStore, "restart-a", [][]bool{{true}})
	quizB := seedRunnerQuizSlug(t, h.quizStore, "restart-b", [][]bool{{true}})

	running, err := h.service.CreateSession(ctx, &quizA.ID, hostID)
	if err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	if err = h.service.StartQuiz(ctx, running.JoinCode, hostID, quizA.ID); err != nil {
		t.Fatalf("StartQuiz err = %v, want nil", err)
	}

	fresh, err := h.service.RestartHosting(ctx, quizB.ID, hostID)
	if err != nil {
		t.Fatalf("RestartHosting err = %v, want nil", err)
	}
	if fresh == nil {
		t.Fatal("RestartHosting returned nil session, want a new room")
	}
	// A brand new room: a different join code from the one just ended.
	if fresh.JoinCode == running.JoinCode {
		t.Errorf("RestartHosting reused join code %q, want a new room", fresh.JoinCode)
	}
	if fresh.QuizID == nil {
		t.Fatalf("new room QuizID = nil, want %d", quizB.ID)
	}
	if got, want := *fresh.QuizID, quizB.ID; got != want {
		t.Errorf("new room QuizID = %d, want %d (quiz B)", got, want)
	}
	if got, want := fresh.Phase, PhaseLobby; got != want {
		t.Errorf("new room Phase = %q, want %q (host starts it later)", got, want)
	}

	// The old session is terminally finished.
	ended, err := h.store.GetSessionByID(ctx, running.ID)
	if err != nil {
		t.Fatalf("GetSessionByID (old) err = %v, want nil", err)
	}
	if got, want := ended.Phase, PhaseFinished; got != want {
		t.Errorf("old session Phase = %q, want %q (ended on restart)", got, want)
	}

	// The fresh room is now the host's active room.
	active, err := h.service.GetActiveSessionForHost(ctx, hostID)
	if err != nil {
		t.Fatalf("GetActiveSessionForHost err = %v, want nil", err)
	}
	if active == nil {
		t.Fatal("active session = nil, want the new room")
	}
	if got, want := active.ID, fresh.ID; got != want {
		t.Errorf("active session ID = %q, want %q (the new room)", got, want)
	}
}

// TestService_RestartHosting_RejectsSoloLeavesRunningUntouched pins that an
// unhostable pick is validated BEFORE the running game is ended (#853): a solo
// quiz B yields ErrNotLiveQuiz, no room is opened, and the running session on
// quiz A is left untouched so the host is never stranded with nothing.
func TestService_RestartHosting_RejectsSoloLeavesRunningUntouched(t *testing.T) {
	t.Parallel()

	h := newEmptyRoomHarness(t, restartStart)
	ctx := t.Context()

	const hostID int64 = 1
	quizA := seedRunnerQuizSlug(t, h.quizStore, "restart-keep-a", [][]bool{{true}})
	running, err := h.service.CreateSession(ctx, &quizA.ID, hostID)
	if err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	if err = h.service.StartQuiz(ctx, running.JoinCode, hostID, quizA.ID); err != nil {
		t.Fatalf("StartQuiz err = %v, want nil", err)
	}

	solo := &quiz.Quiz{
		Title:             "Solo",
		Slug:              "restart-solo-b",
		CreatedByPlayerID: hostID,
		Mode:              quiz.ModeSolo,
		Visibility:        quiz.VisibilityPublic,
		Questions: []*quiz.Question{
			{Text: "Q", Position: 1, Options: []*quiz.Option{{Text: "A", Correct: true}, {Text: "B"}}},
		},
	}
	if err = h.quizStore.CreateQuiz(ctx, solo); err != nil {
		t.Fatalf("CreateQuiz solo err = %v, want nil", err)
	}

	sess, err := h.service.RestartHosting(ctx, solo.ID, hostID)
	if got, want := err, ErrNotLiveQuiz; !errors.Is(got, want) {
		t.Errorf("RestartHosting (solo) err = %v, want %v", got, want)
	}
	if sess != nil {
		t.Errorf("RestartHosting (solo) session = %v, want nil (no room opened)", sess)
	}

	// The running session is untouched: still active and not finished.
	still, err := h.store.GetSessionByID(ctx, running.ID)
	if err != nil {
		t.Fatalf("GetSessionByID err = %v, want nil", err)
	}
	if still.Phase == PhaseFinished {
		t.Error("running session was finished by a rejected restart, want it left running")
	}
}

// TestService_RestartHosting_NoActiveSessionJustOpens pins that RestartHosting
// works as a plain open when the host has no active session (#853): nothing to
// end, so it just opens a new room hosting the picked quiz.
func TestService_RestartHosting_NoActiveSessionJustOpens(t *testing.T) {
	t.Parallel()

	h := newEmptyRoomHarness(t, restartStart)
	ctx := t.Context()

	const hostID int64 = 1
	qz := seedRunnerQuizSlug(t, h.quizStore, "restart-fresh-open", [][]bool{{true}})

	sess, err := h.service.RestartHosting(ctx, qz.ID, hostID)
	if err != nil {
		t.Fatalf("RestartHosting (no active) err = %v, want nil", err)
	}
	if sess == nil {
		t.Fatal("RestartHosting returned nil session, want a new room")
	}
	if sess.QuizID == nil {
		t.Fatalf("new room QuizID = nil, want %d", qz.ID)
	}
	if got, want := *sess.QuizID, qz.ID; got != want {
		t.Errorf("new room QuizID = %d, want %d", got, want)
	}
	if got, want := sess.Phase, PhaseLobby; got != want {
		t.Errorf("new room Phase = %q, want %q", got, want)
	}
}

// runningGame returns HostHasRunningGame for the seeded admin (host id 1, the
// host every restart test uses), failing the test on a lookup error so the
// caller can assert the bool inline with got/want.
func runningGame(t *testing.T, h *emptyRoomHarness) bool {
	t.Helper()
	const hostID int64 = 1
	got, err := h.service.HostHasRunningGame(t.Context(), hostID)
	if err != nil {
		t.Fatalf("HostHasRunningGame err = %v, want nil", err)
	}

	return got
}
