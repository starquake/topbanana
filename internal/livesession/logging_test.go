package livesession_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/game"
	. "github.com/starquake/topbanana/internal/livesession"
	"github.com/starquake/topbanana/internal/store"
)

// captureHandler is a slog.Handler that records every record at Debug+ so a
// test can assert the live-session domain layer emitted the expected log line
// with the expected level and fields. Concurrency-safe because the runner
// detaches its first-round transition onto a goroutine via the advancer.
//
// WithAttrs accumulates the bound attrs and Handle merges them onto each
// record, so a logger derived via slog.With surfaces its bound fields here:
// slog.With binds attrs at the handler level (WithAttrs), not on the Record,
// so returning the receiver unchanged would silently drop them.
type captureHandler struct {
	mu      *sync.Mutex
	records *[]slog.Record
	attrs   []slog.Attr
}

func newCaptureHandler() captureHandler {
	return captureHandler{mu: &sync.Mutex{}, records: &[]slog.Record{}}
}

func (captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h captureHandler) Handle(_ context.Context, r slog.Record) error {
	rec := r.Clone()
	rec.AddAttrs(h.attrs...)

	h.mu.Lock()
	defer h.mu.Unlock()
	*h.records = append(*h.records, rec)

	return nil
}

func (h captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)

	return h
}

func (h captureHandler) WithGroup(string) slog.Handler { return h }

// snapshot returns a copy of the records captured so far.
func (h captureHandler) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()

	return append([]slog.Record(nil), *h.records...)
}

// findLog returns the first captured record with the given message, plus
// whether one was found.
func (h captureHandler) findLog(msg string) (slog.Record, bool) {
	for _, r := range h.snapshot() {
		if r.Message == msg {
			return r, true
		}
	}

	return slog.Record{}, false
}

// assertLog asserts that a record with msg was captured at level want, and
// returns its attributes keyed by name so the caller can check fields.
func assertLog(t *testing.T, h captureHandler, msg string, want slog.Level) map[string]slog.Value {
	t.Helper()

	rec, ok := h.findLog(msg)
	if !ok {
		t.Fatalf("no log record with message %q (captured: %v)", msg, logMessages(h))
	}
	if got := rec.Level; got != want {
		t.Errorf("log %q level = %v, want %v", msg, got, want)
	}

	attrs := make(map[string]slog.Value, rec.NumAttrs())
	rec.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value

		return true
	})

	return attrs
}

// assertNoLog asserts that no record with msg was captured.
func assertNoLog(t *testing.T, h captureHandler, msg string) {
	t.Helper()
	if _, ok := h.findLog(msg); ok {
		t.Errorf("log %q was emitted, want none", msg)
	}
}

// logMessages lists the captured messages so a failed assertion can show what
// was actually logged.
func logMessages(h captureHandler) []string {
	recs := h.snapshot()
	msgs := make([]string, 0, len(recs))
	for _, r := range recs {
		msgs = append(msgs, r.Message)
	}

	return msgs
}

func assertStringAttr(t *testing.T, attrs map[string]slog.Value, key, want string) {
	t.Helper()
	if got := attrs[key].String(); got != want {
		t.Errorf("attr %q = %q, want %q", key, got, want)
	}
}

func assertInt64Attr(t *testing.T, attrs map[string]slog.Value, key string, want int64) {
	t.Helper()
	if got := attrs[key].Int64(); got != want {
		t.Errorf("attr %q = %d, want %d", key, got, want)
	}
}

// loggingHarness wires the live-session service + runner over real stores with
// a capturing logger, so a test drives the service and asserts the emitted
// domain log lines. Mirrors newRunnerHarness but exposes the captured records.
type loggingHarness struct {
	service *Service
	runner  *Runner
	clock   *fakeClock
	store   *store.LiveSessionStore
	logs    captureHandler
	code    string
	players []int64
}

func newLoggingHarness(t *testing.T, start time.Time, rounds [][]bool) *loggingHarness {
	t.Helper()

	const playerCount = 2

	db := dbtest.Open(t)
	logs := newCaptureHandler()
	logger := slog.New(logs)
	discard := slog.New(slog.DiscardHandler)
	quizStore := store.NewQuizStore(db, discard)
	playerStore := store.NewPlayerStore(db, discard)
	sessionStore := store.NewLiveSessionStore(db, discard)

	qz := seedRunnerQuiz(t, quizStore, rounds)

	service := NewService(sessionStore, quizStore, logger)
	hub := NewHub()
	service.SetPublisher(hub)
	service.SetStartCountdown(startCountdown)
	scorer := game.NewService(nil, quizStore, discard)
	clock := &fakeClock{now: start}
	runner := NewRunner(sessionStore, quizStore, hub, scorer, discard, runnerCfg)
	runner.SetClock(clock)
	service.SetAdvancer(runner)

	const hostID int64 = 1 // seeded admin
	sess := &Session{QuizID: quizIDPtr(qz.ID), HostPlayerID: hostID, JoinCode: "LOG234"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}

	players := make([]int64, 0, playerCount)
	for i := range playerCount {
		p, err := playerStore.CreateAnonymousPlayer(t.Context(), "log-anon-"+string(rune('a'+i)))
		if err != nil {
			t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
		}
		players = append(players, p.ID)
	}

	return &loggingHarness{
		service: service,
		runner:  runner,
		clock:   clock,
		store:   sessionStore,
		logs:    logs,
		code:    sess.JoinCode,
		players: players,
	}
}

