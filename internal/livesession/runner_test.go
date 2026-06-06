package livesession_test

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/game"
	. "github.com/starquake/topbanana/internal/livesession"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// fakeClock is the injected, hand-advanced clock the runner reads. Tests step
// it forward and drive a tick, so a session marches through its phases with no
// real sleeping.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// runnerHarness bundles the real store-backed dependencies a runner test
// drives plus the session under test.
type runnerHarness struct {
	service *Service
	runner  *Runner
	clock   *fakeClock
	store   *store.LiveSessionStore
	db      *sql.DB
	code    string
	players []int64
}

// runnerCfg uses tiny beats so a single clock step crosses each threshold.
var runnerCfg = RunnerConfig{
	BeatInterval:     time.Millisecond,
	RoundIntroBeat:   time.Second,
	RevealBeat:       time.Second,
	RoundResultsBeat: time.Second,
	AutoStartWindow:  2 * time.Second,
	QuestionReadBeat: time.Second,
}

// newRunnerHarness seeds a live quiz with the given rounds (each a slice of
// option-correctness for its questions), opens a session, joins two players,
// and wires a runner over the real stores with a fake clock. The first option
// of each question is the one players pick in these tests. Two players is the
// shape every runner test needs: one to drive early-close / answered-order and
// a second to exercise the not-all-answered hold and the stale-player path.
func newRunnerHarness(t *testing.T, start time.Time, rounds [][]bool) *runnerHarness {
	t.Helper()

	const playerCount = 2

	db := dbtest.Open(t)
	logger := slog.New(slog.DiscardHandler)
	quizStore := store.NewQuizStore(db, logger)
	playerStore := store.NewPlayerStore(db, logger)
	sessionStore := store.NewLiveSessionStore(db, logger)

	qz := seedRunnerQuiz(t, quizStore, rounds)

	service := NewService(sessionStore, quizStore, logger)
	hub := NewHub()
	service.SetPublisher(hub)
	scorer := game.NewService(nil, quizStore, logger)
	clock := &fakeClock{now: start}
	runner := NewRunner(sessionStore, quizStore, hub, scorer, logger, runnerCfg)
	runner.SetClock(clock)
	service.SetAdvancer(runner)

	const hostID int64 = 1 // seeded admin
	sess := &Session{QuizID: qz.ID, HostPlayerID: hostID, JoinCode: "RUN234"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}

	players := make([]int64, 0, playerCount)
	for i := range playerCount {
		p, err := playerStore.CreateAnonymousPlayer(t.Context(), "runner-anon-"+string(rune('a'+i)))
		if err != nil {
			t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
		}
		if _, err := sessionStore.AddPlayer(t.Context(), sess.ID, p.ID); err != nil {
			t.Fatalf("AddPlayer err = %v, want nil", err)
		}
		players = append(players, p.ID)
	}

	return &runnerHarness{
		service: service,
		runner:  runner,
		clock:   clock,
		store:   sessionStore,
		db:      db,
		code:    sess.JoinCode,
		players: players,
	}
}

// setLastSeen backdates a roster player's last_seen_at so a runner test can
// place them on either side of the active-window cutoff (a stale player is one
// whose heartbeat stopped). Writes the timestamp in SQLite's CURRENT_TIMESTAMP
// text format ('YYYY-MM-DD HH:MM:SS'), matching how production stamps the
// column, so the store's same-encoding string comparison is exercised
// faithfully. Test-only fixture write.
func (h *runnerHarness) setLastSeen(t *testing.T, sessionID string, playerID int64, at time.Time) {
	t.Helper()
	if _, err := h.db.ExecContext(
		t.Context(),
		"UPDATE session_players SET last_seen_at = ? WHERE session_id = ? AND player_id = ?",
		at.UTC().Format("2006-01-02 15:04:05"), sessionID, playerID,
	); err != nil {
		t.Fatalf("setLastSeen err = %v, want nil", err)
	}
}

// setHostLastSeen backdates (or clears, when at is the zero time) a session's
// host_last_seen_at so a runner test can place the host on either side of the
// abandon cutoff. Writes the timestamp in SQLite's CURRENT_TIMESTAMP text
// format, matching how production stamps the column. Test-only fixture write.
func (h *runnerHarness) setHostLastSeen(t *testing.T, sessionID string, at time.Time) {
	t.Helper()
	if _, err := h.db.ExecContext(
		t.Context(),
		"UPDATE sessions SET host_last_seen_at = ? WHERE id = ?",
		at.UTC().Format("2006-01-02 15:04:05"), sessionID,
	); err != nil {
		t.Fatalf("setHostLastSeen err = %v, want nil", err)
	}
}

