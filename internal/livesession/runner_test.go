package livesession_test

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"slices"
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
	hub     *Hub
	db      *sql.DB
	code    string
	players []int64
}

// runnerCfg uses tiny beats so a single clock step crosses each threshold.
// IdleCloseTimeout is a small, explicit window so the idle-close tests can place
// host presence on either side of it without paying the 30-minute default.
var runnerCfg = RunnerConfig{
	BeatInterval:     time.Millisecond,
	RoundIntroBeat:   time.Second,
	RevealBeat:       time.Second,
	RoundResultsBeat: time.Second,
	QuestionReadBeat: time.Second,
	IdleCloseTimeout: idleCloseTimeout,
}

// idleCloseTimeout is the runner harness's idle-close window: a room whose host
// has been gone this long AND has no active players is closed by the sweep.
const idleCloseTimeout = 2 * time.Minute

// startCountdown is the host-armed last-call window the runner harness sets on
// the service, so ArmStart stamps a deadline this far ahead of the clock.
const startCountdown = 2 * time.Second

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
	service.SetStartCountdown(startCountdown)
	scorer := game.NewService(nil, quizStore, logger)
	clock := &fakeClock{now: start}
	runner := NewRunner(sessionStore, quizStore, hub, scorer, logger, runnerCfg)
	runner.SetClock(clock)
	service.SetAdvancer(runner)

	const hostID int64 = 1 // seeded admin
	sess := &Session{QuizID: quizIDPtr(qz.ID), HostPlayerID: hostID, JoinCode: "RUN234"}
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
		hub:     hub,
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
// idle-close cutoff. Writes the timestamp in SQLite's CURRENT_TIMESTAMP text
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

// staleAllPlayers backdates every roster player's last_seen_at far before the
// active window (relative to the harness clock), so the idle-close sweep sees no
// active players. AddPlayer stamps last_seen_at to the real wall clock, which is
// well after the harness's 2026-06-05 fake clock and so would otherwise count as
// active; this drags them all back to genuinely-gone. Test-only fixture write.
func (h *runnerHarness) staleAllPlayers(t *testing.T, sessionID string) {
	t.Helper()
	stale := h.clock.Now().Add(-ActiveWindow - time.Minute)
	for _, playerID := range h.players {
		h.setLastSeen(t, sessionID, playerID, stale)
	}
}

// seedRunnerQuiz authors a live quiz with the default slug; the harness seeds
// exactly one. A test that needs a second quiz in the same DB (the re-arm path)
// uses seedRunnerQuizSlug with a distinct slug so the two do not collide on the
// quizzes.slug UNIQUE.
func seedRunnerQuiz(t *testing.T, quizStore *store.QuizStore, rounds [][]bool) *quiz.Quiz {
	t.Helper()

	return seedRunnerQuizSlug(t, quizStore, "runner-quiz", rounds)
}

// seedRunnerQuizSlug authors a live quiz under the given slug whose rounds carry
// the given questions; each question's options are correct per the bool slice.
// Every question gets a 10s window inherited from the quiz default.
func seedRunnerQuizSlug(t *testing.T, quizStore *store.QuizStore, slug string, rounds [][]bool) *quiz.Quiz {
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
		Slug:              slug,
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

// rosterIDs returns the active roster player ids for the room, sorted ascending,
// so a multi-game test can assert the roster carried across a re-arm unchanged.
func (h *runnerHarness) rosterIDs(t *testing.T) []int64 {
	t.Helper()
	sess := h.reload(t)
	ids := make([]int64, 0, len(sess.Players))
	for _, p := range sess.Players {
		ids = append(ids, p.PlayerID)
	}
	slices.Sort(ids)

	return ids
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

	// Round 2 intro -> its single question -> timeout reveal -> intermission.
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

	// The last round's reveal ends the game directly into intermission (#836),
	// skipping round_results, so the game ends on a single final-standings screen
	// and the room stays alive for the host to arm the next quiz.
	h.clock.advance(runnerCfg.RevealBeat)
	h.tick(ctx)
	final := h.reload(t)
	if got, want := final.Phase, PhaseIntermission; got != want {
		t.Fatalf("phase after final round reveal = %q, want %q (skips round_results)", got, want)
	}
	if final.FinishedAt == nil {
		t.Error("game ended at intermission has nil FinishedAt")
	}
}

// TestRunner_FinalRoundSkipsRoundResults pins that the last round transitions
// from its closing reveal straight to intermission, never showing the
// between-rounds round_results screen, so the game ends on a single
// final-standings screen (#749). A single-round quiz isolates the final-round
// path.
func TestRunner_FinalRoundSkipsRoundResults(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()

	if err := h.service.Start(ctx, h.code, 1); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}

	// Round intro -> the single question -> timeout reveal.
	h.clock.advance(runnerCfg.RoundIntroBeat)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseQuestion; got != want {
		t.Fatalf("phase after intro beat = %q, want %q", got, want)
	}
	h.clock.advance(11 * time.Second)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseReveal; got != want {
		t.Fatalf("phase after question = %q, want %q", got, want)
	}

	// The reveal beat elapses on the only round: the runner ends the game
	// directly into intermission, never entering round_results.
	h.clock.advance(runnerCfg.RevealBeat)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseIntermission; got != want {
		t.Fatalf("phase after final round reveal = %q, want %q (no round_results)", got, want)
	}
}

