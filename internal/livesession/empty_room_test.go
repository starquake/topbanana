package livesession_test

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/game"
	. "github.com/starquake/topbanana/internal/livesession"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// emptyRoomHarness wires the live-session service + runner over real stores (a
// fresh in-memory DB) the way startSessionRunner does, but creates no session:
// the empty-room tests open their own room (with or without a quiz) and drive it
// through the service. The runner is wired as the service's advancer so StartQuiz
// drives straight into the first round, but the tests assert through the service
// and store rather than stepping the runner clock directly.
type emptyRoomHarness struct {
	service   *Service
	store     *store.LiveSessionStore
	quizStore *store.QuizStore
}

// newEmptyRoomHarness builds the service/runner over real stores with a fake
// clock at start, ready for a test to create a room itself.
func newEmptyRoomHarness(t *testing.T, start time.Time) *emptyRoomHarness {
	t.Helper()

	db := dbtest.Open(t)
	logger := slog.New(slog.DiscardHandler)
	quizStore := store.NewQuizStore(db, logger)
	sessionStore := store.NewLiveSessionStore(db, logger)

	service := NewService(sessionStore, quizStore, logger)
	hub := NewHub()
	service.SetPublisher(hub)
	scorer := game.NewService(nil, quizStore, logger)
	clock := &fakeClock{now: start}
	runner := NewRunner(sessionStore, quizStore, hub, scorer, logger, runnerCfg)
	runner.SetClock(clock)
	service.SetAdvancer(runner)

	return &emptyRoomHarness{
		service:   service,
		store:     sessionStore,
		quizStore: quizStore,
	}
}

// TestService_CreateEmptyRoom pins that a host can open a room with no quiz
// (#836): CreateSession with a nil quiz id yields a lobby room whose QuizID is
// nil (the "no game running yet" staging state), at game_seq 1.
func TestService_CreateEmptyRoom(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newEmptyRoomHarness(t, start)
	ctx := t.Context()

	const hostID int64 = 1 // seeded admin
	sess, err := h.service.CreateSession(ctx, nil, hostID)
	if err != nil {
		t.Fatalf("CreateSession (empty) err = %v, want nil", err)
	}
	if sess.QuizID != nil {
		t.Errorf("empty room QuizID = %d, want nil (no game running yet)", *sess.QuizID)
	}
	if got, want := sess.Phase, PhaseLobby; got != want {
		t.Errorf("empty room Phase = %q, want %q", got, want)
	}
	if got, want := sess.GameSeq, int64(1); got != want {
		t.Errorf("empty room GameSeq = %d, want %d", got, want)
	}

	// The lobby state read works for a quiz-less room: the host is a participant,
	// and Quiz is nil (nothing to render yet).
	state, err := h.service.GetLobbyState(ctx, sess.JoinCode, hostID)
	if err != nil {
		t.Fatalf("GetLobbyState (empty room) err = %v, want nil", err)
	}
	if state.Quiz != nil {
		t.Errorf("empty room lobby Quiz = %v, want nil", state.Quiz)
	}
}