// seedRunnerQuiz authors a live quiz whose rounds carry the given questions;
// each question's options are correct per the bool slice. Every question gets
// a 10s window inherited from the quiz default.
func seedRunnerQuiz(t *testing.T, quizStore *store.QuizStore, rounds [][]bool) *quiz.Quiz {
	t.Helper()

	authored := make([]*quiz.Round, 0, len(rounds))
	pos := 1
	for ri, questions := range rounds {
		qs := make([]*quiz.Question, 0, len(questions))
		for range questions {
			qs = append(qs, &quiz.Question{
				Text:     "Q",
				Position: pos,
				Options: []*quiz.Option{
					{Text: "right", Correct: true},
					{Text: "wrong", Correct: false},
				},
			})
			pos++
		}
		authored = append(authored, &quiz.Round{Title: "R" + string(rune('1'+ri)), Questions: qs})
	}

	qz := &quiz.Quiz{
		Title:             "Runner Quiz",
		Slug:              "runner-quiz",
		Description:       "fixture",
		CreatedByPlayerID: 1,
		Mode:              quiz.ModeLive,
		Visibility:        quiz.VisibilityPublic,
		Rounds:            authored,
	}
	if err := quizStore.CreateQuiz(t.Context(), qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}

	return qz
}

func (h *runnerHarness) tick(ctx context.Context) {
	ExportRunnerTick(ctx, h.runner, h.clock.Now())
}

func (h *runnerHarness) reload(t *testing.T) *Session {
	t.Helper()
	sess, err := h.store.GetSessionByID(t.Context(), h.sessionID(t))
	if err != nil {
		t.Fatalf("GetSessionByID err = %v, want nil", err)
	}

	return sess
}

func (h *runnerHarness) sessionID(t *testing.T) string {
	t.Helper()
	sess, err := h.store.GetSessionByJoinCode(t.Context(), h.code)
	if err != nil {
		t.Fatalf("GetSessionByJoinCode err = %v, want nil", err)
	}

	return sess.ID
}

func (h *runnerHarness) phase(t *testing.T) Phase {
	t.Helper()

	return h.reload(t).Phase
}

