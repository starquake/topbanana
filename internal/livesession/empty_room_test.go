package livesession_test

import (
	"errors"
	"log/slog"
	"slices"
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
	service     *Service
	store       *store.LiveSessionStore
	quizStore   *store.QuizStore
	playerStore *store.PlayerStore
}

// newEmptyRoomHarness builds the service/runner over real stores with a fake
// clock at start, ready for a test to create a room itself.
func newEmptyRoomHarness(t *testing.T, start time.Time) *emptyRoomHarness {
	t.Helper()

	db := dbtest.Open(t)
	logger := slog.New(slog.DiscardHandler)
	quizStore := store.NewQuizStore(db, logger)
	sessionStore := store.NewLiveSessionStore(db, logger)
	playerStore := store.NewPlayerStore(db, logger)

	service := NewService(sessionStore, quizStore, logger)
	hub := NewHub()
	service.SetPublisher(hub)
	scorer := game.NewService(nil, quizStore, logger)
	clock := &fakeClock{now: start}
	runner := NewRunner(sessionStore, quizStore, hub, scorer, logger, runnerCfg)
	runner.SetClock(clock)
	service.SetAdvancer(runner)

	return &emptyRoomHarness{
		service:     service,
		store:       sessionStore,
		quizStore:   quizStore,
		playerStore: playerStore,
	}
}

// joinNewPlayer creates a fresh anonymous player and joins them to the room,
// returning the new player id. The empty-room tests build a roster ad-hoc to
// check it survives a re-arm.
func (h *emptyRoomHarness) joinNewPlayer(t *testing.T, joinCode, name string) int64 {
	t.Helper()
	ctx := t.Context()
	p, err := h.playerStore.CreateAnonymousPlayer(ctx, name)
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}
	if _, err = h.service.Join(ctx, joinCode, p.ID); err != nil {
		t.Fatalf("Join err = %v, want nil", err)
	}

	return p.ID
}

