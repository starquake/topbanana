package livesession

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/starquake/topbanana/internal/quiz"
)

const (
	// defaultBeatInterval is how often the runner scans live sessions and
	// advances their phase. Small enough that a reveal beat or the armed
	// last-call deadline fires within a tick, large enough that the per-beat
	// scan over active rooms is cheap. Tests inject a fast clock and call the
	// tick directly, so they do not wait on this.
	defaultBeatInterval = 250 * time.Millisecond
	// defaultRoundIntroBeat is how long the round_intro screen shows before
	// the round's first question is issued.
	defaultRoundIntroBeat = 3 * time.Second
	// defaultRevealBeat is how long the revealed answer shows before the
	// runner advances to the next question (or round, or finish).
	defaultRevealBeat = 4 * time.Second
	// defaultRoundResultsBeat is how long the between-rounds standings screen
	// shows before the runner advances to the next round's intro (or finish).
	defaultRoundResultsBeat = 6 * time.Second
	// defaultQuestionWindow is the answer window for a question when neither
	// the question nor its quiz sets a time limit.
	defaultQuestionWindow = 10 * time.Second
	// defaultQuestionReadBeat is how long the question text shows before the
	// answer options open and the answer window starts. Mirrors the solo
	// game's reveal beat (#247) so every player gets the same read time before
	// the same full answer time. The app wires it from REVEAL_DELAY, the same
	// knob the solo game uses.
	defaultQuestionReadBeat = 3 * time.Second
)

// logSessionKey is the slog attribute key the runner logs the session id
// under on every warning.
const logSessionKey = "session"

// slog attribute keys shared by the domain-layer log lines, so every
// live-session log line names the same field the same way. logPhaseKey
// matches the literal "phase" the runner already logs under.
const (
	logJoinCodeKey = "joinCode"
	logPlayerKey   = "player"
	logHostKey     = "host"
	logQuizKey     = "quiz"
	logPhaseKey    = "phase"
	logQuestionKey = "question"
	logOptionKey   = "option"
	logReadyKey    = "ready"
	logDeadlineKey = "deadline"
	logReasonKey   = "reason"
)

// errNoQuiz guards the runner against driving a quiz-less room (#836). A room
// created without a quiz stays in the empty lobby until the host arms one, so the
// gameplay advance never runs without a quiz; this is the defensive error the
// plan load returns if it ever is reached without one, turning a nil deref into a
// skipped beat.
var errNoQuiz = errors.New("session has no quiz")

// Clock is the runner's view of time, injectable so tests drive transitions
// off a controlled clock instead of waiting on the wall clock.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

// Scorer is the slice of the scoring logic the runner reuses at question
// close: the exact CalculateScore curve, decoupled from game.Answer so the
// runner need not build one. Implemented by game.Service via a thin adapter
// in the app wiring.
type Scorer interface {
	ScoreAnswer(ctx context.Context, correct bool, startedAt, expiredAt, answeredAt time.Time) int
}

// RunnerConfig holds the runner's tunable beats and windows. Zero fields
// fall back to the package defaults, so a caller overrides only what it
// needs (the e2e suite shrinks the beats; tests inject tiny values).
type RunnerConfig struct {
	BeatInterval     time.Duration
	RoundIntroBeat   time.Duration
	RevealBeat       time.Duration
	RoundResultsBeat time.Duration
	// QuestionReadBeat is how long the question text shows before the answer
	// options open and the answer window starts.
	QuestionReadBeat time.Duration
	// IdleCloseTimeout is how long a room may sit with its host gone AND no
	// active players before the runner closes it as idle (#836). Zero falls back
	// to DefaultIdleCloseTimeout. The e2e/integration suites shrink it so an
	// idle-close spec does not pay the 30-minute production window.
	IdleCloseTimeout time.Duration
}

func (c RunnerConfig) withDefaults() RunnerConfig {
	if c.BeatInterval <= 0 {
		c.BeatInterval = defaultBeatInterval
	}
	if c.RoundIntroBeat <= 0 {
		c.RoundIntroBeat = defaultRoundIntroBeat
	}
	if c.RevealBeat <= 0 {
		c.RevealBeat = defaultRevealBeat
	}
	if c.RoundResultsBeat <= 0 {
		c.RoundResultsBeat = defaultRoundResultsBeat
	}
	if c.QuestionReadBeat <= 0 {
		c.QuestionReadBeat = defaultQuestionReadBeat
	}
	if c.IdleCloseTimeout <= 0 {
		c.IdleCloseTimeout = DefaultIdleCloseTimeout
	}

	return c
}