// TestService_StartFirstQuizFromEmptyLobby pins the unified start path (#836):
// from an empty lobby (no quiz), the host arms the first live quiz and the runner
// drives game 1, which lands at game_seq 1 (the first game does not skip to 2).
func TestService_StartFirstQuizFromEmptyLobby(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newEmptyRoomHarness(t, start)
	ctx := t.Context()

	const hostID int64 = 1
	sess, err := h.service.CreateSession(ctx, nil, hostID)
	if err != nil {
		t.Fatalf("CreateSession (empty) err = %v, want nil", err)
	}
	qz := seedRunnerQuizSlug(t, h.quizStore, "empty-room-first-quiz", [][]bool{{true}})

	// The host picks the first quiz from the empty lobby; the unified StartQuiz
	// arms it and begins immediately (the runner enters the first round_intro).
	if err = h.service.StartQuiz(ctx, sess.JoinCode, hostID, qz.ID); err != nil {
		t.Fatalf("StartQuiz (first game from empty lobby) err = %v, want nil", err)
	}

	armed, err := h.store.GetSessionByID(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSessionByID err = %v, want nil", err)
	}
	if armed.QuizID == nil {
		t.Fatalf("armed QuizID = nil, want %d", qz.ID)
	}
	if got, want := *armed.QuizID, qz.ID; got != want {
		t.Errorf("armed QuizID = %d, want %d (first quiz)", got, want)
	}
	// The first game from an empty room stays at game_seq 1 (it did not bump).
	if got, want := armed.GameSeq, int64(1); got != want {
		t.Errorf("armed GameSeq = %d, want %d (first game stays at 1)", got, want)
	}
	if got, want := armed.Phase, PhaseRoundIntro; got != want {
		t.Errorf("phase after first start = %q, want %q (game 1 driving)", got, want)
	}
}

// TestService_StartHosting_NoActiveRoomOpensArmedLobby pins StartHosting case 1
// (#851): with no active room for the host, it opens a NEW lobby armed with the
// quiz (the host starts it once players are in), same as the prior "Play live".
func TestService_StartHosting_NoActiveRoomOpensArmedLobby(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 9, 12, 0, 0, 0, time.UTC)
	h := newEmptyRoomHarness(t, start)
	ctx := t.Context()

	const hostID int64 = 1
	qz := seedRunnerQuizSlug(t, h.quizStore, "host-hosting-new", [][]bool{{true}})

	sess, err := h.service.StartHosting(ctx, qz.ID, hostID)
	if err != nil {
		t.Fatalf("StartHosting (no active room) err = %v, want nil", err)
	}
	if sess == nil {
		t.Fatal("StartHosting returned nil session, want a new armed lobby")
	}
	// A new room is opened, armed with the quiz, and still in the lobby (the
	// host starts it when players are in - it is not started here).
	if sess.QuizID == nil {
		t.Fatalf("new room QuizID = nil, want %d", qz.ID)
	}
	if got, want := *sess.QuizID, qz.ID; got != want {
		t.Errorf("new room QuizID = %d, want %d", got, want)
	}
	if got, want := sess.Phase, PhaseLobby; got != want {
		t.Errorf("new room Phase = %q, want %q (host starts it later)", got, want)
	}
	if sess.StartedAt != nil {
		t.Error("new room StartedAt should be nil (not started until the host starts it)")
	}
}

// TestService_StartHosting_EmptyRoomArmsExistingRoom pins StartHosting case 2
// (#851): with an active empty staging room for the host, it arms+starts the
// quiz in THAT room (reusing StartQuiz) rather than spawning a second one, so
// the returned session is the existing room driving the game.
func TestService_StartHosting_EmptyRoomArmsExistingRoom(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 9, 12, 0, 0, 0, time.UTC)
	h := newEmptyRoomHarness(t, start)
	ctx := t.Context()

	const hostID int64 = 1
	empty, err := h.service.CreateSession(ctx, nil, hostID)
	if err != nil {
		t.Fatalf("CreateSession (empty) err = %v, want nil", err)
	}
	qz := seedRunnerQuizSlug(t, h.quizStore, "host-hosting-empty", [][]bool{{true}})

	sess, err := h.service.StartHosting(ctx, qz.ID, hostID)
	if err != nil {
		t.Fatalf("StartHosting (empty active room) err = %v, want nil", err)
	}
	if sess == nil {
		t.Fatal("StartHosting returned nil session, want the existing room")
	}
	// No second room: the returned session is the same room the host already had.
	if got, want := sess.ID, empty.ID; got != want {
		t.Errorf("StartHosting room ID = %q, want %q (same room, no second spawned)", got, want)
	}

	// That room is now armed onto the quiz and driving the first game.
	armed, err := h.store.GetSessionByID(ctx, empty.ID)
	if err != nil {
		t.Fatalf("GetSessionByID err = %v, want nil", err)
	}
	if armed.QuizID == nil {
		t.Fatalf("armed QuizID = nil, want %d", qz.ID)
	}
	if got, want := *armed.QuizID, qz.ID; got != want {
		t.Errorf("armed QuizID = %d, want %d", got, want)
	}
	if got, want := armed.Phase, PhaseRoundIntro; got != want {
		t.Errorf("armed Phase = %q, want %q (game driving in the existing room)", got, want)
	}
}