// TestRunner_IntermissionKeepsHubVersion pins that a game ending into
// intermission keeps the room alive (#836): the hub version entry survives, so
// SSE keeps working and the host can re-arm the next quiz. The #791 leak fix
// (evicting the entry on a terminal finish) still holds for the idle-close path
// - see TestRunner_IdleCloseForgetsHubVersion - but a normal game end is no
// longer terminal.
func TestRunner_IntermissionKeepsHubVersion(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()

	// Host Start publishes the first tick, so the hub now holds a version entry.
	if err := h.service.Start(ctx, h.code, 1); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}
	if got, want := ExportHubHasVersion(h.hub, h.code), true; got != want {
		t.Fatalf("has version while running = %v, want %v", got, want)
	}

	// Drive the single round to intermission: intro beat -> question -> timeout
	// reveal -> end of game.
	h.clock.advance(runnerCfg.RoundIntroBeat)
	h.tick(ctx)
	h.clock.advance(11 * time.Second)
	h.tick(ctx)
	h.clock.advance(runnerCfg.RevealBeat)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseIntermission; got != want {
		t.Fatalf("phase after final reveal = %q, want %q", got, want)
	}

	// The room is alive between games: the version entry is kept, not evicted.
	if got, want := ExportHubHasVersion(h.hub, h.code), true; got != want {
		t.Errorf("has version at intermission = %v, want %v (room stays alive)", got, want)
	}
}

// TestRunner_IdleCloseForgetsHubVersion pins that the terminal idle-close path
// still evicts the hub version entry (#791, #836): a room gone idle (host past
// the idle timeout AND no active players) is closed for good (finished + evict),
// so it does not pin its version entry for the process lifetime.
func TestRunner_IdleCloseForgetsHubVersion(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()
	sessionID := h.sessionID(t)

	if err := h.service.Start(ctx, h.code, 1); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}
	if got, want := ExportHubHasVersion(h.hub, h.code), true; got != want {
		t.Fatalf("has version while running = %v, want %v", got, want)
	}

	// The host drops past the idle cutoff and every player goes stale, so the room
	// is genuinely idle; the next tick closes it.
	h.setHostLastSeen(t, sessionID, h.clock.Now().Add(-idleCloseTimeout-time.Minute))
	h.staleAllPlayers(t, sessionID)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseFinished; got != want {
		t.Fatalf("phase after idle-close sweep = %q, want %q", got, want)
	}
	if got, want := ExportHubHasVersion(h.hub, h.code), false; got != want {
		t.Errorf("has version after terminal finish = %v, want %v (entry must be evicted)", got, want)
	}
}

// TestRunner_FinishedStandingsCarryLastRoundScore pins that the finished-phase
// standings expose each player's score in the last round as RoundScore so the
// bar graph can animate that final contribution (#729). It drives a single-round
// quiz to intermission (the end-of-game screen, #836) with one player answering
// correctly and the other not, then asserts the answerer's final RoundScore
// equals the last round's points (here the whole total, since the only round is
// the last one) and the non-answerer's stays 0.
func TestRunner_FinishedStandingsCarryLastRoundScore(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()
	scorer, slacker := h.players[0], h.players[1]

	if err := h.service.Start(ctx, h.code, 1); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}

	// Round intro -> the single question; only the scorer answers, correctly.
	h.clock.advance(runnerCfg.RoundIntroBeat)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseQuestion; got != want {
		t.Fatalf("phase after intro beat = %q, want %q", got, want)
	}
	optRight := correctOptionID(ctx, t, h.service, h.code, scorer)
	answerAt := h.clock.Now().Add(2 * time.Second)
	h.clock.advance(2 * time.Second)
	if err := h.service.SubmitAnswer(ctx, h.code, scorer, optRight, answerAt); err != nil {
		t.Fatalf("SubmitAnswer err = %v, want nil", err)
	}

	// Timeout-close the question, then the reveal beat ends the game.
	h.clock.advance(11 * time.Second)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseReveal; got != want {
		t.Fatalf("phase after question = %q, want %q", got, want)
	}
	h.clock.advance(runnerCfg.RevealBeat)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseIntermission; got != want {
		t.Fatalf("phase after final reveal = %q, want %q", got, want)
	}

	state, err := h.service.GetSessionState(ctx, h.code, scorer)
	if err != nil {
		t.Fatalf("GetSessionState err = %v, want nil", err)
	}
	scorerStanding := findRunnerStanding(t, state.Standings, scorer)
	slackerStanding := findRunnerStanding(t, state.Standings, slacker)

	if scorerStanding.RoundScore <= 0 {
		t.Errorf("scorer finished RoundScore = %d, want > 0 (last round's points)", scorerStanding.RoundScore)
	}
	// The only round is the last round, so its score is the whole cumulative total.
	if got, want := scorerStanding.RoundScore, scorerStanding.TotalScore; got != want {
		t.Errorf("scorer finished RoundScore = %d, want %d (equals total in a single-round quiz)", got, want)
	}
	if got, want := slackerStanding.RoundScore, 0; got != want {
		t.Errorf("slacker finished RoundScore = %d, want %d (scored nothing in the last round)", got, want)
	}
	if got, want := slackerStanding.TotalScore, 0; got != want {
		t.Errorf("slacker finished TotalScore = %d, want %d", got, want)
	}
}