// Runner advances live sessions through their phases on a server clock. It is
// the single owner of the timed transitions (armed last-call start, round_intro
// -> question, close on timeout-or-all-answered, reveal -> next) and publishes
// a tick on every transition. One Runner per process (the deploy is
// single-instance); its in-memory bookkeeping is keyed by session id.
//
// The clock and beats are injectable so tests drive a session from start
// through several questions without sleeping: build the runner with a fake
// clock and tiny beats and call Tick directly.
type Runner struct {
	store     Store
	quizzes   QuizReader
	publisher Publisher
	scorer    Scorer
	logger    *slog.Logger
	clock     Clock
	cfg       RunnerConfig

	mu sync.Mutex
	// phaseSince records when the runner moved a session into its current
	// beat-gated phase (round_intro or reveal), so the beat is measured from
	// the transition rather than persisted.
	phaseSince map[string]time.Time
}

// NewRunner builds a runner over the live-session store, quiz reader, tick
// publisher, and scorer. cfg's zero fields fall back to the package defaults.
func NewRunner(
	store Store, quizzes QuizReader, publisher Publisher, scorer Scorer, logger *slog.Logger, cfg RunnerConfig,
) *Runner {
	return &Runner{
		store:      store,
		quizzes:    quizzes,
		publisher:  publisher,
		scorer:     scorer,
		logger:     logger,
		clock:      realClock{},
		cfg:        cfg.withDefaults(),
		phaseSince: make(map[string]time.Time),
	}
}

// SetClock overrides the runner's clock. Startup/test-wiring only, before
// Run starts; not safe for concurrent use with a running loop.
func (r *Runner) SetClock(c Clock) {
	r.clock = c
}

// Run drives the runner loop until ctx is cancelled (the signal-driven
// shutdown context, so a graceful shutdown stops the runner before the DB
// closes). It ticks every BeatInterval; a scan failure is logged and the
// loop continues so one bad beat does not stall the next.
func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.BeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.tick(ctx, r.clock.Now())
		}
	}
}

// Begin is the host "Start now" path: it drives the just-started session out
// of the lobby into its first round_intro at once, rather than waiting for the
// next beat to notice it. Start has already marked the session started. A
// session no longer in the lobby (a double Start, or the armed countdown having
// won) is a no-op.
func (r *Runner) Begin(ctx context.Context, sessionID string) {
	now := r.clock.Now()
	sess, err := r.store.GetSessionByID(ctx, sessionID)
	if err != nil {
		r.logger.WarnContext(ctx, "runner failed to load session for begin",
			slog.String(logSessionKey, sessionID), slog.Any("err", err))

		return
	}
	if sess.Phase != PhaseLobby {
		return
	}
	r.enterFirstRound(ctx, sess, now)
}

// Rearm is the host "start next quiz" path (#836): after the service has
// re-armed the room (pointed it at the new quiz, bumped game_seq, reset it to
// the lobby, and marked it started for the new game), this drops the room's
// stale per-game phase clock and drives it straight into the new game's first
// round, the same transition Begin runs for a fresh start. Safe to call more
// than once; a room not in the lobby (a double re-arm) is a no-op via Begin.
func (r *Runner) Rearm(ctx context.Context, sessionID string) {
	r.forget(sessionID)
	r.Begin(ctx, sessionID)
}

// tick scans every live session once and advances each. Exported to tests as
// Tick via export_test.
func (r *Runner) tick(ctx context.Context, now time.Time) {
	ids, err := r.store.ListLiveSessionIDs(ctx)
	if err != nil {
		r.logger.WarnContext(ctx, "runner failed to list live sessions", slog.Any("err", err))

		return
	}
	for _, id := range ids {
		r.advance(ctx, id, now)
	}
}

