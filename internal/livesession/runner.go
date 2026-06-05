package livesession

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/starquake/topbanana/internal/quiz"
)

const (
	// defaultBeatInterval is how often the runner scans live sessions and
	// advances their phase. Small enough that a reveal beat or auto-start
	// window fires within a tick of its deadline, large enough that the
	// per-beat scan over active rooms is cheap. Tests inject a fast clock
	// and call the tick directly, so they do not wait on this.
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
	// defaultAutoStartWindow is how long every joined player must have been
	// ready before the runner auto-starts the game. The host can override
	// this at any time via Start.
	defaultAutoStartWindow = 5 * time.Second
	// defaultQuestionWindow is the answer window for a question when neither
	// the question nor its quiz sets a time limit.
	defaultQuestionWindow = 10 * time.Second
)

// logSessionKey is the slog attribute key the runner logs the session id
// under on every warning.
const logSessionKey = "session"

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
	AutoStartWindow  time.Duration
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
	if c.AutoStartWindow <= 0 {
		c.AutoStartWindow = defaultAutoStartWindow
	}

	return c
}

// Runner advances live sessions through their phases on a server clock. It is
// the single owner of the timed transitions (auto-start, round_intro ->
// question, close on timeout-or-all-answered, reveal -> next) and publishes a
// tick on every transition. One Runner per process (the deploy is
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
	// allReadySince records when every joined player in a lobby became ready,
	// so the auto-start window is measured from that moment and reset the
	// instant someone un-readies or a not-ready player joins.
	allReadySince map[string]time.Time
}

// NewRunner builds a runner over the live-session store, quiz reader, tick
// publisher, and scorer. cfg's zero fields fall back to the package defaults.
func NewRunner(
	store Store, quizzes QuizReader, publisher Publisher, scorer Scorer, logger *slog.Logger, cfg RunnerConfig,
) *Runner {
	return &Runner{
		store:         store,
		quizzes:       quizzes,
		publisher:     publisher,
		scorer:        scorer,
		logger:        logger,
		clock:         realClock{},
		cfg:           cfg.withDefaults(),
		phaseSince:    make(map[string]time.Time),
		allReadySince: make(map[string]time.Time),
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

// Begin is the host-Start override: it drives the just-started session out of
// the lobby into its first round_intro at once, rather than waiting for the
// next beat to notice it. Start has already marked the session started, so
// Begin skips the auto-start ready window entirely. A session no longer in
// the lobby (a double Start, or the auto-start having won) is a no-op.
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
	case PhaseFinished:
		r.forget(sess.ID)
	default:
		r.logger.WarnContext(ctx, "runner saw unknown session phase",
			slog.String(logSessionKey, sess.ID), slog.String("phase", string(sess.Phase)))
	}
}

// advanceLobby auto-starts a lobby once every joined player has been ready for
// the auto-start window. The window resets the moment the roster stops being
// all-ready (someone un-readies, or a not-ready player joins). An empty lobby
// never auto-starts.
func (r *Runner) advanceLobby(ctx context.Context, sess *Session, now time.Time) {
	if !allPlayersReady(sess) {
		r.clearReadySince(sess.ID)

		return
	}
	if now.Sub(r.readySince(sess.ID, now)) < r.cfg.AutoStartWindow {
		return
	}

	won, err := r.store.MarkStarted(ctx, sess.ID)
	if err != nil {
		r.logger.WarnContext(
			ctx,
			"runner failed to auto-start session",
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
	if !timedOut && !r.allActiveAnswered(ctx, sess) {
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
// between-rounds round_results screen.
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
		r.finish(ctx, sess)

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
		// A quiz with no questions has nothing to run: finish immediately.
		r.finish(ctx, sess)

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
// and publishes. The window runs from now to now + the resolved per-question
// time limit.
func (r *Runner) issueQuestion(ctx context.Context, sess *Session, q *quiz.Question, now time.Time) {
	expires := now.Add(r.questionWindow(ctx, sess.QuizID, q))
	if err := r.store.EnterQuestion(ctx, sess.ID, q.RoundID, q.ID, now, expires); err != nil {
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

// finish persists the finished transition, publishes, and drops the session's
// in-memory bookkeeping.
func (r *Runner) finish(ctx context.Context, sess *Session) {
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
// current question. Active is defined minimally for MP-5 as every roster
// player (the full heartbeat lifecycle is MP-10). An empty roster never
// early-closes, so the question times out instead of closing instantly.
func (r *Runner) allActiveAnswered(ctx context.Context, sess *Session) bool {
	active := len(sess.Players)
	if active == 0 {
		return false
	}
	count, err := r.store.CountAnswers(ctx, sess.ID, *sess.CurrentQuestionID)
	if err != nil {
		r.logger.WarnContext(
			ctx,
			"runner failed to count answers",
			slog.String(logSessionKey, sess.ID),
			slog.Any("err", err),
		)

		return false
	}

	return count >= active
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

// loadPlan loads the quiz and projects it into the runner's question plan.
func (r *Runner) loadPlan(ctx context.Context, sess *Session) (questionPlan, error) {
	qz, err := r.quizzes.GetQuiz(ctx, sess.QuizID)
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

// readySince returns when the lobby became all-ready, seeding it with now the
// first time the runner observes the all-ready state.
func (r *Runner) readySince(sessionID string, now time.Time) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.allReadySince[sessionID]
	if !ok {
		r.allReadySince[sessionID] = now
		t = now
	}

	return t
}

func (r *Runner) clearReadySince(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.allReadySince, sessionID)
}

func (r *Runner) forget(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.phaseSince, sessionID)
	delete(r.allReadySince, sessionID)
}

// allPlayersReady reports whether a lobby has at least one player and every
// joined player is ready. The host is not a roster player, so a host-only
// room never auto-starts (the host uses Start).
func allPlayersReady(sess *Session) bool {
	if len(sess.Players) == 0 {
		return false
	}
	for _, p := range sess.Players {
		if !p.IsReady {
			return false
		}
	}

	return true
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