// TestRunner_FullFlow drives a two-round session (two questions, then one)
// from host Start through every question to finished, asserting the phase
// order, the per-question deadlines, answered-order, no correctness before
// reveal and correctness present at reveal, the scoring formula, and both the
// early-close (all answered) and timeout-close paths.
func TestRunner_FullFlow(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true, true}, {true}})
	ctx := t.Context()

	// Host Start overrides immediately: lobby -> round_intro for round 1.
	if err := h.service.Start(ctx, h.code, 1); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}
	if got, want := h.phase(t), PhaseRoundIntro; got != want {
		t.Fatalf("phase after Start = %q, want %q", got, want)
	}

	// Round intro beat elapses -> first question issued with a server window.
	h.clock.advance(runnerCfg.RoundIntroBeat)
	h.tick(ctx)
	q1 := h.reload(t)
	if got, want := q1.Phase, PhaseQuestion; got != want {
		t.Fatalf("phase after intro beat = %q, want %q", got, want)
	}
	assertQuestionWindow(t, q1, h.clock.Now())
	firstQuestionID := *q1.CurrentQuestionID

	// EARLY-CLOSE PATH: both players answer the right option within the
	// window, so the question closes before the deadline.
	optRight := correctOptionID(ctx, t, h.service, h.code, 1)
	answerAt := h.clock.Now().Add(2 * time.Second)
	h.clock.advance(2 * time.Second)
	for _, pid := range h.players {
		if err := h.service.SubmitAnswer(ctx, h.code, pid, optRight, answerAt); err != nil {
			t.Fatalf("SubmitAnswer err = %v, want nil", err)
		}
	}

	// Pre-reveal: the state read exposes answered-order but NEVER correctness.
	assertNoCorrectnessBeforeReveal(ctx, t, h.service, h.code, h.players)

	// A tick with all active players answered closes the question early
	// (well before question_expires_at) and reveals.
	h.tick(ctx)
	if got, want := h.phase(t), PhaseReveal; got != want {
		t.Fatalf("phase after all answered = %q, want %q (early close)", got, want)
	}

	// At reveal: correctness and the formula score are exposed.
	assertRevealScores(ctx, t, h.service, h.code, h.players, answerAt, q1)

	// Reveal beat elapses -> second question of round 1.
	h.clock.advance(runnerCfg.RevealBeat)
	h.tick(ctx)
	q2 := h.reload(t)
	if got, want := q2.Phase, PhaseQuestion; got != want {
		t.Fatalf("phase after reveal beat = %q, want %q", got, want)
	}
	if got := *q2.CurrentQuestionID; got == firstQuestionID {
		t.Fatalf("second question id = %d, want a different question", got)
	}

	// TIMEOUT-CLOSE PATH: nobody answers; the question closes only once the
	// window expires.
	h.tick(ctx) // before the deadline: no transition.
	if got, want := h.phase(t), PhaseQuestion; got != want {
		t.Fatalf("phase before timeout = %q, want %q (must not close early)", got, want)
	}
	h.clock.advance(11 * time.Second) // past the 10s window.
	h.tick(ctx)
	if got, want := h.phase(t), PhaseReveal; got != want {
		t.Fatalf("phase after timeout = %q, want %q (timeout close)", got, want)
	}

	// Reveal beat -> round 1 done -> round_results (the between-rounds screen).
	h.clock.advance(runnerCfg.RevealBeat)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseRoundResults; got != want {
		t.Fatalf("phase after round 1 last reveal = %q, want %q (round results)", got, want)
	}

	// round_results beat -> round 2 intro.
	h.clock.advance(runnerCfg.RoundResultsBeat)
	h.tick(ctx)
	intro2 := h.reload(t)
	if got, want := intro2.Phase, PhaseRoundIntro; got != want {
		t.Fatalf("phase after round results = %q, want %q (next round intro)", got, want)
	}

	// Round 2 intro -> its single question -> timeout reveal -> finished.
	h.clock.advance(runnerCfg.RoundIntroBeat)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseQuestion; got != want {
		t.Fatalf("phase in round 2 = %q, want %q", got, want)
	}
	h.clock.advance(11 * time.Second)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseReveal; got != want {
		t.Fatalf("phase after round 2 question = %q, want %q", got, want)
	}

	// The last round also shows round_results before the session finishes.
	h.clock.advance(runnerCfg.RevealBeat)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseRoundResults; got != want {
		t.Fatalf("phase after round 2 reveal = %q, want %q (final round results)", got, want)
	}
	h.clock.advance(runnerCfg.RoundResultsBeat)
	h.tick(ctx)
	final := h.reload(t)
	if got, want := final.Phase, PhaseFinished; got != want {
		t.Fatalf("final phase = %q, want %q", got, want)
	}
	if final.FinishedAt == nil {
		t.Error("finished session has nil FinishedAt")
	}
}

// TestRunner_QuestionReadBeatAnchorsWindow pins that issuing a question opens
// the answer window after the read beat: StartedAt is the issue instant plus
// the read beat and ExpiresAt is StartedAt plus the question window, so the
// question text shows during [issuedAt, StartedAt) before the options open.
func TestRunner_QuestionReadBeatAnchorsWindow(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()

	if err := h.service.Start(ctx, h.code, 1); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}
	issuedAt := h.clock.Now().Add(runnerCfg.RoundIntroBeat)
	h.clock.advance(runnerCfg.RoundIntroBeat)
	h.tick(ctx)

	q := h.reload(t)
	if got, want := q.Phase, PhaseQuestion; got != want {
		t.Fatalf("phase after intro beat = %q, want %q", got, want)
	}
	assertQuestionWindow(t, q, issuedAt)
}