// advance loads one session and applies the single transition (if any) due at
// now. It is the whole state machine: each phase decides whether its beat or
// deadline has elapsed and, if so, moves to the next phase and publishes.
func (r *Runner) advance(ctx context.Context, sessionID string, now time.Time) {
	sess, err := r.store.GetSessionByID(ctx, sessionID)
	if err != nil {
		r.logger.WarnContext(
			ctx,
			"runner failed to load session",
			slog.String(logSessionKey, sessionID),
			slog.Any("err", err),
		)

		return
	}

	if r.closeIfIdle(ctx, sess, now) {
		return
	}

	switch sess.Phase {
	case PhaseLobby:
		r.advanceLobby(ctx, sess, now)
	case PhaseRoundIntro:
		r.advanceRoundIntro(ctx, sess, now)
	case PhaseQuestion:
		r.advanceQuestion(ctx, sess, now)
	case PhaseReveal:
		r.advanceReveal(ctx, sess, now)
	case PhaseRoundResults:
		r.advanceRoundResults(ctx, sess, now)
	case PhaseIntermission:
		// The room waits between games for the host to arm the next quiz; the
		// runner drives nothing here (closeIfIdle above is the only sweep).
	case PhaseFinished:
		r.forget(sess.ID)
	default:
		r.logger.WarnContext(ctx, "runner saw unknown session phase",
			slog.String(logSessionKey, sess.ID), slog.String("phase", string(sess.Phase)))
	}
}

// advanceLobby starts a lobby once its host-armed last-call countdown reaches
// its deadline (#735): a session whose start_at is set and at or before now
// leaves the lobby into its first round, the same transition host "Start now"
// drives. A lobby with no armed countdown (start_at nil) is a no-op - the host
// controls the start; the runner never auto-starts on its own.
//
// A session already marked started (started_at set) but still in the lobby is
// the abandoned-Begin state (#781): host "Start now" won MarkStarted, then the
// detached first-round transition failed before it could run. The runner heals
// it on the next tick by entering the first round directly, since the session
// is already started.
func (r *Runner) advanceLobby(ctx context.Context, sess *Session, now time.Time) {
	if sess.StartedAt != nil {
		r.enterFirstRound(ctx, sess, now)

		return
	}
	if sess.StartAt == nil || now.Before(*sess.StartAt) {
		return
	}

	won, err := r.store.MarkStarted(ctx, sess.ID)
	if err != nil {
		r.logger.WarnContext(
			ctx,
			"runner failed to start session at armed countdown",
			slog.String(logSessionKey, sess.ID),
			slog.Any("err", err),
		)

		return
	}
	if !won {
		return
	}
	r.enterFirstRound(ctx, sess, now)
}

// advanceRoundIntro issues the round's first question once the round_intro
// beat has elapsed.
func (r *Runner) advanceRoundIntro(ctx context.Context, sess *Session, now time.Time) {
	if now.Sub(r.phaseEnteredAt(sess.ID, now)) < r.cfg.RoundIntroBeat {
		return
	}

	plan, err := r.loadPlan(ctx, sess)
	if err != nil {
		return
	}
	first, ok := plan.firstQuestionOfRound(sess.CurrentRoundID)
	if !ok {
		// A round with no questions: skip straight to the next round or finish.
		r.advanceAfterRound(ctx, sess, plan, now)

		return
	}
	r.issueQuestion(ctx, sess, first, now)
}

// advanceQuestion closes the current question when every active player has
// answered (early close) or the answer window has expired (timeout close),
// scoring the picks and moving into the reveal phase.
func (r *Runner) advanceQuestion(ctx context.Context, sess *Session, now time.Time) {
	if sess.CurrentQuestionID == nil || sess.QuestionExpiresAt == nil {
		return
	}

	timedOut := !now.Before(*sess.QuestionExpiresAt)
	if !timedOut && !r.allActiveAnswered(ctx, sess, now) {
		return
	}

	r.scoreQuestion(ctx, sess)
	if err := r.store.EnterReveal(ctx, sess.ID); err != nil {
		r.logger.WarnContext(
			ctx,
			"runner failed to enter reveal",
			slog.String(logSessionKey, sess.ID),
			slog.Any("err", err),
		)

		return
	}
	r.markPhase(sess.ID, now)
	r.publish(sess.JoinCode, PhaseReveal)
}