// TestService_StartHosting_InFlightRoomLeftUntouched pins StartHosting case 3
// (#851): with an active room whose game is already in flight, a stray pick
// leaves it untouched and returns the running room - the picked quiz does NOT
// arm (the end-and-restart confirm is deferred to #853).
func TestService_StartHosting_InFlightRoomLeftUntouched(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 9, 12, 0, 0, 0, time.UTC)
	h := newEmptyRoomHarness(t, start)
	ctx := t.Context()

	const hostID int64 = 1
	running := seedRunnerQuizSlug(t, h.quizStore, "host-hosting-running", [][]bool{{true}})
	sess, err := h.service.CreateSession(ctx, &running.ID, hostID)
	if err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	// Drive the room into flight: the first game is now running (round_intro).
	if err = h.service.StartQuiz(ctx, sess.JoinCode, hostID, running.ID); err != nil {
		t.Fatalf("StartQuiz err = %v, want nil", err)
	}

	// A different quiz is picked while the game runs.
	other := seedRunnerQuizSlug(t, h.quizStore, "host-hosting-other", [][]bool{{true}})
	got, err := h.service.StartHosting(ctx, other.ID, hostID)
	if err != nil {
		t.Fatalf("StartHosting (in-flight room) err = %v, want nil", err)
	}
	if got == nil {
		t.Fatal("StartHosting returned nil session, want the running room")
	}
	if want := sess.ID; got.ID != want {
		t.Errorf("StartHosting room ID = %q, want %q (the running room)", got.ID, want)
	}

	// The running room is untouched: it still points at the original quiz, not
	// the stray pick.
	still, err := h.store.GetSessionByID(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSessionByID err = %v, want nil", err)
	}
	if still.QuizID == nil {
		t.Fatal("running room QuizID = nil, want the original quiz")
	}
	if got, want := *still.QuizID, running.ID; got != want {
		t.Errorf("running room QuizID = %d, want %d (stray pick must not re-arm)", got, want)
	}
}

// TestService_StartHosting_RejectsSoloQuiz pins that StartHosting propagates the
// unhostable-quiz error (#851): a solo quiz id yields ErrNotLiveQuiz so the
// handler can bounce to the quiz list, and no room is opened.
func TestService_StartHosting_RejectsSoloQuiz(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 9, 12, 0, 0, 0, time.UTC)
	h := newEmptyRoomHarness(t, start)
	ctx := t.Context()

	const hostID int64 = 1
	solo := &quiz.Quiz{
		Title:             "Solo",
		Slug:              "host-hosting-solo",
		CreatedByPlayerID: hostID,
		Mode:              quiz.ModeSolo,
		Visibility:        quiz.VisibilityPublic,
		Questions: []*quiz.Question{
			{Text: "Q", Position: 1, Options: []*quiz.Option{{Text: "A", Correct: true}, {Text: "B"}}},
		},
	}
	if err := h.quizStore.CreateQuiz(ctx, solo); err != nil {
		t.Fatalf("CreateQuiz solo err = %v, want nil", err)
	}

	sess, err := h.service.StartHosting(ctx, solo.ID, hostID)
	if got, want := err, ErrNotLiveQuiz; !errors.Is(got, want) {
		t.Errorf("StartHosting (solo) err = %v, want %v", got, want)
	}
	if sess != nil {
		t.Errorf("StartHosting (solo) session = %v, want nil (no room opened)", sess)
	}
}