// TestRunner_DoesNotEarlyCloseDuringReadBeat pins that a question does not
// early-close while the read beat is still running: even with every active
// player present, there is nothing to early-close on until answers open at
// StartedAt, so a tick during [issuedAt, StartedAt) leaves the question phase
// in place.
func TestRunner_DoesNotEarlyCloseDuringReadBeat(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()
	sessionID := h.sessionID(t)

	if err := h.service.Start(ctx, h.code, 1); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}
	h.clock.advance(runnerCfg.RoundIntroBeat)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseQuestion; got != want {
		t.Fatalf("phase after intro beat = %q, want %q", got, want)
	}

	// Both players are active, but the read beat is still running, so nobody can
	// answer yet. A tick within [issuedAt, StartedAt) must not close.
	for _, pid := range h.players {
		h.setLastSeen(t, sessionID, pid, h.clock.Now())
	}
	h.tick(ctx)
	if got, want := h.phase(t), PhaseQuestion; got != want {
		t.Fatalf("phase during read beat = %q, want %q (must not early-close)", got, want)
	}

	// Once answers open, both active players answer and the question closes early.
	h.clock.advance(runnerCfg.QuestionReadBeat)
	optRight := correctOptionID(ctx, t, h.service, h.code, h.players[0])
	answerAt := h.clock.Now()
	for _, pid := range h.players {
		if err := h.service.SubmitAnswer(ctx, h.code, pid, optRight, answerAt); err != nil {
			t.Fatalf("SubmitAnswer err = %v, want nil", err)
		}
	}
	h.tick(ctx)
	if got, want := h.phase(t), PhaseReveal; got != want {
		t.Fatalf("phase after answers open and all answer = %q, want %q (early close)", got, want)
	}
}

// TestRunner_AutoStart drives the auto-start path: the lobby starts itself
// once every joined player has been ready for the auto-start window, with no
// host Start.
func TestRunner_AutoStart(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()

	// Not all ready yet: a tick must not start the game.
	h.tick(ctx)
	if got, want := h.phase(t), PhaseLobby; got != want {
		t.Fatalf("phase with no ready players = %q, want %q", got, want)
	}

	// Everyone readies; the window starts now (first all-ready observation).
	for _, pid := range h.players {
		if err := h.service.SetReady(ctx, h.code, pid, true); err != nil {
			t.Fatalf("SetReady err = %v, want nil", err)
		}
	}
	h.tick(ctx) // observes all-ready, seeds the window; still lobby.
	if got, want := h.phase(t), PhaseLobby; got != want {
		t.Fatalf("phase at window start = %q, want %q (window not elapsed)", got, want)
	}

	// Window elapses -> auto-start into round_intro.
	h.clock.advance(runnerCfg.AutoStartWindow)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseRoundIntro; got != want {
		t.Fatalf("phase after auto-start window = %q, want %q", got, want)
	}
}

// TestRunner_UnreadyResetsAutoStart pins that a player un-readying resets the
// auto-start window so the game does not start under the original timer.
func TestRunner_UnreadyResetsAutoStart(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()

	for _, pid := range h.players {
		if err := h.service.SetReady(ctx, h.code, pid, true); err != nil {
			t.Fatalf("SetReady err = %v, want nil", err)
		}
	}
	h.tick(ctx) // seeds the window.

	// One player un-readies before the window elapses; the window must reset.
	if err := h.service.SetReady(ctx, h.code, h.players[0], false); err != nil {
		t.Fatalf("SetReady false err = %v, want nil", err)
	}
	h.clock.advance(runnerCfg.AutoStartWindow)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseLobby; got != want {
		t.Fatalf("phase after un-ready = %q, want %q (window reset)", got, want)
	}
}

// TestRunner_StalePlayerDoesNotStallEarlyClose pins the MP-10 active-player
// rule: a session with one active player (fresh heartbeat) who answers and one
// stale player (last_seen far in the past) who never answers closes the
// question early once the active player has answered, rather than stalling
// until the answer window times out. A dropped player must not hold a question
// open.
func TestRunner_StalePlayerDoesNotStallEarlyClose(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()
	active, stale := h.players[0], h.players[1]
	sessionID := h.sessionID(t)

	if err := h.service.Start(ctx, h.code, 1); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}
	h.clock.advance(runnerCfg.RoundIntroBeat)
	h.tick(ctx)
	q := h.reload(t)
	if got, want := q.Phase, PhaseQuestion; got != want {
		t.Fatalf("phase after intro beat = %q, want %q", got, want)
	}

	// The stale player's heartbeat stopped long before the active window; the
	// active player beat just now.
	h.setLastSeen(t, sessionID, stale, start.Add(-time.Hour))
	h.setLastSeen(t, sessionID, active, h.clock.Now())

	// Only the active player answers, well within the window.
	optRight := correctOptionID(ctx, t, h.service, h.code, active)
	answerAt := h.clock.Now().Add(2 * time.Second)
	h.clock.advance(2 * time.Second)
	if err := h.service.SubmitAnswer(ctx, h.code, active, optRight, answerAt); err != nil {
		t.Fatalf("SubmitAnswer err = %v, want nil", err)
	}

	// A tick now must close the question early: every ACTIVE player has answered,
	// even though the stale player never did and the window has not expired.
	h.tick(ctx)
	if got, want := h.phase(t), PhaseReveal; got != want {
		t.Fatalf("phase after active player answered = %q, want %q (early close, ignoring stale)", got, want)
	}
}