// advanceReveal moves to the next question once the reveal beat has elapsed,
// or - when the revealed question was the last of its round - into the
// between-rounds round_results screen. The final round skips round_results and
// finishes directly, so the game ends on a single final-standings screen rather
// than showing "Scores so far" back-to-back with "Final scores".
func (r *Runner) advanceReveal(ctx context.Context, sess *Session, now time.Time) {
	if now.Sub(r.phaseEnteredAt(sess.ID, now)) < r.cfg.RevealBeat {
		return
	}

	plan, err := r.loadPlan(ctx, sess)
	if err != nil {
		return
	}
	next, ok := plan.nextQuestionInRound(sess.CurrentRoundID, sess.CurrentQuestionID)
	if ok {
		r.issueQuestion(ctx, sess, next, now)

		return
	}
	if _, hasNext := plan.nextRound(sess.CurrentRoundID); !hasNext {
		r.endGame(ctx, sess)

		return
	}
	r.enterRoundResults(ctx, sess, now)
}

// advanceRoundResults moves on from the between-rounds standings screen once
// the round_results beat has elapsed: into the next round's intro, or finish
// when the round just shown was the last.
func (r *Runner) advanceRoundResults(ctx context.Context, sess *Session, now time.Time) {
	if now.Sub(r.phaseEnteredAt(sess.ID, now)) < r.cfg.RoundResultsBeat {
		return
	}

	plan, err := r.loadPlan(ctx, sess)
	if err != nil {
		return
	}
	r.advanceAfterRound(ctx, sess, plan, now)
}

// advanceAfterRound moves into the next round's intro, or finishes the
// session when the current round was the last. Reached from round_results
// (the normal between-rounds path) and from a round that had no questions to
// run (which has no standings to show, so it skips round_results).
func (r *Runner) advanceAfterRound(ctx context.Context, sess *Session, plan questionPlan, now time.Time) {
	nextRound, ok := plan.nextRound(sess.CurrentRoundID)
	if !ok {
		r.endGame(ctx, sess)

		return
	}
	r.enterRoundIntro(ctx, sess, nextRound, now)
}

// enterFirstRound moves a freshly started lobby into its first round's intro.
func (r *Runner) enterFirstRound(ctx context.Context, sess *Session, now time.Time) {
	plan, err := r.loadPlan(ctx, sess)
	if err != nil {
		return
	}
	firstRound, ok := plan.firstRound()
	if !ok {
		// A quiz with no questions has nothing to run: end the game immediately
		// (into intermission), so the room stays alive for the host to re-arm.
		r.endGame(ctx, sess)

		return
	}
	r.enterRoundIntro(ctx, sess, firstRound, now)
}

// enterRoundIntro persists the round_intro transition and stamps the beat
// clock so the intro shows for the full RoundIntroBeat.
func (r *Runner) enterRoundIntro(ctx context.Context, sess *Session, roundID int64, now time.Time) {
	if err := r.store.EnterRoundIntro(ctx, sess.ID, roundID); err != nil {
		r.logger.WarnContext(
			ctx,
			"runner failed to enter round intro",
			slog.String(logSessionKey, sess.ID),
			slog.Any("err", err),
		)

		return
	}
	r.markPhase(sess.ID, now)
	r.publish(sess.JoinCode, PhaseRoundIntro)
}

// enterRoundResults persists the round_results transition (leaving
// current_round_id in place so the standings read knows which round just
// finished) and stamps the beat clock so the standings show for the full
// RoundResultsBeat.
func (r *Runner) enterRoundResults(ctx context.Context, sess *Session, now time.Time) {
	if err := r.store.EnterRoundResults(ctx, sess.ID); err != nil {
		r.logger.WarnContext(
			ctx,
			"runner failed to enter round results",
			slog.String(logSessionKey, sess.ID),
			slog.Any("err", err),
		)

		return
	}
	r.markPhase(sess.ID, now)
	r.publish(sess.JoinCode, PhaseRoundResults)
}