// TestService_EndSessionClosesImmediately pins the host End control (#836):
// EndSession terminally finishes the room at once, regardless of host presence
// or players, and a second End is an idempotent no-op.
func TestService_EndSessionClosesImmediately(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newEmptyRoomHarness(t, start)
	ctx := t.Context()

	const hostID int64 = 1
	qz := seedRunnerQuizSlug(t, h.quizStore, "end-session-quiz", [][]bool{{true}})
	sess, err := h.service.CreateSession(ctx, &qz.ID, hostID)
	if err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}

	if err = h.service.EndSession(ctx, sess.JoinCode, hostID); err != nil {
		t.Fatalf("EndSession err = %v, want nil", err)
	}
	ended, err := h.store.GetSessionByID(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSessionByID err = %v, want nil", err)
	}
	if got, want := ended.Phase, PhaseFinished; got != want {
		t.Errorf("phase after EndSession = %q, want %q", got, want)
	}
	if ended.FinishedAt == nil {
		t.Error("ended session has nil FinishedAt")
	}

	// A second End on the now-finished room is an idempotent no-op.
	if err = h.service.EndSession(ctx, sess.JoinCode, hostID); err != nil {
		t.Errorf("second EndSession err = %v, want nil (idempotent)", err)
	}
}

// TestService_EndSessionRejectsNonHost pins that only the host may end a room: a
// non-host caller gets ErrNotHost and the room stays open.
func TestService_EndSessionRejectsNonHost(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newEmptyRoomHarness(t, start)
	ctx := t.Context()

	const hostID int64 = 1
	sess, err := h.service.CreateSession(ctx, nil, hostID)
	if err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}

	const otherPlayerID int64 = 999
	if got, want := h.service.EndSession(ctx, sess.JoinCode, otherPlayerID), ErrNotHost; !errors.Is(got, want) {
		t.Errorf("EndSession by non-host err = %v, want %v", got, want)
	}
	still, err := h.store.GetSessionByID(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSessionByID err = %v, want nil", err)
	}
	if got, want := still.Phase, PhaseLobby; got != want {
		t.Errorf("phase after rejected End = %q, want %q (room stays open)", got, want)
	}
}

// TestService_GetActiveSessionForHost pins the resume lookup (#836): it returns
// the host's live room while it is alive and nil once it is finished.
func TestService_GetActiveSessionForHost(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newEmptyRoomHarness(t, start)
	ctx := t.Context()

	const hostID int64 = 1

	// No room yet: the lookup returns nil.
	none, err := h.service.GetActiveSessionForHost(ctx, hostID)
	if err != nil {
		t.Fatalf("GetActiveSessionForHost (none) err = %v, want nil", err)
	}
	if none != nil {
		t.Errorf("active session with no room = %v, want nil", none)
	}

	sess, err := h.service.CreateSession(ctx, nil, hostID)
	if err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}

	// The open room is returned as the host's active room.
	active, err := h.service.GetActiveSessionForHost(ctx, hostID)
	if err != nil {
		t.Fatalf("GetActiveSessionForHost (live) err = %v, want nil", err)
	}
	if active == nil {
		t.Fatal("active session = nil, want the open room")
	}
	if got, want := active.ID, sess.ID; got != want {
		t.Errorf("active session ID = %q, want %q", got, want)
	}

	// After the room is ended, it is no longer active: the lookup returns nil.
	if err = h.service.EndSession(ctx, sess.JoinCode, hostID); err != nil {
		t.Fatalf("EndSession err = %v, want nil", err)
	}
	gone, err := h.service.GetActiveSessionForHost(ctx, hostID)
	if err != nil {
		t.Fatalf("GetActiveSessionForHost (finished) err = %v, want nil", err)
	}
	if gone != nil {
		t.Errorf("active session after finish = %v, want nil", gone)
	}
}