// TestRunner_AllStaleRosterDoesNotEarlyClose pins that a roster with no active
// player never early-closes: the question must time out instead of closing
// instantly, preserving the empty-roster behaviour for an all-dropped room.
func TestRunner_AllStaleRosterDoesNotEarlyClose(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()
	sessionID := h.sessionID(t)

	if err := h.service.Start(ctx, h.code, 1); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}
	h.clock.advance(runnerCfg.RoundIntroBeat)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseQuestion; got != want {
		t.Fatalf("phase after intro beat = %q, want %q", got, want)
	}

	// Every player has gone stale.
	for _, pid := range h.players {
		h.setLastSeen(t, sessionID, pid, start.Add(-time.Hour))
	}

	// A tick before the window expires must NOT close: no active player to wait
	// on, so it falls through to the timeout path.
	h.tick(ctx)
	if got, want := h.phase(t), PhaseQuestion; got != want {
		t.Fatalf("phase with all-stale roster = %q, want %q (must not early-close)", got, want)
	}

	// Past the window, the timeout close still fires.
	h.clock.advance(11 * time.Second)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseReveal; got != want {
		t.Fatalf("phase after timeout = %q, want %q (timeout close)", got, want)
	}
}

// TestRunner_AbandonsHostGoneSession pins the MP-10 slice-3 sweep: a started,
// mid-game session whose host_last_seen_at is older than AbandonTimeout is
// finished by a runner tick, so a room whose host dropped does not linger live
// forever.
func TestRunner_AbandonsHostGoneSession(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()
	sessionID := h.sessionID(t)

	// Host starts the game, then drops: their last host beat is well past the
	// abandon cutoff.
	if err := h.service.Start(ctx, h.code, 1); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}
	if got, want := h.phase(t), PhaseRoundIntro; got != want {
		t.Fatalf("phase after Start = %q, want %q", got, want)
	}
	h.setHostLastSeen(t, sessionID, h.clock.Now().Add(-AbandonTimeout-time.Minute))

	h.tick(ctx)
	final := h.reload(t)
	if got, want := final.Phase, PhaseFinished; got != want {
		t.Fatalf("phase after abandon sweep = %q, want %q", got, want)
	}
	if final.FinishedAt == nil {
		t.Error("abandoned session has nil FinishedAt")
	}
}

// TestRunner_DoesNotAbandonWithFreshHostBeat pins that a mid-game session whose
// host beat recently is left running by the sweep.
func TestRunner_DoesNotAbandonWithFreshHostBeat(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()
	sessionID := h.sessionID(t)

	if err := h.service.Start(ctx, h.code, 1); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}
	// The host beat just now, well inside the abandon window.
	h.setHostLastSeen(t, sessionID, h.clock.Now())

	h.tick(ctx)
	if got, want := h.phase(t), PhaseRoundIntro; got != want {
		t.Errorf("phase with fresh host beat = %q, want %q (not abandoned)", got, want)
	}
}

// TestRunner_DoesNotAbandonLobby pins that the sweep never finishes a lobby:
// only a started session is in scope, so a host who is slow to start (or never
// beat) leaves the lobby intact rather than terminating it.
func TestRunner_DoesNotAbandonLobby(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()
	sessionID := h.sessionID(t)

	// A lobby with a long-stale host beat (and no started_at) must not be swept.
	h.setHostLastSeen(t, sessionID, start.Add(-AbandonTimeout-time.Hour))

	h.clock.advance(AbandonTimeout + time.Hour)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseLobby; got != want {
		t.Errorf("lobby phase after sweep = %q, want %q (lobby never abandoned)", got, want)
	}
}