// issueQuestion persists the question transition with its server answer window
// and publishes. The answer window opens after the read beat, so the question
// text shows during [now, startedAt) before the options open: StartedAt is now
// + QuestionReadBeat and ExpiresAt is StartedAt + the resolved per-question
// time limit. This mirrors the solo game's reveal beat (#247) so every player
// gets the same read time and the same full answer time.
func (r *Runner) issueQuestion(ctx context.Context, sess *Session, q *quiz.Question, now time.Time) {
	startedAt := now.Add(r.cfg.QuestionReadBeat)
	// issueQuestion is only reached after loadPlan succeeded, so the room has a
	// quiz; questionWindow falls back to the default if QuizID is somehow unset.
	quizID := int64(0)
	if sess.QuizID != nil {
		quizID = *sess.QuizID
	}
	expires := startedAt.Add(r.questionWindow(ctx, quizID, q))
	if err := r.store.EnterQuestion(ctx, sess.ID, q.RoundID, q.ID, startedAt, expires); err != nil {
		r.logger.WarnContext(
			ctx,
			"runner failed to enter question",
			slog.String(logSessionKey, sess.ID),
			slog.Any("err", err),
		)

		return
	}
	r.markPhase(sess.ID, now)
	r.publish(sess.JoinCode, PhaseQuestion)
}

// closeIfIdle terminally closes a non-finished room that has gone genuinely idle
// (#836): its host has not beat its presence heartbeat for longer than the idle
// timeout AND no players are still active. Hosting is session-first - a host
// opens a room up front and may browse away for minutes - so a missing host
// heartbeat alone no longer closes the room; only a room nobody is using is swept
// (a room with players present, or a host who is just briefly away, stays open).
// Every phase except the terminal finished is in scope, including the empty
// lobby (an abandoned staging room with no players still ages out) and the
// between-games intermission. The host's effective last-seen is
// COALESCE(HostLastSeenAt, StartedAt, CreatedAt) so a room whose host never beat
// still ages from when it began (or was created, for a never-started lobby).
// Reports whether it closed, so the caller skips the normal phase advance for
// this beat.
func (r *Runner) closeIfIdle(ctx context.Context, sess *Session, now time.Time) bool {
	if sess.Phase == PhaseFinished {
		return false
	}
	lastSeen := r.hostLastSeen(sess)
	if !lastSeen.Before(now.Add(-r.cfg.IdleCloseTimeout)) {
		return false
	}

	// A room with anyone still present is in use, not idle: the host may have
	// browsed away but players are waiting, so it must not be closed under them.
	active, err := r.store.CountActive(ctx, sess.ID, now.Add(-ActiveWindow))
	if err != nil {
		r.logger.WarnContext(ctx, "runner failed to count active players for idle close",
			slog.String(logSessionKey, sess.ID), slog.Any("err", err))

		return false
	}
	if active > 0 {
		return false
	}

	r.logger.InfoContext(ctx, "closing idle session (host gone, no active players)",
		slog.String(logSessionKey, sess.ID), slog.Time("hostLastSeen", lastSeen))
	r.finishTerminal(ctx, sess)

	return true
}

// hostLastSeen is the room's effective host-presence timestamp for the idle
// sweep: the host heartbeat, falling back to when the game started, then to when
// the room was created (a never-started lobby has neither earlier stamp).
func (*Runner) hostLastSeen(sess *Session) time.Time {
	if sess.HostLastSeenAt != nil {
		return *sess.HostLastSeenAt
	}
	if sess.StartedAt != nil {
		return *sess.StartedAt
	}

	return sess.CreatedAt
}

// endGame ends a game without closing the room (#836): it persists the
// intermission transition and publishes, leaving the session alive in memory
// (its publisher version entry stays so SSE keeps working and the host can
// re-arm the next quiz). The phase clock is dropped because intermission is not
// beat-gated - the runner waits on the host, not a timer. Reached on the normal
// game-end paths (the last reveal of the last round, or a quiz with no
// questions).
func (r *Runner) endGame(ctx context.Context, sess *Session) {
	if err := r.store.Intermission(ctx, sess.ID); err != nil {
		r.logger.WarnContext(
			ctx,
			"runner failed to move session to intermission",
			slog.String(logSessionKey, sess.ID),
			slog.Any("err", err),
		)

		return
	}
	r.forget(sess.ID)
	r.publish(sess.JoinCode, PhaseIntermission)
}