func (h *loggingHarness) tick(ctx context.Context) {
	ExportRunnerTick(ctx, h.runner, h.clock.Now())
}

// TestService_LogsLiveGameNightFlow drives a representative live game night
// through the service - create, join, arm-start, host start, a submitted
// answer, and end - and asserts each milestone emits its log line at the right
// level with the identifying fields a host needs to reconstruct the night.
func TestService_LogsLiveGameNightFlow(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newLoggingHarness(t, start, [][]bool{{true}})
	ctx := t.Context()

	const hostID int64 = 1

	if _, err := h.service.Join(ctx, h.code, h.players[0]); err != nil {
		t.Fatalf("Join err = %v, want nil", err)
	}
	if _, err := h.service.Join(ctx, h.code, h.players[1]); err != nil {
		t.Fatalf("Join err = %v, want nil", err)
	}

	joinAttrs := assertLog(t, h.logs, "player joined live session", slog.LevelInfo)
	assertStringAttr(t, joinAttrs, "joinCode", h.code)
	assertInt64Attr(t, joinAttrs, "player", h.players[0])

	if err := h.service.SetReady(ctx, h.code, h.players[0], true); err != nil {
		t.Fatalf("SetReady err = %v, want nil", err)
	}
	readyAttrs := assertLog(t, h.logs, "player ready toggled", slog.LevelDebug)
	assertInt64Attr(t, readyAttrs, "player", h.players[0])
	if got := readyAttrs["ready"].Bool(); !got {
		t.Errorf("ready attr = %v, want true", got)
	}

	if err := h.service.ArmStart(ctx, h.code, hostID, h.clock.Now()); err != nil {
		t.Fatalf("ArmStart err = %v, want nil", err)
	}
	armAttrs := assertLog(t, h.logs, "live session start countdown armed", slog.LevelInfo)
	assertInt64Attr(t, armAttrs, "host", hostID)
	if got, want := armAttrs["deadline"].Time(), h.clock.Now().Add(startCountdown); !got.Equal(want) {
		t.Errorf("deadline attr = %v, want %v", got, want)
	}

	if err := h.service.Start(ctx, h.code, hostID); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}
	startAttrs := assertLog(t, h.logs, "live session started", slog.LevelInfo)
	assertStringAttr(t, startAttrs, "joinCode", h.code)
	assertInt64Attr(t, startAttrs, "host", hostID)

	// Drive into the question phase and submit an accepted answer.
	h.clock.advance(runnerCfg.RoundIntroBeat)
	h.tick(ctx)
	state, err := h.service.GetSessionState(ctx, h.code, h.players[0])
	if err != nil {
		t.Fatalf("GetSessionState err = %v, want nil", err)
	}
	if state.CurrentQuestion == nil {
		t.Fatalf("no current question after round intro beat (phase=%q)", state.Session.Phase)
	}
	optID := state.CurrentQuestion.Options[0].ID
	answerAt := h.clock.Now().Add(time.Second)
	h.clock.advance(time.Second)
	if err := h.service.SubmitAnswer(ctx, h.code, h.players[0], optID, answerAt); err != nil {
		t.Fatalf("SubmitAnswer err = %v, want nil", err)
	}
	answerAttrs := assertLog(t, h.logs, "answer accepted", slog.LevelDebug)
	assertInt64Attr(t, answerAttrs, "player", h.players[0])
	assertInt64Attr(t, answerAttrs, "option", optID)
	assertInt64Attr(t, answerAttrs, "question", *state.Session.CurrentQuestionID)

	if err := h.service.EndSession(ctx, h.code, hostID); err != nil {
		t.Fatalf("EndSession err = %v, want nil", err)
	}
	endAttrs := assertLog(t, h.logs, "live session ended", slog.LevelInfo)
	assertStringAttr(t, endAttrs, "joinCode", h.code)
	assertInt64Attr(t, endAttrs, "host", hostID)
}