// assertQuestionWindow pins that issuing a question opens the answer window
// after the read beat: StartedAt is the issue instant plus the read beat (the
// question text shows during [issuedAt, StartedAt)), and ExpiresAt is StartedAt
// plus the 10s default window.
func assertQuestionWindow(t *testing.T, sess *Session, issuedAt time.Time) {
	t.Helper()
	if sess.QuestionStartedAt == nil || sess.QuestionExpiresAt == nil {
		t.Fatal("question phase has nil StartedAt/ExpiresAt")
	}
	wantStart := issuedAt.Add(runnerCfg.QuestionReadBeat)
	if got, want := *sess.QuestionStartedAt, wantStart; !got.Equal(want) {
		t.Errorf("QuestionStartedAt = %v, want %v (issued + read beat)", got, want)
	}
	if got, want := *sess.QuestionExpiresAt, wantStart.Add(10*time.Second); !got.Equal(want) {
		t.Errorf("QuestionExpiresAt = %v, want %v (StartedAt + 10s default window)", got, want)
	}
}

// correctOptionID returns the id of the live question's correct option by
// reading state (which exposes options without a correct flag pre-reveal) and
// cross-referencing the quiz - the test knows option index 0 is correct.
func correctOptionID(ctx context.Context, t *testing.T, service *Service, code string, viewer int64) int64 {
	t.Helper()
	state, err := service.GetLobbyState(ctx, code, viewer)
	if err != nil {
		t.Fatalf("GetLobbyState err = %v, want nil", err)
	}
	if state.CurrentQuestion == nil {
		t.Fatal("no current question in state")
	}
	for _, o := range state.CurrentQuestion.Options {
		if o.Correct {
			return o.ID
		}
	}
	t.Fatal("no correct option on current question")

	return 0
}

func assertNoCorrectnessBeforeReveal(
	ctx context.Context, t *testing.T, service *Service, code string, players []int64,
) {
	t.Helper()
	state, err := service.GetLobbyState(ctx, code, players[0])
	if err != nil {
		t.Fatalf("GetLobbyState err = %v, want nil", err)
	}
	if state.Revealed {
		t.Fatal("state reports Revealed in the question phase")
	}
	if got, want := len(state.Answers), len(players); got != want {
		t.Fatalf("answered count = %d, want %d", got, want)
	}
	// Answered-order is the submit order; the picks carry no score yet.
	for i, a := range state.Answers {
		if got, want := a.PlayerID, players[i]; got != want {
			t.Errorf("answered[%d].PlayerID = %d, want %d (answered order)", i, got, want)
		}
		if a.Score != nil {
			t.Errorf("answered[%d].Score = %v, want nil before reveal", i, *a.Score)
		}
	}
}

func assertRevealScores(
	ctx context.Context,
	t *testing.T,
	service *Service,
	code string,
	players []int64,
	answeredAt time.Time,
	question *Session,
) {
	t.Helper()
	state, err := service.GetLobbyState(ctx, code, players[0])
	if err != nil {
		t.Fatalf("GetLobbyState err = %v, want nil", err)
	}
	if !state.Revealed {
		t.Fatal("state does not report Revealed in the reveal phase")
	}
	if got, want := len(state.Answers), len(players); got != want {
		t.Fatalf("reveal answered count = %d, want %d", got, want)
	}

	// Both players answered the correct option partway into the 10s window
	// (anchored at StartedAt, after the read beat), so the formula yields a
	// score between 0 and 1000; scoreAt mirrors the curve for the expectation.
	wantScore := scoreAt(question, answeredAt)
	for _, a := range state.Answers {
		if !a.Correct {
			t.Errorf("answer for player %d Correct = false, want true", a.PlayerID)
		}
		if a.Score == nil {
			t.Fatalf("answer for player %d has nil Score at reveal", a.PlayerID)
		}
		if got := *a.Score; got != wantScore {
			t.Errorf("answer for player %d Score = %d, want %d", a.PlayerID, got, wantScore)
		}
	}
}

// scoreAt mirrors the CalculateScore curve for the test's expectation: a
// correct pick scores 1000 at StartedAt, falling linearly to 0 at ExpiresAt.
func scoreAt(sess *Session, answeredAt time.Time) int {
	window := sess.QuestionExpiresAt.Sub(*sess.QuestionStartedAt)
	elapsed := answeredAt.Sub(*sess.QuestionStartedAt)

	return int(1000 - (elapsed.Seconds()/window.Seconds())*1000)
}