// findRunnerStanding returns the standing for playerID, failing if absent.
func findRunnerStanding(t *testing.T, standings []*Standing, playerID int64) *Standing {
	t.Helper()
	for _, s := range standings {
		if s.PlayerID == playerID {
			return s
		}
	}
	t.Fatalf("standings missing player %d", playerID)

	return nil
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

// TestRunner_ArmedStartFiresAtDeadline drives the host-armed last-call
// countdown (#735): a lobby with no armed start_at never starts on its own,
// arming stamps a deadline, a tick before the deadline holds in the lobby, and
// a tick at or after the deadline starts the game into round_intro.
func TestRunner_ArmedStartFiresAtDeadline(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()

	// No armed countdown: a tick (even with players present) must not start.
	h.tick(ctx)
	if got, want := h.phase(t), PhaseLobby; got != want {
		t.Fatalf("phase with no armed start = %q, want %q", got, want)
	}

	// Host arms the countdown; the deadline is now + startCountdown.
	if err := h.service.ArmStart(ctx, h.code, 1, h.clock.Now()); err != nil {
		t.Fatalf("ArmStart err = %v, want nil", err)
	}
	if got := h.reload(t).StartAt; got == nil {
		t.Fatal("StartAt after ArmStart = nil, want a deadline")
	}

	// A tick before the deadline holds in the lobby.
	h.clock.advance(startCountdown - time.Millisecond)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseLobby; got != want {
		t.Fatalf("phase before deadline = %q, want %q", got, want)
	}

	// At the deadline the runner starts the game into round_intro.
	h.clock.advance(time.Millisecond)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseRoundIntro; got != want {
		t.Fatalf("phase at armed deadline = %q, want %q", got, want)
	}
}

// TestRunner_CancelStartStopsCountdown pins that cancelling an armed countdown
// clears start_at so the deadline passing no longer starts the game.
func TestRunner_CancelStartStopsCountdown(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()

	if err := h.service.ArmStart(ctx, h.code, 1, h.clock.Now()); err != nil {
		t.Fatalf("ArmStart err = %v, want nil", err)
	}
	if err := h.service.CancelStart(ctx, h.code, 1); err != nil {
		t.Fatalf("CancelStart err = %v, want nil", err)
	}
	if got := h.reload(t).StartAt; got != nil {
		t.Fatalf("StartAt after CancelStart = %v, want nil", got)
	}

	// The original deadline passes, but with start_at cleared the game holds.
	h.clock.advance(startCountdown + time.Second)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseLobby; got != want {
		t.Fatalf("phase after cancel + deadline = %q, want %q (no start)", got, want)
	}
}

// TestRunner_RecoversStartedSessionStuckInLobby pins the #781 self-heal: a
// session marked started (started_at set) but still in the lobby - the state
// left behind when host "Start now" won MarkStarted but the detached
// first-round transition was abandoned (host disconnect) before it ran - is
// advanced into round_intro on the next runner tick, with no armed countdown
// and without the runner having won MarkStarted itself.
func TestRunner_RecoversStartedSessionStuckInLobby(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()
	sessionID := h.sessionID(t)

	// Reproduce the stuck state directly: stamp started_at (phase stays lobby),
	// the same row MarkStarted leaves behind, then skip Begin to mimic the
	// abandoned first-round transition.
	won, err := h.store.MarkStarted(ctx, sessionID)
	if err != nil {
		t.Fatalf("MarkStarted err = %v, want nil", err)
	}
	if !won {
		t.Fatal("MarkStarted won = false, want true (a fresh lobby starts)")
	}
	stuck := h.reload(t)
	if got, want := stuck.Phase, PhaseLobby; got != want {
		t.Fatalf("phase after MarkStarted = %q, want %q (stuck in lobby)", got, want)
	}
	if stuck.StartedAt == nil {
		t.Fatal("StartedAt after MarkStarted = nil, want a timestamp")
	}

	// The next tick heals it into the first round's intro, independent of any
	// armed countdown.
	h.tick(ctx)
	if got, want := h.phase(t), PhaseRoundIntro; got != want {
		t.Fatalf("phase after recovery tick = %q, want %q", got, want)
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

// TestRunner_AnswerCountsPlayerActiveForEarlyClose pins the answer-as-liveness
// rule (#712): a roster player whose last_seen_at is backdated before the active
// window - so the heartbeat alone would have them counted dropped - is counted
// active once they answer, because recording the pick bumps their last_seen_at
// to the answer's timestamp. With the only other player gone, that single
// answering player is the whole active roster, so the all-answered early-close
// fires on the next tick rather than stalling until the window times out.
func TestRunner_AnswerCountsPlayerActiveForEarlyClose(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()
	answerer, gone := h.players[0], h.players[1]
	sessionID := h.sessionID(t)

	if err := h.service.Start(ctx, h.code, 1); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}
	h.clock.advance(runnerCfg.RoundIntroBeat)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseQuestion; got != want {
		t.Fatalf("phase after intro beat = %q, want %q", got, want)
	}

	// The other player has left, so the answerer is the entire roster the active
	// counts can see. The answerer's heartbeat stopped long before the active
	// window, so without the answer-as-liveness bump they would be counted
	// dropped and the early-close would never fire.
	if err := h.service.Leave(ctx, h.code, gone); err != nil {
		t.Fatalf("Leave err = %v, want nil", err)
	}
	h.setLastSeen(t, sessionID, answerer, start.Add(-time.Hour))

	// The answerer picks well within the window. Recording the pick must also
	// stamp their last_seen_at to the answer time, dragging them back inside the
	// active window.
	optRight := correctOptionID(ctx, t, h.service, h.code, answerer)
	answerAt := h.clock.Now().Add(2 * time.Second)
	h.clock.advance(2 * time.Second)
	if err := h.service.SubmitAnswer(ctx, h.code, answerer, optRight, answerAt); err != nil {
		t.Fatalf("SubmitAnswer err = %v, want nil", err)
	}

	// A tick now must early-close: the lone active player has answered, so the
	// phase advances to reveal even though the window has not expired.
	h.tick(ctx)
	if got, want := h.phase(t), PhaseReveal; got != want {
		t.Fatalf("phase after answerer picked = %q, want %q (answer made them active, early close)", got, want)
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

// TestRunner_ClosesIdleSession pins the idle-close sweep (#836): a session whose
// host has gone past the idle timeout AND has no active players is finished by a
// runner tick, so a room nobody is using does not linger live forever.
func TestRunner_ClosesIdleSession(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()
	sessionID := h.sessionID(t)

	// Host starts the game, then everyone leaves: the host beat is well past the
	// idle cutoff and every player has gone stale.
	if err := h.service.Start(ctx, h.code, 1); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}
	if got, want := h.phase(t), PhaseRoundIntro; got != want {
		t.Fatalf("phase after Start = %q, want %q", got, want)
	}
	h.setHostLastSeen(t, sessionID, h.clock.Now().Add(-idleCloseTimeout-time.Minute))
	h.staleAllPlayers(t, sessionID)

	h.tick(ctx)
	final := h.reload(t)
	if got, want := final.Phase, PhaseFinished; got != want {
		t.Fatalf("phase after idle-close sweep = %q, want %q", got, want)
	}
	if final.FinishedAt == nil {
		t.Error("idle-closed session has nil FinishedAt")
	}
}

// TestRunner_DoesNotCloseWithPlayersPresent pins the core session-first rule
// (#836): a room whose host has gone past the idle timeout is NOT closed while a
// player is still active. Browsing away does not end a room that people are in.
func TestRunner_DoesNotCloseWithPlayersPresent(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()
	sessionID := h.sessionID(t)

	if err := h.service.Start(ctx, h.code, 1); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}
	// Host gone well past the idle cutoff, but a player is freshly seen (active),
	// so the room is in use and must stay open.
	h.setHostLastSeen(t, sessionID, h.clock.Now().Add(-idleCloseTimeout-time.Minute))
	h.setLastSeen(t, sessionID, h.players[0], h.clock.Now())

	h.tick(ctx)
	if got, want := h.phase(t), PhaseRoundIntro; got != want {
		t.Errorf("phase with a player present = %q, want %q (not closed)", got, want)
	}
}

// TestRunner_DoesNotCloseWithFreshHostBeat pins that a room whose host beat
// recently is left running by the idle sweep even with no players.
func TestRunner_DoesNotCloseWithFreshHostBeat(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()
	sessionID := h.sessionID(t)

	if err := h.service.Start(ctx, h.code, 1); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}
	// The host beat just now, well inside the idle window, even though every
	// player has gone stale.
	h.setHostLastSeen(t, sessionID, h.clock.Now())
	h.staleAllPlayers(t, sessionID)

	h.tick(ctx)
	if got, want := h.phase(t), PhaseRoundIntro; got != want {
		t.Errorf("phase with fresh host beat = %q, want %q (not closed)", got, want)
	}
}

// TestRunner_ClosesIdleEmptyLobby pins that the idle sweep also closes an empty
// staging lobby (#836): a room opened up front, never started, whose host has
// gone past the idle timeout with no players present is swept like any other idle
// room rather than pinned open forever.
func TestRunner_ClosesIdleEmptyLobby(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()
	sessionID := h.sessionID(t)

	// Never started: a lobby with a long-stale host presence and every player
	// gone stale is genuinely idle and must be swept.
	h.setHostLastSeen(t, sessionID, h.clock.Now().Add(-idleCloseTimeout-time.Hour))
	h.staleAllPlayers(t, sessionID)

	h.tick(ctx)
	if got, want := h.phase(t), PhaseFinished; got != want {
		t.Errorf("empty lobby phase after idle sweep = %q, want %q (idle lobby closed)", got, want)
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
	state, err := service.GetSessionState(ctx, code, viewer)
	if err != nil {
		t.Fatalf("GetSessionState err = %v, want nil", err)
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
	state, err := service.GetSessionState(ctx, code, players[0])
	if err != nil {
		t.Fatalf("GetSessionState err = %v, want nil", err)
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
	state, err := service.GetSessionState(ctx, code, players[0])
	if err != nil {
		t.Fatalf("GetSessionState err = %v, want nil", err)
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

// playSingleQuestionGame drives the room's current single-round, single-question
// game from the lobby to its end-of-game intermission, with the given player
// answering the correct option (an empty answerers slice runs a no-answer
// timeout game). It assumes the harness's quiz is a single round of one
// question. Returns the final standings read at intermission. Uses t.Context()
// throughout (like the harness's other helpers) so it shares one context.
func (h *runnerHarness) playSingleQuestionGame(t *testing.T, answerers []int64) []*Standing {
	t.Helper()
	ctx := t.Context()

	// Round intro -> the single question.
	h.clock.advance(runnerCfg.RoundIntroBeat)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseQuestion; got != want {
		t.Fatalf("phase after intro beat = %q, want %q", got, want)
	}

	if len(answerers) > 0 {
		optRight := correctOptionID(ctx, t, h.service, h.code, answerers[0])
		answerAt := h.clock.Now().Add(2 * time.Second)
		h.clock.advance(2 * time.Second)
		for _, pid := range answerers {
			if err := h.service.SubmitAnswer(ctx, h.code, pid, optRight, answerAt); err != nil {
				t.Fatalf("SubmitAnswer err = %v, want nil", err)
			}
		}
	}

	// Timeout-close the question, then the reveal beat ends the game.
	h.clock.advance(11 * time.Second)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseReveal; got != want {
		t.Fatalf("phase after question = %q, want %q", got, want)
	}
	h.clock.advance(runnerCfg.RevealBeat)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseIntermission; got != want {
		t.Fatalf("phase after final reveal = %q, want %q", got, want)
	}

	// Read the final standings off the intermission state.
	state, err := h.service.GetSessionState(ctx, h.code, h.players[0])
	if err != nil {
		t.Fatalf("GetSessionState err = %v, want nil", err)
	}

	return state.Standings
}

// TestRunner_RearmRunsNextGameWithResetScores drives a room through game 1 to
// intermission, asserts the room is still alive (not terminal), then the host
// re-arms onto a SECOND quiz and game 2 runs with its own scoring. Per-game
// reset is the headline: game 2's standings reflect only game 2's answers, never
// game 1's (#836).
func TestRunner_RearmRunsNextGameWithResetScores(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()
	scorer, slacker := h.players[0], h.players[1]

	// A second live quiz the host will arm for game 2.
	quizStore := store.NewQuizStore(h.db, slog.New(slog.DiscardHandler))
	game2 := seedRunnerQuizSlug(t, quizStore, "runner-quiz-g2", [][]bool{{true}})

	// Game 1: only the scorer answers, correctly.
	if err := h.service.Start(ctx, h.code, 1); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}
	g1Standings := h.playSingleQuestionGame(t, []int64{scorer})
	g1Scorer := findRunnerStanding(t, g1Standings, scorer)
	if g1Scorer.TotalScore <= 0 {
		t.Fatalf("game 1 scorer TotalScore = %d, want > 0", g1Scorer.TotalScore)
	}

	// The room is alive at intermission, NOT terminally finished, and it kept its
	// hub version so SSE keeps working into game 2.
	if got, want := h.phase(t), PhaseIntermission; got != want {
		t.Fatalf("phase after game 1 = %q, want %q (room alive)", got, want)
	}
	if got, want := ExportHubHasVersion(h.hub, h.code), true; got != want {
		t.Errorf("has version at intermission = %v, want %v (room alive)", got, want)
	}

	// The host re-arms onto game 2 (a different quiz): the room bumps game_seq and
	// the runner drives straight into game 2's first round.
	const hostID int64 = 1
	if err := h.service.StartQuiz(ctx, h.code, hostID, game2.ID, false); err != nil {
		t.Fatalf("StartQuiz err = %v, want nil", err)
	}
	reArmed := h.reload(t)
	if reArmed.QuizID == nil {
		t.Fatalf("re-armed QuizID = nil, want %d (new quiz)", game2.ID)
	}
	if got, want := *reArmed.QuizID, game2.ID; got != want {
		t.Errorf("re-armed QuizID = %d, want %d (new quiz)", got, want)
	}
	if got, want := reArmed.GameSeq, int64(2); got != want {
		t.Errorf("re-armed GameSeq = %d, want %d", got, want)
	}
	if got, want := reArmed.Phase, PhaseRoundIntro; got != want {
		t.Fatalf("phase after re-arm = %q, want %q (game 2 driving)", got, want)
	}

	// Game 2: this time the slacker answers and the scorer does not, so the
	// game-2 standings must invert game 1 - proof the scores reset per game.
	g2Standings := h.playSingleQuestionGame(t, []int64{slacker})
	g2Scorer := findRunnerStanding(t, g2Standings, scorer)
	g2Slacker := findRunnerStanding(t, g2Standings, slacker)
	if got, want := g2Scorer.TotalScore, 0; got != want {
		t.Errorf("game 2 scorer TotalScore = %d, want %d (no game-1 carryover)", got, want)
	}
	if g2Slacker.TotalScore <= 0 {
		t.Errorf("game 2 slacker TotalScore = %d, want > 0 (answered in game 2)", g2Slacker.TotalScore)
	}
}

// TestService_StartHosting_IntermissionArmsAndWaits pins StartHosting from the
// between-games intermission (#875): after game 1 ends, the host picking a SECOND
// quiz arms it and the room returns to the lobby WAITING - it must NOT auto-start
// into game 2's first question. The host then presses Start, and only then does
// game 2 run. This is the gap the bug shipped through: the intermission pick used
// to arm-and-start, dropping still-joined players straight into round 1.
func TestService_StartHosting_IntermissionArmsAndWaits(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()

	const hostID int64 = 1
	game2 := seedRunnerQuizSlug(t, store.NewQuizStore(h.db, slog.New(slog.DiscardHandler)),
		"start-hosting-intermission-g2", [][]bool{{true}})

	// Play game 1 to its end-of-game intermission.
	if err := h.service.Start(ctx, h.code, hostID); err != nil {
		t.Fatalf("Start (game 1) err = %v, want nil", err)
	}
	h.playSingleQuestionGame(t, []int64{h.players[0]})
	if got, want := h.phase(t), PhaseIntermission; got != want {
		t.Fatalf("phase after game 1 = %q, want %q", got, want)
	}

	// The host picks a second quiz from the intermission. StartHosting must ARM it
	// and stay in the lobby, not start it.
	if _, err := h.service.StartHosting(ctx, game2.ID, hostID, false); err != nil {
		t.Fatalf("StartHosting (from intermission) err = %v, want nil", err)
	}

	armed := h.reload(t)
	if armed.QuizID == nil {
		t.Fatalf("armed QuizID = nil, want %d (game 2)", game2.ID)
	}
	if got, want := *armed.QuizID, game2.ID; got != want {
		t.Errorf("armed QuizID = %d, want %d (game 2)", got, want)
	}
	if got, want := armed.Phase, PhaseLobby; got != want {
		t.Errorf("armed Phase = %q, want %q (armed but waiting in the lobby, not auto-started)", got, want)
	}
	if armed.StartedAt != nil {
		t.Errorf("armed StartedAt = %v, want nil (must not auto-start the picked quiz)", armed.StartedAt)
	}

	// A defensive tick must not advance the waiting lobby into a question on its
	// own: the game waits for the host.
	h.tick(ctx)
	if got, want := h.phase(t), PhaseLobby; got != want {
		t.Fatalf("phase after a tick = %q, want %q (still waiting on the host)", got, want)
	}

	// The host presses Start; only now does game 2 run, reaching its first question.
	if err := h.service.Start(ctx, h.code, hostID); err != nil {
		t.Fatalf("Start (game 2) err = %v, want nil", err)
	}
	if got, want := h.phase(t), PhaseRoundIntro; got != want {
		t.Fatalf("phase after host Start = %q, want %q (game 2 driving)", got, want)
	}
	h.clock.advance(runnerCfg.RoundIntroBeat)
	h.tick(ctx)
	q := h.reload(t)
	if got, want := q.Phase, PhaseQuestion; got != want {
		t.Fatalf("phase after intro beat = %q, want %q (game 2's first question)", got, want)
	}
	if q.CurrentQuestionID == nil {
		t.Error("game 2 first question CurrentQuestionID = nil, want it set")
	}
	if got, want := q.GameSeq, int64(2); got != want {
		t.Errorf("GameSeq for game 2 = %d, want %d", got, want)
	}
}

// TestService_StartHosting_RearmBeforeStartFromIntermission pins re-arming
// before start from the between-games intermission (#877): after game 1 ends, the
// host picks quiz B, then changes their mind to C - both before pressing Start.
// The room must end up armed on the LAST pick (C), waiting in the lobby and not
// started, with the roster carried across. game_seq bumps once (the first
// intermission re-arm) and then holds across the second lobby re-arm, so it does
// not skip a number per change of mind.
func TestService_StartHosting_RearmBeforeStartFromIntermission(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()

	const hostID int64 = 1
	quizStore := store.NewQuizStore(h.db, slog.New(slog.DiscardHandler))
	quizB := seedRunnerQuizSlug(t, quizStore, "rearm-intermission-b", [][]bool{{true}})
	quizC := seedRunnerQuizSlug(t, quizStore, "rearm-intermission-c", [][]bool{{true}})

	wantRoster := slices.Clone(h.players)
	slices.Sort(wantRoster)

	// Play game 1 to its end-of-game intermission.
	if err := h.service.Start(ctx, h.code, hostID); err != nil {
		t.Fatalf("Start (game 1) err = %v, want nil", err)
	}
	h.playSingleQuestionGame(t, []int64{h.players[0]})
	if got, want := h.phase(t), PhaseIntermission; got != want {
		t.Fatalf("phase after game 1 = %q, want %q", got, want)
	}

	// The host picks B from the intermission, then changes to C - both before Start.
	for _, qz := range []*quiz.Quiz{quizB, quizC} {
		if _, err := h.service.StartHosting(ctx, qz.ID, hostID, false); err != nil {
			t.Fatalf("StartHosting (arm %s) err = %v, want nil", qz.Slug, err)
		}
	}

	armed := h.reload(t)
	// Armed on the LAST pick (C), not B: re-arm reflects the latest quiz id.
	if armed.QuizID == nil {
		t.Fatalf("armed QuizID = nil, want %d (last pick C)", quizC.ID)
	}
	if got, want := *armed.QuizID, quizC.ID; got != want {
		t.Errorf("armed QuizID = %d, want %d (last pick C, not B)", got, want)
	}
	// Waiting in the lobby, not started: changing the pick never auto-starts.
	if got, want := armed.Phase, PhaseLobby; got != want {
		t.Errorf("armed Phase = %q, want %q (armed but waiting in the lobby)", got, want)
	}
	if armed.StartedAt != nil {
		t.Errorf("armed StartedAt = %v, want nil (re-arming must not start the game)", armed.StartedAt)
	}
	// game_seq bumped once at the first intermission re-arm and then held across
	// the second lobby re-arm, so two picks land on game 2, not game 3.
	if got, want := armed.GameSeq, int64(2); got != want {
		t.Errorf("armed GameSeq = %d, want %d (one bump at intermission, lobby re-arm holds)", got, want)
	}
	// The roster carried across both re-arms: nobody was forced to re-join.
	if got := h.rosterIDs(t); !slices.Equal(got, wantRoster) {
		t.Errorf("roster after re-arms = %v, want %v (roster intact)", got, wantRoster)
	}

	// A defensive tick must not advance the waiting lobby on its own.
	h.tick(ctx)
	if got, want := h.phase(t), PhaseLobby; got != want {
		t.Fatalf("phase after a tick = %q, want %q (still waiting on the host)", got, want)
	}

	// The host presses Start; only now does the last-picked game (C) run.
	if err := h.service.Start(ctx, h.code, hostID); err != nil {
		t.Fatalf("Start (game 2) err = %v, want nil", err)
	}
	if got, want := h.phase(t), PhaseRoundIntro; got != want {
		t.Fatalf("phase after host Start = %q, want %q (game 2 driving)", got, want)
	}
}

// TestRunner_ThreeGamesBackToBack pins the multi-quiz marathon (#877): three
// distinct quizzes run back-to-back in one room. Per game it asserts the roster
// carries across (players are never forced to re-join), live scores are
// per-session and scoped to the game (a player who scored in game A starts game B
// at zero), standings render for each game, and no per-game runner state bleeds
// across (current question/round cleared between games). A different player
// scores each game so the standings cannot accidentally pass by carrying a stale
// total.
func TestRunner_ThreeGamesBackToBack(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()
	alice, bob := h.players[0], h.players[1]

	const hostID int64 = 1
	quizStore := store.NewQuizStore(h.db, slog.New(slog.DiscardHandler))
	gameB := seedRunnerQuizSlug(t, quizStore, "marathon-b", [][]bool{{true}})
	gameC := seedRunnerQuizSlug(t, quizStore, "marathon-c", [][]bool{{true}})

	wantRoster := slices.Clone(h.players)
	slices.Sort(wantRoster)

	// Game A: alice scores, bob does not.
	if err := h.service.Start(ctx, h.code, hostID); err != nil {
		t.Fatalf("Start (game A) err = %v, want nil", err)
	}
	if got, want := h.reload(t).GameSeq, int64(1); got != want {
		t.Fatalf("game A GameSeq = %d, want %d", got, want)
	}
	aStandings := h.playSingleQuestionGame(t, []int64{alice})
	assertWinnerScoredLoserZero(t, aStandings, alice, bob, "A")
	if got := h.rosterIDs(t); !slices.Equal(got, wantRoster) {
		t.Errorf("game A roster = %v, want %v (roster intact)", got, wantRoster)
	}

	// Arm + start game B (a different quiz). armNextGame pins that the re-arm
	// cleared every per-game runner column before Start, so the room enters game B
	// with no stale question/round.
	armNextGame(t, h, hostID, gameB.ID)
	if got, want := h.reload(t).GameSeq, int64(2); got != want {
		t.Errorf("game B GameSeq = %d, want %d", got, want)
	}

	// Game B: bob scores, alice does not. alice must start B at zero (per-game
	// scope), inverting game A - proof game A's points did not carry over.
	bStandings := h.playSingleQuestionGame(t, []int64{bob})
	assertWinnerScoredLoserZero(t, bStandings, bob, alice, "B")
	if got := h.rosterIDs(t); !slices.Equal(got, wantRoster) {
		t.Errorf("game B roster = %v, want %v (roster intact)", got, wantRoster)
	}

	// Arm + start game C (a third quiz).
	armNextGame(t, h, hostID, gameC.ID)
	if got, want := h.reload(t).GameSeq, int64(3); got != want {
		t.Errorf("game C GameSeq = %d, want %d", got, want)
	}

	// Game C: alice scores again; her game-C total reflects only game C, never
	// the sum of games A and C.
	cStandings := h.playSingleQuestionGame(t, []int64{alice})
	assertWinnerScoredLoserZero(t, cStandings, alice, bob, "C")
	if got := h.rosterIDs(t); !slices.Equal(got, wantRoster) {
		t.Errorf("game C roster = %v, want %v (roster intact)", got, wantRoster)
	}

	// alice scored the same single correct answer in games A and C; the per-game
	// scope means her game-C total equals her game-A total, not double it.
	if got, want := findRunnerStanding(t, cStandings, alice).TotalScore,
		findRunnerStanding(t, aStandings, alice).TotalScore; got != want {
		t.Errorf("game C alice TotalScore = %d, want %d (per-game scope, no A+C sum)", got, want)
	}
}

// armNextGame arms quizID from the between-games intermission and presses Start,
// driving the room into the next game's first round_intro. It pins the
// arm-then-start handoff the marathon test repeats per game, and that the re-arm
// cleared the previous game's per-game runner state (current round/question)
// before the new game starts.
func armNextGame(t *testing.T, h *runnerHarness, hostID, quizID int64) {
	t.Helper()
	ctx := t.Context()
	if got, want := h.phase(t), PhaseIntermission; got != want {
		t.Fatalf("phase before arming next game = %q, want %q", got, want)
	}
	if _, err := h.service.StartHosting(ctx, quizID, hostID, false); err != nil {
		t.Fatalf("StartHosting (arm next game) err = %v, want nil", err)
	}
	armed := h.reload(t)
	if got, want := armed.Phase, PhaseLobby; got != want {
		t.Fatalf("phase after arm = %q, want %q (armed, waiting on Start)", got, want)
	}
	// The re-arm cleared the finished game's per-game runner columns, so the next
	// game starts with no stale question/round bleeding in.
	if armed.CurrentQuestionID != nil {
		t.Errorf("armed CurrentQuestionID = %v, want nil (cleared between games)", *armed.CurrentQuestionID)
	}
	if armed.CurrentRoundID != nil {
		t.Errorf("armed CurrentRoundID = %v, want nil (cleared between games)", *armed.CurrentRoundID)
	}
	if err := h.service.Start(ctx, h.code, hostID); err != nil {
		t.Fatalf("Start (next game) err = %v, want nil", err)
	}
	if got, want := h.phase(t), PhaseRoundIntro; got != want {
		t.Fatalf("phase after Start = %q, want %q (next game driving)", got, want)
	}
}

// assertWinnerScoredLoserZero pins a single-question game's standings: the winner
// has a positive total and the loser zero, with both present so the standings
// render for the whole roster. label names the game in failures.
func assertWinnerScoredLoserZero(t *testing.T, standings []*Standing, winner, loser int64, label string) {
	t.Helper()
	if got, want := len(standings), 2; got != want {
		t.Fatalf("game %s standings count = %d, want %d (both players ranked)", label, got, want)
	}
	if got := findRunnerStanding(t, standings, winner).TotalScore; got <= 0 {
		t.Errorf("game %s winner TotalScore = %d, want > 0", label, got)
	}
	if got, want := findRunnerStanding(t, standings, loser).TotalScore, 0; got != want {
		t.Errorf("game %s loser TotalScore = %d, want %d (no cross-game carryover)", label, got, want)
	}
}

// TestRunner_RearmSameQuizResetsScores pins that re-arming onto the SAME quiz
// still resets scores per game (#836): a player who scored in game 1 starts
// game 2 at zero, because the new game_seq scopes the standings to game 2's
// answers and the widened unique key lets the same picks be re-recorded.
func TestRunner_RearmSameQuizResetsScores(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()
	scorer := h.players[0]

	// Game 1: the scorer answers correctly and ends with points.
	if err := h.service.Start(ctx, h.code, 1); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}
	g1 := h.playSingleQuestionGame(t, []int64{scorer})
	if findRunnerStanding(t, g1, scorer).TotalScore <= 0 {
		t.Fatalf("game 1 scorer TotalScore = %d, want > 0", findRunnerStanding(t, g1, scorer).TotalScore)
	}

	// Re-arm onto the SAME quiz (the room's current quiz id).
	const hostID int64 = 1
	sameQuizID := h.reload(t).QuizID
	if sameQuizID == nil {
		t.Fatal("room QuizID = nil, want the game-1 quiz id")
	}
	if err := h.service.StartQuiz(ctx, h.code, hostID, *sameQuizID, false); err != nil {
		t.Fatalf("StartQuiz (same quiz) err = %v, want nil", err)
	}

	// Game 2 with nobody answering: the scorer's game-2 total is 0, so game 1's
	// points did not bleed across the re-run.
	g2 := h.playSingleQuestionGame(t, nil)
	if got, want := findRunnerStanding(t, g2, scorer).TotalScore, 0; got != want {
		t.Errorf("game 2 (same quiz) scorer TotalScore = %d, want %d (per-game reset)", got, want)
	}
}

// TestRunner_StartQuizRejectedMidGame pins that the host cannot arm a quiz while
// a game is in flight (#836): StartQuiz only works when no game is running (an
// empty lobby or the between-games intermission), so a mid-game call returns
// ErrGameInFlight and the running game is untouched.
func TestRunner_StartQuizRejectedMidGame(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()

	quizStore := store.NewQuizStore(h.db, slog.New(slog.DiscardHandler))
	game2 := seedRunnerQuizSlug(t, quizStore, "runner-quiz-midgame", [][]bool{{true}})

	// Start the game and advance into the question phase (mid-game).
	if err := h.service.Start(ctx, h.code, 1); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}
	h.clock.advance(runnerCfg.RoundIntroBeat)
	h.tick(ctx)
	if got, want := h.phase(t), PhaseQuestion; got != want {
		t.Fatalf("phase = %q, want %q (mid-game)", got, want)
	}

	const hostID int64 = 1
	err := h.service.StartQuiz(ctx, h.code, hostID, game2.ID, false)
	if got, want := err, ErrGameInFlight; !errors.Is(got, want) {
		t.Errorf("StartQuiz mid-game err = %v, want %v", got, want)
	}
	// The running game is untouched: still on the original quiz, game_seq 1.
	sess := h.reload(t)
	if got, want := sess.GameSeq, int64(1); got != want {
		t.Errorf("GameSeq after rejected re-arm = %d, want %d", got, want)
	}
	if sess.QuizID == nil {
		t.Fatal("QuizID after rejected re-arm = nil, want it unchanged from the original quiz")
	}
	if got, notWant := *sess.QuizID, game2.ID; got == notWant {
		t.Errorf("QuizID after rejected re-arm = %d, want it unchanged from the original quiz", got)
	}
}