// TestService_LogsCreateSession pins the create milestone, including the quiz
// id field that only appears when a quiz is armed up front.
func TestService_LogsCreateSession(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newLoggingHarness(t, start, [][]bool{{true}})
	ctx := t.Context()

	const hostID int64 = 1
	sess, err := h.service.CreateSession(ctx, nil, hostID, false)
	if err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	attrs := assertLog(t, h.logs, "live session created", slog.LevelInfo)
	assertStringAttr(t, attrs, "session", sess.ID)
	assertStringAttr(t, attrs, "joinCode", sess.JoinCode)
	assertInt64Attr(t, attrs, "host", hostID)
	if _, ok := attrs["quiz"]; ok {
		t.Error("quiz attr present for a quiz-less room, want absent")
	}
}

// TestService_LogsClosedRoomJoinRejection pins that a join into a finished room
// names the reason at Info, not just a bare 409 in the access log.
func TestService_LogsClosedRoomJoinRejection(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newLoggingHarness(t, start, [][]bool{{true}})
	ctx := t.Context()

	const hostID int64 = 1
	if err := h.service.EndSession(ctx, h.code, hostID); err != nil {
		t.Fatalf("EndSession err = %v, want nil", err)
	}

	if _, err := h.service.Join(ctx, h.code, h.players[0]); err == nil {
		t.Fatal("Join into a closed room err = nil, want ErrLobbyClosed")
	}
	attrs := assertLog(t, h.logs, "live session join rejected: room closed", slog.LevelInfo)
	assertStringAttr(t, attrs, "joinCode", h.code)
	assertInt64Attr(t, attrs, "player", h.players[0])
	assertNoLog(t, h.logs, "player joined live session")
}

// TestService_LogsNotParticipantAnswerRejection pins the not-a-participant
// rejection log at Info for an answer from a player who never joined.
func TestService_LogsNotParticipantAnswerRejection(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newLoggingHarness(t, start, [][]bool{{true}})
	ctx := t.Context()

	const stranger int64 = 999
	if err := h.service.SubmitAnswer(ctx, h.code, stranger, 1, h.clock.Now()); err == nil {
		t.Fatal("SubmitAnswer from a non-participant err = nil, want ErrNotParticipant")
	}
	attrs := assertLog(t, h.logs, "answer rejected: not a participant", slog.LevelInfo)
	assertStringAttr(t, attrs, "joinCode", h.code)
	assertInt64Attr(t, attrs, "player", stranger)
}

// TestService_LogsQuestionNotOpenAnswerRejection pins the question-not-open
// rejection log at Debug, naming the wrong-phase reason for an answer submitted
// while the room is still in the lobby.
func TestService_LogsQuestionNotOpenAnswerRejection(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newLoggingHarness(t, start, [][]bool{{true}})
	ctx := t.Context()

	if _, err := h.service.Join(ctx, h.code, h.players[0]); err != nil {
		t.Fatalf("Join err = %v, want nil", err)
	}
	// Still in the lobby: no question is open, so the pick is rejected.
	if err := h.service.SubmitAnswer(ctx, h.code, h.players[0], 1, h.clock.Now()); err == nil {
		t.Fatal("SubmitAnswer in the lobby err = nil, want ErrQuestionNotOpen")
	}
	attrs := assertLog(t, h.logs, "answer rejected: question not open", slog.LevelDebug)
	assertInt64Attr(t, attrs, "player", h.players[0])
	assertStringAttr(t, attrs, "phase", string(PhaseLobby))
	assertStringAttr(t, attrs, "reason", "wrong-phase")
}

// TestService_LogsNonHostControlRejection pins that a non-host attempt to start
// a room names the reason at Info, not just a bare 403 in the access log.
func TestService_LogsNonHostControlRejection(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newLoggingHarness(t, start, [][]bool{{true}})
	ctx := t.Context()

	const notHost int64 = 999
	if err := h.service.Start(ctx, h.code, notHost); err == nil {
		t.Fatal("Start by a non-host err = nil, want ErrNotHost")
	}
	attrs := assertLog(t, h.logs, "live session control rejected: not host", slog.LevelInfo)
	assertStringAttr(t, attrs, "action", "start")
	assertStringAttr(t, attrs, "joinCode", h.code)
	assertInt64Attr(t, attrs, "player", notHost)
	assertNoLog(t, h.logs, "live session started")
}

// TestCaptureHandler_SurfacesWithBoundAttrs pins that the capture handler
// honours WithAttrs, so a logger derived via slog.With carries its bound
// field through to the asserted record. Without the fix this fails: a no-op
// WithAttrs drops the bound field. Guards future slog.With use in this layer.
func TestCaptureHandler_SurfacesWithBoundAttrs(t *testing.T) {
	t.Parallel()

	logs := newCaptureHandler()
	logger := slog.New(logs).With(slog.String("joinCode", "ABC123"))
	logger.Info("bound line")

	attrs := assertLog(t, logs, "bound line", slog.LevelInfo)
	assertStringAttr(t, attrs, "joinCode", "ABC123")
}