// finishTerminal closes the room for good: it persists the finished transition,
// publishes, and drops the session's in-memory bookkeeping (its phase clock and,
// since the room is now terminal, its publisher version entry). Reached only
// when the room is actually closed - the idle auto-close swept it (host gone and
// no players present) or the host explicitly ended the session.
func (r *Runner) finishTerminal(ctx context.Context, sess *Session) {
	if err := r.store.Finish(ctx, sess.ID); err != nil {
		r.logger.WarnContext(
			ctx,
			"runner failed to finish session",
			slog.String(logSessionKey, sess.ID),
			slog.Any("err", err),
		)

		return
	}
	r.forget(sess.ID)
	r.publish(sess.JoinCode, PhaseFinished)
	// Evict the version entry only after the finished tick is published, so
	// that tick still carries the last real version.
	r.forgetPublished(sess.JoinCode)
}

// scoreQuestion computes and writes the score for every pick on the current
// question using the shared CalculateScore curve.
func (r *Runner) scoreQuestion(ctx context.Context, sess *Session) {
	if sess.CurrentQuestionID == nil || sess.QuestionStartedAt == nil || sess.QuestionExpiresAt == nil {
		return
	}
	answers, err := r.store.ListAnswers(ctx, sess.ID, *sess.CurrentQuestionID)
	if err != nil {
		r.logger.WarnContext(
			ctx,
			"runner failed to list answers for scoring",
			slog.String(logSessionKey, sess.ID),
			slog.Any("err", err),
		)

		return
	}
	for _, a := range answers {
		score := r.scorer.ScoreAnswer(ctx, a.Correct, *sess.QuestionStartedAt, *sess.QuestionExpiresAt, a.AnsweredAt)
		if err := r.store.SetAnswerScore(ctx, sess.ID, *sess.CurrentQuestionID, a.PlayerID, score); err != nil {
			r.logger.WarnContext(
				ctx,
				"runner failed to set answer score",
				slog.String(logSessionKey, sess.ID),
				slog.Any("err", err),
			)
		}
	}
}

// allActiveAnswered reports whether every active player has answered the
// current question, so the runner can early-close before the timeout. Active
// means last_seen_at within ActiveWindow of now (the heartbeat the held SSE
// connection beats), so a dropped player whose heartbeat has gone stale no
// longer holds the question open. An empty or all-stale roster never
// early-closes (it has no active answerer to wait on); the question times out
// instead. since is computed from the runner's injected clock so tests stay
// deterministic.
func (r *Runner) allActiveAnswered(ctx context.Context, sess *Session, now time.Time) bool {
	since := now.Add(-ActiveWindow)
	active, err := r.store.CountActive(ctx, sess.ID, since)
	if err != nil {
		r.logger.WarnContext(
			ctx,
			"runner failed to count active players",
			slog.String(logSessionKey, sess.ID),
			slog.Any("err", err),
		)

		return false
	}
	if active == 0 {
		return false
	}
	unanswered, err := r.store.CountActiveUnanswered(ctx, sess.ID, *sess.CurrentQuestionID, since)
	if err != nil {
		r.logger.WarnContext(
			ctx,
			"runner failed to count active unanswered players",
			slog.String(logSessionKey, sess.ID),
			slog.Any("err", err),
		)

		return false
	}

	return unanswered == 0
}

// questionWindow resolves the answer window for a question: the per-question
// override, then the quiz default, then the package default - the same
// priority chain the solo game uses (#99).
func (r *Runner) questionWindow(ctx context.Context, quizID int64, q *quiz.Question) time.Duration {
	if q.TimeLimitSeconds != nil && *q.TimeLimitSeconds > 0 {
		return time.Duration(*q.TimeLimitSeconds) * time.Second
	}
	qz, err := r.quizzes.GetQuiz(ctx, quizID)
	if err == nil && qz.TimeLimitSeconds > 0 {
		return time.Duration(qz.TimeLimitSeconds) * time.Second
	}

	return defaultQuestionWindow
}