// rosterIDs returns the active roster player ids for the room, sorted ascending,
// so a test can assert the roster carried across a re-arm unchanged.
func (h *emptyRoomHarness) rosterIDs(t *testing.T, sessionID string) []int64 {
	t.Helper()
	sess, err := h.store.GetSessionByID(t.Context(), sessionID)
	if err != nil {
		t.Fatalf("GetSessionByID err = %v, want nil", err)
	}
	ids := make([]int64, 0, len(sess.Players))
	for _, p := range sess.Players {
		ids = append(ids, p.PlayerID)
	}
	slices.Sort(ids)

	return ids
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
	sess, err := h.service.CreateSession(ctx, nil, hostID, false)
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

	// The session state read works for a quiz-less room: the host is a participant,
	// and Quiz is nil (nothing to render yet).
	state, err := h.service.GetSessionState(ctx, sess.JoinCode, hostID)
	if err != nil {
		t.Fatalf("GetSessionState (empty room) err = %v, want nil", err)
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
	sess, err := h.service.CreateSession(ctx, nil, hostID, false)
	if err != nil {
		t.Fatalf("CreateSession (empty) err = %v, want nil", err)
	}
	qz := seedRunnerQuizSlug(t, h.quizStore, "empty-room-first-quiz", [][]bool{{true}})

	// The host picks the first quiz from the empty lobby; the unified StartQuiz
	// arms it and begins immediately (the runner enters the first round_intro).
	if err = h.service.StartQuiz(ctx, sess.JoinCode, hostID, qz.ID, false); err != nil {
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

	sess, err := h.service.StartHosting(ctx, qz.ID, hostID, false)
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
// (#851/#863): with an active empty staging room for the host, it arms the quiz
// in THAT room (reusing ArmQuiz) rather than spawning a second one, and leaves it
// in the lobby - NOT started - so the host gathers players and presses Start.
func TestService_StartHosting_EmptyRoomArmsExistingRoom(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 9, 12, 0, 0, 0, time.UTC)
	h := newEmptyRoomHarness(t, start)
	ctx := t.Context()

	const hostID int64 = 1
	empty, err := h.service.CreateSession(ctx, nil, hostID, false)
	if err != nil {
		t.Fatalf("CreateSession (empty) err = %v, want nil", err)
	}
	qz := seedRunnerQuizSlug(t, h.quizStore, "host-hosting-empty", [][]bool{{true}})

	sess, err := h.service.StartHosting(ctx, qz.ID, hostID, false)
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

	// That room is now armed onto the quiz but STILL IN THE LOBBY, not started
	// (#863): the host gathers players and presses Start, same as the no-active
	// case.
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
	if got, want := armed.Phase, PhaseLobby; got != want {
		t.Errorf("armed Phase = %q, want %q (armed but waiting in the lobby)", got, want)
	}
	if armed.StartedAt != nil {
		t.Errorf("armed StartedAt = %v, want nil (not started until the host presses Start)", armed.StartedAt)
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
	sess, err := h.service.CreateSession(ctx, &running.ID, hostID, false)
	if err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	// Drive the room into flight: the first game is now running (round_intro).
	if err = h.service.StartQuiz(ctx, sess.JoinCode, hostID, running.ID, false); err != nil {
		t.Fatalf("StartQuiz err = %v, want nil", err)
	}

	// A different quiz is picked while the game runs.
	other := seedRunnerQuizSlug(t, h.quizStore, "host-hosting-other", [][]bool{{true}})
	got, err := h.service.StartHosting(ctx, other.ID, hostID, false)
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

	sess, err := h.service.StartHosting(ctx, solo.ID, hostID, false)
	if got, want := err, ErrNotLiveQuiz; !errors.Is(got, want) {
		t.Errorf("StartHosting (solo) err = %v, want %v", got, want)
	}
	if sess != nil {
		t.Errorf("StartHosting (solo) session = %v, want nil (no room opened)", sess)
	}
}

// TestService_CreateSession_OwnerGate pins the per-host hosting isolation
// (#1207): the owner may host their own draft, and a non-owner non-admin cannot
// host it.
func TestService_CreateSession_OwnerGate(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)
	h := newEmptyRoomHarness(t, start)
	ctx := t.Context()

	// seedRunnerQuizSlug creates a live quiz owned by player 1, left as a draft.
	const ownerID int64 = 1
	qz := seedRunnerQuizSlug(t, h.quizStore, "draft-live-owner-gate", [][]bool{{true}})

	t.Run("owner can host their own draft", func(t *testing.T) {
		t.Parallel()

		sess, err := h.service.CreateSession(ctx, &qz.ID, ownerID, false)
		if err != nil {
			t.Fatalf("CreateSession (owner, draft) err = %v, want nil", err)
		}
		if sess == nil {
			t.Fatal("CreateSession (owner, draft) session = nil, want a room")
		}
	})

	t.Run("a non-owner cannot host a draft", func(t *testing.T) {
		t.Parallel()

		stranger, err := h.playerStore.CreateAnonymousPlayer(ctx, "stranger-host")
		if err != nil {
			t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
		}

		sess, err := h.service.CreateSession(ctx, &qz.ID, stranger.ID, false)
		if got, want := err, ErrQuizNotOwned; !errors.Is(got, want) {
			t.Errorf("CreateSession (non-owner, draft) err = %v, want %v", got, want)
		}
		if sess != nil {
			t.Errorf("CreateSession (non-owner, draft) session = %v, want nil", sess)
		}
	})
}

// TestService_Hosting_PerHostIsolation pins the #1207 override of #677 for the
// live-run path: a non-owner non-admin cannot host another host's PUBLISHED live
// quiz through any entry point (CreateSession, ArmQuiz, StartHosting), while an
// admin may host it.
func TestService_Hosting_PerHostIsolation(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.July, 4, 12, 0, 0, 0, time.UTC)
	h := newEmptyRoomHarness(t, start)
	ctx := t.Context()

	// A live quiz owned by player 1, published - the case #677 used to let any
	// host run; #1207 restricts it to the owner or an admin.
	qz := seedRunnerQuizSlug(t, h.quizStore, "published-owner-gate", [][]bool{{true}})
	if err := h.quizStore.SetQuizPublished(ctx, qz.ID, true); err != nil {
		t.Fatalf("SetQuizPublished err = %v, want nil", err)
	}

	stranger, err := h.playerStore.CreateAnonymousPlayer(ctx, "iso-stranger")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	t.Run("non-owner CreateSession is rejected", func(t *testing.T) {
		t.Parallel()

		sess, cerr := h.service.CreateSession(ctx, &qz.ID, stranger.ID, false)
		if got, want := cerr, ErrQuizNotOwned; !errors.Is(got, want) {
			t.Errorf("CreateSession (non-owner, published) err = %v, want %v", got, want)
		}
		if sess != nil {
			t.Errorf("CreateSession (non-owner, published) session = %v, want nil", sess)
		}
	})

	t.Run("non-owner ArmQuiz is rejected", func(t *testing.T) {
		t.Parallel()

		empty, cerr := h.service.CreateSession(ctx, nil, stranger.ID, false)
		if cerr != nil {
			t.Fatalf("CreateSession (empty room) err = %v, want nil", cerr)
		}
		if got, want := h.service.ArmQuiz(
			ctx,
			empty.JoinCode,
			stranger.ID,
			qz.ID,
			false,
		), ErrQuizNotOwned; !errors.Is(
			got,
			want,
		) {
			t.Errorf("ArmQuiz (non-owner, published) err = %v, want %v", got, want)
		}
	})

	t.Run("non-owner StartHosting is rejected", func(t *testing.T) {
		t.Parallel()

		sess, cerr := h.service.StartHosting(ctx, qz.ID, stranger.ID, false)
		if got, want := cerr, ErrQuizNotOwned; !errors.Is(got, want) {
			t.Errorf("StartHosting (non-owner, published) err = %v, want %v", got, want)
		}
		if sess != nil {
			t.Errorf("StartHosting (non-owner, published) session = %v, want nil", sess)
		}
	})

	t.Run("admin may host any quiz", func(t *testing.T) {
		t.Parallel()

		admin, aerr := h.playerStore.CreateAnonymousPlayer(ctx, "iso-admin")
		if aerr != nil {
			t.Fatalf("CreateAnonymousPlayer err = %v, want nil", aerr)
		}
		sess, cerr := h.service.CreateSession(ctx, &qz.ID, admin.ID, true)
		if cerr != nil {
			t.Fatalf("CreateSession (admin, foreign quiz) err = %v, want nil", cerr)
		}
		if sess == nil {
			t.Fatal("CreateSession (admin, foreign quiz) session = nil, want a room")
		}
	})
}

// TestService_ArmQuiz pins the arm-without-start contract (#863): ArmQuiz points
// an empty staging room at a live quiz and leaves it in the lobby (not started),
// and rejects a solo quiz.
func TestService_ArmQuiz(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 9, 12, 0, 0, 0, time.UTC)
	h := newEmptyRoomHarness(t, start)
	ctx := t.Context()

	const hostID int64 = 1
	empty, err := h.service.CreateSession(ctx, nil, hostID, false)
	if err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	qz := seedRunnerQuizSlug(t, h.quizStore, "armquiz-live", [][]bool{{true}})

	if err = h.service.ArmQuiz(ctx, empty.JoinCode, hostID, qz.ID, false); err != nil {
		t.Fatalf("ArmQuiz err = %v, want nil", err)
	}

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
	if got, want := armed.Phase, PhaseLobby; got != want {
		t.Errorf("armed Phase = %q, want %q (still in the lobby)", got, want)
	}
	if armed.StartedAt != nil {
		t.Errorf("armed StartedAt = %v, want nil (not started until the host presses Start)", armed.StartedAt)
	}

	// A solo quiz cannot be armed.
	solo := &quiz.Quiz{
		Title:             "Solo",
		Slug:              "armquiz-solo",
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
	if got, want := h.service.ArmQuiz(
		ctx,
		empty.JoinCode,
		hostID,
		solo.ID,
		false,
	), ErrNotLiveQuiz; !errors.Is(
		got,
		want,
	) {
		t.Errorf("ArmQuiz (solo) err = %v, want %v", got, want)
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
	sess, err := h.service.CreateSession(ctx, &qz.ID, hostID, false)
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
	sess, err := h.service.CreateSession(ctx, nil, hostID, false)
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

	sess, err := h.service.CreateSession(ctx, nil, hostID, false)
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

// TestService_StartHosting_RearmBeforeStartFromEmptyLobby pins re-arming before
// start (#877): from an empty staging lobby with players already in, the host
// arms quiz A, then changes their mind to B, then to C - all before pressing
// Start. The room must end up armed on the LAST pick (C), still in the lobby and
// not started, with no second room spawned and the roster carried across every
// re-arm. StartHosting -> ArmQuiz -> armRoomForHost -> RearmSession is idempotent
// and always reflects the latest quiz id.
func TestService_StartHosting_RearmBeforeStartFromEmptyLobby(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 9, 12, 0, 0, 0, time.UTC)
	h := newEmptyRoomHarness(t, start)
	ctx := t.Context()

	const hostID int64 = 1
	empty, err := h.service.CreateSession(ctx, nil, hostID, false)
	if err != nil {
		t.Fatalf("CreateSession (empty) err = %v, want nil", err)
	}
	p1 := h.joinNewPlayer(t, empty.JoinCode, "rearm-empty-a")
	p2 := h.joinNewPlayer(t, empty.JoinCode, "rearm-empty-b")
	wantRoster := []int64{p1, p2}
	slices.Sort(wantRoster)

	quizA := seedRunnerQuizSlug(t, h.quizStore, "rearm-empty-a", [][]bool{{true}})
	quizB := seedRunnerQuizSlug(t, h.quizStore, "rearm-empty-b", [][]bool{{true}})
	quizC := seedRunnerQuizSlug(t, h.quizStore, "rearm-empty-c", [][]bool{{true}})

	// The host arms A, then changes the pick to B, then to C, all before Start.
	for _, qz := range []*quiz.Quiz{quizA, quizB, quizC} {
		sess, hostErr := h.service.StartHosting(ctx, qz.ID, hostID, false)
		if hostErr != nil {
			t.Fatalf("StartHosting (arm %s) err = %v, want nil", qz.Slug, hostErr)
		}
		// No second room spawned: every pick reuses the one empty room.
		if got, want := sess.ID, empty.ID; got != want {
			t.Fatalf("StartHosting room ID = %q, want %q (same room, no second spawned)", got, want)
		}
	}

	armed, err := h.store.GetSessionByID(ctx, empty.ID)
	if err != nil {
		t.Fatalf("GetSessionByID err = %v, want nil", err)
	}
	// Armed on the LAST pick (C), not A or B: re-arm reflects the latest quiz id.
	if armed.QuizID == nil {
		t.Fatalf("armed QuizID = nil, want %d (last pick C)", quizC.ID)
	}
	if got, want := *armed.QuizID, quizC.ID; got != want {
		t.Errorf("armed QuizID = %d, want %d (last pick C, not A or B)", got, want)
	}
	// Still waiting in the lobby, not started: changing the pick never starts.
	if got, want := armed.Phase, PhaseLobby; got != want {
		t.Errorf("armed Phase = %q, want %q (armed but waiting in the lobby)", got, want)
	}
	if armed.StartedAt != nil {
		t.Errorf("armed StartedAt = %v, want nil (re-arming must not start the game)", armed.StartedAt)
	}
	// The first game from an empty lobby never bumps game_seq, and re-arming from
	// the lobby (not intermission) keeps it, so three lobby picks stay at 1.
	if got, want := armed.GameSeq, int64(1); got != want {
		t.Errorf("armed GameSeq = %d, want %d (lobby re-arms do not bump)", got, want)
	}
	// The roster is carried across every re-arm: nobody is dropped.
	if got := h.rosterIDs(t, empty.ID); !slices.Equal(got, wantRoster) {
		t.Errorf("roster after re-arms = %v, want %v (roster intact)", got, wantRoster)
	}
}