// loadPlan loads the quiz and projects it into the runner's question plan. A
// quiz-less room (#836) never leaves the empty lobby into a game, so reaching
// here without a quiz is a defensive guard rather than a real path; it returns
// an error so the caller's advance is a no-op rather than a nil deref.
func (r *Runner) loadPlan(ctx context.Context, sess *Session) (questionPlan, error) {
	if sess.QuizID == nil {
		return questionPlan{}, errNoQuiz
	}
	qz, err := r.quizzes.GetQuiz(ctx, *sess.QuizID)
	if err != nil {
		r.logger.WarnContext(
			ctx, "runner failed to load quiz", slog.String(logSessionKey, sess.ID), slog.Any("err", err),
		)

		return questionPlan{}, fmt.Errorf("failed to load quiz for runner: %w", err)
	}

	return newQuestionPlan(qz), nil
}

func (r *Runner) publish(code string, phase Phase) {
	if r.publisher == nil {
		return
	}
	r.publisher.Publish(code, phase)
}

// forgetPublished releases the publisher's version bookkeeping for a terminal
// session. Same nil-publisher guard as publish: tests that drive the runner
// without a publisher skip it.
func (r *Runner) forgetPublished(code string) {
	if r.publisher == nil {
		return
	}
	r.publisher.Forget(code)
}

// markPhase stamps when the session entered its current beat-gated phase.
func (r *Runner) markPhase(sessionID string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phaseSince[sessionID] = now
}

// phaseEnteredAt returns when the session entered its current phase, seeding
// it with now on a cold start (e.g. a process restart mid-phase) so the beat
// is measured from first observation rather than firing instantly.
func (r *Runner) phaseEnteredAt(sessionID string, now time.Time) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.phaseSince[sessionID]
	if !ok {
		r.phaseSince[sessionID] = now
		t = now
	}

	return t
}

func (r *Runner) forget(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.phaseSince, sessionID)
}

// questionPlan is the runner's flattened view of a quiz: its questions grouped
// by round, with rounds and questions in play order. It answers the four
// navigation questions the state machine asks (first round, first question of
// a round, next question in a round, next round) without re-deriving ordering
// at each call site.
type questionPlan struct {
	rounds         []int64
	questionsByRnd map[int64][]*quiz.Question
}

// newQuestionPlan projects a loaded quiz into a questionPlan. GetQuiz returns
// questions in quiz-wide play order already (across rounds), so grouping by
// round id while preserving that order yields per-round play order, and the
// first time each round id is seen fixes round play order.
//
// The plan is derived from questions, so a round with no questions never
// appears and its intro is never shown in a live session. This is intentional
// (#803): a live round with nothing to ask would be a dead beat, unlike the
// solo path which can show an empty round's intro.
func newQuestionPlan(qz *quiz.Quiz) questionPlan {
	plan := questionPlan{questionsByRnd: make(map[int64][]*quiz.Question)}
	seen := make(map[int64]struct{})
	questions := append([]*quiz.Question(nil), qz.Questions...)
	slices.SortStableFunc(questions, func(a, b *quiz.Question) int {
		return a.Position - b.Position
	})
	for _, q := range questions {
		if _, ok := seen[q.RoundID]; !ok {
			seen[q.RoundID] = struct{}{}
			plan.rounds = append(plan.rounds, q.RoundID)
		}
		plan.questionsByRnd[q.RoundID] = append(plan.questionsByRnd[q.RoundID], q)
	}

	return plan
}

func (p questionPlan) firstRound() (int64, bool) {
	if len(p.rounds) == 0 {
		return 0, false
	}

	return p.rounds[0], true
}

func (p questionPlan) firstQuestionOfRound(roundID *int64) (*quiz.Question, bool) {
	if roundID == nil {
		return nil, false
	}
	qs := p.questionsByRnd[*roundID]
	if len(qs) == 0 {
		return nil, false
	}

	return qs[0], true
}

func (p questionPlan) nextQuestionInRound(roundID, questionID *int64) (*quiz.Question, bool) {
	if roundID == nil || questionID == nil {
		return nil, false
	}
	qs := p.questionsByRnd[*roundID]
	for i, q := range qs {
		if q.ID == *questionID && i+1 < len(qs) {
			return qs[i+1], true
		}
	}

	return nil, false
}

func (p questionPlan) nextRound(roundID *int64) (int64, bool) {
	if roundID == nil {
		return 0, false
	}
	for i, r := range p.rounds {
		if r == *roundID && i+1 < len(p.rounds) {
			return p.rounds[i+1], true
		}
	}

	return 0, false
}
