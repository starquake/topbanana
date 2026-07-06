package game

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/starquake/topbanana/internal/quiz"
)

const (
	// defaultRevealDelay is the wall-clock gap between issuing a
	// question and revealing the answer options. The player sees the
	// question text immediately and gets the delay "for free" to read
	// it before the per-question countdown starts - see #247. The
	// server shifts StartedAt into the future by this amount so the
	// answer window (StartedAt -> ExpiredAt) starts AFTER the reveal,
	// not from the moment the question was issued.
	defaultRevealDelay = 3 * time.Second
	// defaultStalePeriod is the grace window for the in-progress dot
	// (#336): a 10s answer window plus slack for reveal beats and
	// mobile network jitter.
	defaultStalePeriod = 30 * time.Second

	// lateAnswerGrace is how far past ExpiredAt an answer may still land,
	// covering a last-instant tap delayed in flight (#1163).
	lateAnswerGrace = 2 * time.Second

	// errGetGameFmt is the wrap format for store.GetGame errors. Every
	// entry-point gate (GetNextQuestion, GetNext, MarkRoundSeen,
	// SubmitAnswer, GetResults) wraps the failure with the same
	// "failed to get game" prefix - revive's add-constant rule fires
	// after four occurrences, so we hoist the format string here.
	errGetGameFmt = "failed to get game: %w"
)

// Service exposes the quiz-gameplay use cases on top of the store layer
// (game + quiz). Holds a logger and an optional LeaderboardPublisher.
type Service struct {
	store                Store
	quizStore            quiz.Store
	logger               *slog.Logger
	leaderboardPublisher LeaderboardPublisher
	revealDelay          time.Duration
	stalePeriod          time.Duration
}

// NewService initializes and returns a new instance of Service with the provided game and quiz stores.
func NewService(gameStore Store, quizStore quiz.Store, logger *slog.Logger) *Service {
	return &Service{
		store:       gameStore,
		quizStore:   quizStore,
		logger:      logger,
		revealDelay: defaultRevealDelay,
		stalePeriod: defaultStalePeriod,
	}
}

// SetStalePeriod overrides the in-progress dot grace window (#336).
// Not safe for concurrent use; call during startup wiring.
func (s *Service) SetStalePeriod(d time.Duration) {
	s.stalePeriod = d
}

// GetQuiz proxies to the wrapped quiz store. Exposed so clientapi
// handlers can apply the #103 visibility gate without taking a separate
// quiz.Store parameter (every leaderboard / my-game / create-game
// handler already needs the *Service).
func (s *Service) GetQuiz(ctx context.Context, id int64) (*quiz.Quiz, error) {
	qz, err := s.quizStore.GetQuiz(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get quiz %d: %w", id, err)
	}

	return qz, nil
}

// GetQuizMeta proxies to the quiz store's metadata read.
func (s *Service) GetQuizMeta(ctx context.Context, id int64) (*quiz.Quiz, error) {
	qz, err := s.quizStore.GetQuizMeta(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get quiz meta %d: %w", id, err)
	}

	return qz, nil
}

// GetQuizVisibility proxies to the wrapped quiz store. Exposed so the
// clientapi read-path visibility gate can check existence + visibility
// without paying the questions/options fan-out GetQuiz performs.
func (s *Service) GetQuizVisibility(ctx context.Context, id int64) (string, error) {
	visibility, err := s.quizStore.GetQuizVisibility(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get quiz visibility %d: %w", id, err)
	}

	return visibility, nil
}

// SetRevealDelay overrides the per-question reveal beat (#247). The default
// is 3 s - long enough to read the prompt before the option buttons appear.
// E2E and load-test deployments shrink this to a few hundred ms to speed up
// runs without losing the visual reveal phase.
//
// Not safe for concurrent use: must be called during startup wiring, before
// the service is handed to any HTTP handler that may invoke GetNextQuestion.
func (s *Service) SetRevealDelay(d time.Duration) {
	s.revealDelay = d
}

// SetLeaderboardPublisher wires a publisher invoked on every successful
// SubmitAnswer so SSE subscribers (or any other listener) learn about
// score changes. Optional - Service works fine without one.
//
// Not safe for concurrent use: must be called during startup wiring,
// before the service is handed to any HTTP handler that may invoke
// SubmitAnswer. There is no in-flight reconfiguration use case for
// this field; if one ever appears, swap the bare field for an
// atomic.Pointer.
func (s *Service) SetLeaderboardPublisher(p LeaderboardPublisher) {
	s.leaderboardPublisher = p
}

// PublishLeaderboardForPlayer fans out a leaderboard tick on every
// quiz where the given player has at least one answer. The claim-name
// flow calls this after a successful rename so all SSE subscribers see
// the new display name on the player's existing row without waiting
// for the next answer-submit publish.
//
// The store lookup error is returned to the caller; per-publish steps
// are best-effort (the publisher is nil-tolerant and the Publish call
// itself never returns).
func (s *Service) PublishLeaderboardForPlayer(ctx context.Context, playerID int64) error {
	if s.leaderboardPublisher == nil {
		return nil
	}
	quizIDs, err := s.store.ListQuizIDsForPlayer(ctx, playerID)
	if err != nil {
		return fmt.Errorf("list quiz IDs for player %d: %w", playerID, err)
	}
	for _, quizID := range quizIDs {
		s.leaderboardPublisher.Publish(quizID)
	}

	return nil
}

// CreateGame creates a solo game for the given quiz and player. When preview is
// true it delegates to [Service.CreatePreviewGame]; otherwise a draft or live
// quiz 404s as [quiz.ErrQuizNotFound] and an existing real game returns
// [ErrGameAlreadyExists] (also enforced by the game_participants UNIQUE index).
//
//nolint:revive // preview selects the preview-play path (a distinct create flow), not a behavioural mode switch inside one flow.
func (s *Service) CreateGame(ctx context.Context, quizID, playerID int64, preview bool) (*Game, error) {
	qz, err := s.quizStore.GetQuiz(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to get quiz: %w", err)
	}

	if preview {
		return s.CreatePreviewGame(ctx, qz, playerID)
	}

	// Live quizzes are hosted-only (#677) and drafts are not real-playable (#1192); surface ErrQuizNotFound so neither is distinguishable from a missing quiz.
	if qz.Mode == quiz.ModeLive || !qz.Published {
		return nil, quiz.ErrQuizNotFound
	}

	existing, err := s.store.GetGameByPlayerAndQuiz(ctx, playerID, qz.ID)
	if err != nil && !errors.Is(err, ErrGameNotFound) {
		return nil, fmt.Errorf("failed to check existing game: %w", err)
	}
	if existing != nil {
		// A prior preview game does not consume the one real attempt (#1192); reset it. A real prior game still blocks.
		if !existing.Preview {
			return nil, ErrGameAlreadyExists
		}
		if err = s.store.DeleteGamesForPlayerOnQuiz(ctx, playerID, qz.ID); err != nil {
			return nil, fmt.Errorf("failed to reset prior preview game: %w", err)
		}
	}

	// CreateGame + CreateParticipant + StartGame run in a single
	// transaction (#351) so a crash mid-flow can't leave an orphan
	// games row. The UNIQUE(player_id, quiz_id) loser surfaces as
	// ErrGameAlreadyExists from inside the txn.
	g := &Game{QuizID: qz.ID}
	pa := &Participant{PlayerID: playerID, QuizID: qz.ID}
	if err = s.store.CreateGameAndParticipant(ctx, g, pa); err != nil {
		if errors.Is(err, ErrGameAlreadyExists) {
			return nil, ErrGameAlreadyExists
		}

		return nil, fmt.Errorf("failed to create game and participant: %w", err)
	}
	g.Quiz = qz

	// Repaint subscribers so the new participant appears on the live
	// leaderboard at score 0 / in-progress immediately (#335). Without
	// this fire, existing subscribers (hosts watching the start screen,
	// other players on the same quiz) would only see the row once the
	// player committed their first answer. Nil-guarded to match
	// PublishLeaderboardForPlayer / SubmitAnswer - tests can construct
	// a Service without wiring a publisher.
	if s.leaderboardPublisher != nil {
		s.leaderboardPublisher.Publish(qz.ID)
	}

	return g, nil
}

// GetGameForPlayerOnQuiz returns the player's most-recent game for the given
// quiz with [Game.Quiz] populated so callers can call [Game.IsCompleted].
//
// Returns [ErrGameNotFound] when the player has no game for the quiz, and
// [quiz.ErrQuizNotFound] when the quiz itself does not exist.
func (s *Service) GetGameForPlayerOnQuiz(ctx context.Context, playerID, quizID int64) (*Game, error) {
	// Verify the quiz exists first so callers can map ErrQuizNotFound to
	// a 404 distinct from "no game yet".
	qz, err := s.quizStore.GetQuiz(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to load quiz for player resume: %w", err)
	}

	// Resume only the real (non-preview) game so an owner-preview never surfaces as resumable (#1192).
	g, err := s.store.GetRealGameByPlayerAndQuiz(ctx, playerID, quizID)
	if err != nil {
		return nil, fmt.Errorf("failed to load game for player resume: %w", err)
	}

	g.Quiz = qz

	return g, nil
}

// GetAudioManifest returns the questions of the game's quiz so a caller can
// build the per-question audio preload list. Participant-gated exactly like
// [Service.GetNextQuestion]: a non-participant gets [ErrGameNotFound] so the
// gameID stays opaque to outsiders, and a missing quiz surfaces
// [quiz.ErrQuizNotFound]. The questions carry AudioMediaID/AudioRepeat in
// position order; the caller filters to the audio-bearing ones.
func (s *Service) GetAudioManifest(ctx context.Context, gameID string, playerID int64) ([]*quiz.Question, error) {
	_, qz, err := s.loadGameForPlayer(ctx, gameID, playerID)
	if err != nil {
		return nil, err
	}

	return qz.Questions, nil
}

// ResetGamesForPlayerOnQuiz hard-deletes every game (and dependent rows) the
// given player has for the given quiz. The reset is idempotent: running it
// against a (player, quiz) with no games is a no-op success so the admin
// reset button can be pressed safely from any state.
//
// Returns [quiz.ErrQuizNotFound] when the quiz does not exist so the admin
// route can map it to a 404.
func (s *Service) ResetGamesForPlayerOnQuiz(ctx context.Context, playerID, quizID int64) error {
	// Existence-only check: we don't need the quiz's questions or options,
	// so use QuizExists to skip the per-question/per-option fan-out reads
	// GetQuiz performs.
	exists, err := s.quizStore.QuizExists(ctx, quizID)
	if err != nil {
		return fmt.Errorf("failed to check quiz exists for reset: %w", err)
	}
	if !exists {
		return quiz.ErrQuizNotFound
	}

	if err := s.store.DeleteGamesForPlayerOnQuiz(ctx, playerID, quizID); err != nil {
		return fmt.Errorf("failed to delete games for player %d on quiz %d: %w", playerID, quizID, err)
	}

	return nil
}

// GetNextQuestion returns the next unanswered question for the game,
// or nil if all are answered. Idempotent while the answer window is
// open: an unanswered question whose ExpiredAt is still in the future
// is returned with its original StartedAt/ExpiredAt anchor, so a
// reload resumes on the same question without restarting the timer.
func (s *Service) GetNextQuestion(ctx context.Context, gameID string, playerID int64) (*Question, error) {
	// Get the game
	g, err := s.store.GetGame(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf(errGetGameFmt, err)
	}

	// Participant gate (#272): non-participants get ErrGameNotFound so
	// the error path is indistinguishable from a genuinely missing
	// game - the gameID stays opaque to outsiders.
	if !hasParticipant(g, playerID) {
		return nil, ErrGameNotFound
	}

	// Get the quiz
	qz, err := s.quizStore.GetQuiz(ctx, g.QuizID)
	if err != nil {
		return nil, fmt.Errorf("failed to get quiz: %w", err)
	}

	g.Quiz = qz

	// Resume path: when the latest issued game_question is unanswered
	// and the answer window is still open, hand back the same row so a
	// reload doesn't skip the question.
	if gq := resumeCandidate(g, qz); gq != nil {
		return gq, nil
	}

	askedQuestions := make(map[int64]bool)
	for _, gqs := range g.Questions {
		askedQuestions[gqs.QuestionID] = true
	}

	var nextQuestion *quiz.Question
	for _, q := range qz.Questions {
		if !askedQuestions[q.ID] {
			nextQuestion = q

			break
		}
	}

	if nextQuestion == nil {
		return nil, ErrNoMoreQuestions
	}

	// The answer window (StartedAt -> ExpiredAt) is anchored at now +
	// revealDelay, not "now" - the reveal delay gives the player a brief
	// beat to read the question before the option buttons appear (#247).
	revealAt := time.Now().Add(s.revealDelay)
	gq := &Question{
		GameID:       gameID,
		QuestionID:   nextQuestion.ID,
		QuizQuestion: nextQuestion,
		StartedAt:    revealAt,
		ExpiredAt:    revealAt.Add(resolveAnswerWindow(nextQuestion, qz)),
		// Position counts the newly-issued question itself, so it's
		// the prior asked count + 1 (the player just received this
		// question; previous answers were the N-1 before it).
		Position: len(g.Questions) + 1,
		Total:    len(qz.Questions),
	}
	applyRoundProgress(gq, qz)
	if err = s.store.CreateQuestion(ctx, gq, completesGame(gq)); err != nil {
		if errors.Is(err, ErrQuestionAlreadyIssued) {
			return gq, nil
		}

		return nil, fmt.Errorf("failed to record game question: %w", err)
	}

	return gq, nil
}

// applyRoundProgress stamps the question's round placement (Round N of M, plus
// its position within the round) onto gq from the quiz's questions, for the
// gameplay header.
func applyRoundProgress(gq *Question, qz *quiz.Quiz) {
	p := quiz.QuestionRoundProgress(qz.Questions, gq.QuestionID)
	gq.RoundNumber = p.RoundNumber
	gq.RoundTotal = p.RoundTotal
	gq.RoundPosition = p.RoundPosition
	gq.RoundQuestions = p.RoundQuestions
}

// completesGame reports whether gq is the question whose insertion flips
// [Game.IsCompleted] to true - the last question of the quiz. The caller's
// not-yet-asked gate guarantees one insert per completion, so the store's
// play_count bump (#891) keyed on this fires exactly once per game.
func completesGame(gq *Question) bool {
	return gq.Total > 0 && gq.Position >= gq.Total
}

// GetNext returns the next item in the play sequence. The resume path
// short-circuits: an unanswered question still inside its answer
// window is handed back unchanged so a reload does not skip ahead.
// Returns [ErrNoMoreQuestions] when nothing is left (kept for legacy
// reasons; it covers items, not just questions).
func (s *Service) GetNext(ctx context.Context, gameID string, playerID int64) (*Item, error) {
	g, qz, err := s.loadGameForPlayer(ctx, gameID, playerID)
	if err != nil {
		return nil, err
	}

	// Resume path: keep the player on an in-flight question through a
	// reload, matching GetNextQuestion's semantics. A break is never
	// "in flight" - it is either seen or unseen - so the resume path
	// only fires for questions.
	if gq := resumeCandidate(g, qz); gq != nil {
		return &Item{Type: ItemTypeQuestion, Question: gq}, nil
	}

	rounds, err := s.quizStore.ListRoundsByQuiz(ctx, qz.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list rounds: %w", err)
	}

	seenPhases, err := s.store.ListSeenRoundPhasesByGame(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("failed to list seen rounds: %w", err)
	}

	askedQuestions := make(map[int64]bool, len(g.Questions))
	for _, gq := range g.Questions {
		askedQuestions[gq.QuestionID] = true
	}
	seenRoundPhases := make(map[seenKey]bool, len(seenPhases))
	for _, sp := range seenPhases {
		seenRoundPhases[seenKey{roundID: sp.RoundID, phase: sp.Phase}] = true
	}

	switch next := nextRoundSlot(rounds, qz.Questions, askedQuestions, seenRoundPhases); next.kind {
	case slotKindRoundBoundary:
		return s.buildRoundBoundaryItem(ctx, g, qz, playerID, next.round, next.phase)
	case slotKindQuestion:
		gq, qErr := s.issueQuestion(ctx, gameID, qz, next.question, len(g.Questions))
		if qErr != nil {
			return nil, qErr
		}

		return &Item{Type: ItemTypeQuestion, Question: gq}, nil
	default:
		return nil, ErrNoMoreQuestions
	}
}

// MarkRoundSeen records that the player has acknowledged the given
// phase of a round boundary. Idempotent at the store layer. Tracks the
// round by id, so an admin who reorders rounds while a player has a
// round boundary on screen will not re-show it.
func (s *Service) MarkRoundSeen(ctx context.Context, gameID string, playerID, roundID int64, phase RoundPhase) error {
	if !phase.Valid() {
		return ErrInvalidRoundPhase
	}

	g, err := s.store.GetGame(ctx, gameID)
	if err != nil {
		return fmt.Errorf(errGetGameFmt, err)
	}
	if !hasParticipant(g, playerID) {
		return ErrGameNotFound
	}

	round, err := s.quizStore.GetRound(ctx, roundID)
	if err != nil {
		return fmt.Errorf("failed to get round: %w", err)
	}
	if round.QuizID != g.QuizID {
		return quiz.ErrRoundNotFound
	}

	if err := s.store.MarkRoundSeen(ctx, gameID, roundID, phase); err != nil {
		return fmt.Errorf("failed to mark round seen: %w", err)
	}

	return nil
}

// SubmitAnswer records a player's answer. Answers past the window
// (ExpiredAt plus the latency grace) are rejected with
// ErrAnswerWindowClosed; otherwise tappedAt is refunded up to
// maxLatencyRefund so a slow link is not penalised but a client cannot
// claim the window start (#237, #1163).
func (s *Service) SubmitAnswer(
	ctx context.Context,
	gameID string,
	playerID, questionID, optionID int64,
	tappedAt time.Time,
) (*Answer, error) {
	g, err := s.store.GetGame(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf(errGetGameFmt, err)
	}

	// Participant gate (#272): non-participants get ErrGameNotFound. The
	// answer-post path previously trusted the (gameID, playerID) pair the
	// caller supplied, so a third party could land an answer row in
	// someone else's game.
	if !hasParticipant(g, playerID) {
		return nil, ErrGameNotFound
	}

	question, option, err := s.resolveAnswerTarget(ctx, g, gameID, questionID, optionID)
	if err != nil {
		return nil, err
	}

	// Reject an answer that lands past the window; it scores nothing (#1163).
	now := time.Now()
	if now.After(question.ExpiredAt.Add(lateAnswerGrace)) {
		return nil, ErrAnswerWindowClosed
	}

	a := &Answer{
		GameID:     gameID,
		PlayerID:   playerID,
		QuestionID: question.ID,
		Question:   question,
		OptionID:   optionID,
		Option:     option,
		AnsweredAt: clampTappedAt(tappedAt, now, maxLatencyRefund),
	}

	if err = s.store.CreateAnswer(ctx, a); err != nil {
		// Pass ErrAnswerAlreadyRecorded through unwrapped so the
		// handler can map it to 409 instead of 500 - a double-tap is
		// a retry, not a server fault (#353).
		if errors.Is(err, ErrAnswerAlreadyRecorded) {
			return nil, ErrAnswerAlreadyRecorded
		}

		return nil, fmt.Errorf("failed to create answer: %w", err)
	}

	// Signal SSE subscribers that the leaderboard has moved. Non-blocking
	// (the hub buffers one event per subscriber and drops on backpressure),
	// so this never delays the answer-submit response.
	if s.leaderboardPublisher != nil {
		s.leaderboardPublisher.Publish(g.QuizID)
	}

	return a, nil
}

// GetResults calculates the accumulated score for each player in a game and
// returns the results. Requires playerID for the participant gate (#272);
// non-participants get ErrGameNotFound so the gameID itself can't be used
// to read the score map of a game the caller is not in.
func (s *Service) GetResults(ctx context.Context, gameID string, playerID int64) (*Results, error) {
	g, err := s.store.GetGame(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf(errGetGameFmt, err)
	}

	if !hasParticipant(g, playerID) {
		return nil, ErrGameNotFound
	}

	// Collect all option IDs needed across all answers in one pass.
	var optionIDs []int64
	for _, gqs := range g.Questions {
		for _, ga := range gqs.Answers {
			optionIDs = append(optionIDs, ga.OptionID)
		}
	}

	options, err := s.quizStore.GetOptionsByIDs(ctx, optionIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to get options: %w", err)
	}

	optionsByID := make(map[int64]*quiz.Option, len(options))
	for _, o := range options {
		optionsByID[o.ID] = o
	}

	plsMap := make(map[int64]int, len(g.Participants))
	for _, gqs := range g.Questions {
		for _, ga := range gqs.Answers {
			ga.Question = gqs
			ga.Option = optionsByID[ga.OptionID]
			// A deleted option leaves a dangling answer; skip it so
			// CalculateScore never dereferences a nil Option.
			if ga.Option == nil {
				continue
			}
			plsMap[ga.PlayerID] += s.CalculateScore(ctx, ga)
		}
	}

	// Seed at 0 so an all-wrong run leaves Winner == 0 (no winner) rather than
	// crowning a zero-score player.
	var winner int64
	topScore := 0
	for playerID, score := range plsMap {
		if score > topScore {
			topScore = score
			winner = playerID
		} else if score == topScore {
			winner = 0
		}
	}

	return &Results{GameID: g.ID, Winner: winner, PlayerScores: plsMap}, nil
}

// CreatePreviewGame creates an owner preview game from an already-loaded quiz: a
// solo-only, off-leaderboard test-play of a draft (#1192). A live or published
// quiz returns [ErrPreviewNotAllowed]; any prior game for the pair is reset first
// so re-previewing works. Ownership is enforced by the caller.
func (s *Service) CreatePreviewGame(ctx context.Context, qz *quiz.Quiz, playerID int64) (*Game, error) {
	// Solo drafts only. Rejecting a published quiz is load-bearing: the reset below would otherwise hard-delete the requester's real game and destroy their leaderboard entry (#1192).
	if qz.Mode != quiz.ModeSolo || qz.Published {
		return nil, ErrPreviewNotAllowed
	}

	if err := s.store.DeleteGamesForPlayerOnQuiz(ctx, playerID, qz.ID); err != nil {
		return nil, fmt.Errorf("failed to reset prior game for preview: %w", err)
	}

	g := &Game{QuizID: qz.ID, Preview: true}
	pa := &Participant{PlayerID: playerID, QuizID: qz.ID}
	if err := s.store.CreateGameAndParticipant(ctx, g, pa); err != nil {
		return nil, fmt.Errorf("failed to create preview game and participant: %w", err)
	}
	g.Quiz = qz

	return g, nil
}

// resolveAnswerTarget finds the issued game_question for the supplied
// questionID and the option for the supplied optionID, loading the
// option set in one round-trip so [Service.SubmitAnswer] can also
// surface the correct options on a wrong-pick reveal (#233). Returns
// [ErrQuestionNotInGame] or [ErrOptionNotInQuestion] when the lookup
// misses; pulled out of SubmitAnswer to keep it under revive's
// function-length cap.
func (s *Service) resolveAnswerTarget(
	ctx context.Context, g *Game, gameID string, questionID, optionID int64,
) (*Question, *quiz.Option, error) {
	var question *Question
	for _, qs := range g.Questions {
		if qs.QuestionID == questionID {
			question = qs

			break
		}
	}
	if question == nil {
		return nil, nil, fmt.Errorf(
			"question %d not found in game %s: %w", questionID, gameID, ErrQuestionNotInGame,
		)
	}

	quizQuestion, err := s.quizStore.GetQuestion(ctx, question.QuestionID)
	if err != nil {
		// A host can delete the question mid-game; treat it as
		// no-longer-in-game so the submit path 404s instead of 500ing (#1180).
		if errors.Is(err, quiz.ErrQuestionNotFound) {
			return nil, nil, fmt.Errorf(
				"question %d deleted from game %s: %w", question.QuestionID, gameID, ErrQuestionNotInGame,
			)
		}

		return nil, nil, fmt.Errorf("failed to get question: %w", err)
	}
	question.QuizQuestion = quizQuestion

	for _, o := range quizQuestion.Options {
		if o.ID == optionID {
			return question, o, nil
		}
	}

	return nil, nil, fmt.Errorf(
		"option %d not in question %d: %w",
		optionID,
		question.QuestionID,
		ErrOptionNotInQuestion,
	)
}

// loadGameForPlayer is the entry-point gate shared by [Service.GetNext]
// and the new MarkBreakSeen flow. It loads the game, applies the #272
// participant check (non-participants get ErrGameNotFound so the gameID
// stays opaque), and attaches the populated quiz. Pulled out so the two
// callers stay short and the gate logic lives in one place.
func (s *Service) loadGameForPlayer(
	ctx context.Context, gameID string, playerID int64,
) (*Game, *quiz.Quiz, error) {
	g, err := s.store.GetGame(ctx, gameID)
	if err != nil {
		return nil, nil, fmt.Errorf(errGetGameFmt, err)
	}
	if !hasParticipant(g, playerID) {
		return nil, nil, ErrGameNotFound
	}

	qz, err := s.quizStore.GetQuiz(ctx, g.QuizID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get quiz: %w", err)
	}
	g.Quiz = qz

	return g, qz, nil
}

// issueQuestion creates the game_questions row for the chosen quiz
// question and returns the populated [Question] the handler hands back
// to the player. The reveal-delay + answer-window arithmetic matches
// [Service.GetNextQuestion] exactly so the two entry points stay
// behavior-equivalent on the question path (#167 slice 2 / #247).
func (s *Service) issueQuestion(
	ctx context.Context, gameID string, qz *quiz.Quiz, q *quiz.Question, askedCount int,
) (*Question, error) {
	revealAt := time.Now().Add(s.revealDelay)
	gq := &Question{
		GameID:       gameID,
		QuestionID:   q.ID,
		QuizQuestion: q,
		StartedAt:    revealAt,
		ExpiredAt:    revealAt.Add(resolveAnswerWindow(q, qz)),
		Position:     askedCount + 1,
		Total:        len(qz.Questions),
	}
	applyRoundProgress(gq, qz)
	if err := s.store.CreateQuestion(ctx, gq, completesGame(gq)); err != nil {
		if errors.Is(err, ErrQuestionAlreadyIssued) {
			return gq, nil
		}

		return nil, fmt.Errorf("failed to record game question: %w", err)
	}

	return gq, nil
}

// computeGameScore aggregates the requesting player's running score
// across every game_answer recorded on the game so far, reusing
// [Service.CalculateScore] for the per-answer points. Used by
// [Service.GetNext] to populate the running total on a round item so
// the player sees their score on the round screen without a separate
// round-trip (#167 slice 2). Filters by playerID because the loaded
// game carries answers from every participant.
func (s *Service) computeGameScore(ctx context.Context, g *Game, playerID int64) (int, error) {
	result, err := s.scoreAnswers(ctx, g, playerID, nil)

	return result.Score, err
}

// scoreResult is the outcome of [Service.scoreAnswers]: the summed
// points and the number of correctly answered questions over the scored
// answer set.
type scoreResult struct {
	Score   int
	Correct int
}

// scoreAnswers scores the requesting player's recorded answers, reusing
// [Service.CalculateScore] for the per-answer points and a single
// GetOptionsByIDs round-trip for the correctness flags. When include is
// non-nil, only answers to questions for which include returns true are
// counted, which lets the results-phase round recap score one round's
// questions through the same path as the running total.
func (s *Service) scoreAnswers(
	ctx context.Context, g *Game, playerID int64, include func(questionID int64) bool,
) (scoreResult, error) {
	answers := collectPlayerAnswers(g, playerID, include)
	if len(answers) == 0 {
		return scoreResult{}, nil
	}

	optionIDs := make([]int64, len(answers))
	for i, ga := range answers {
		optionIDs[i] = ga.OptionID
	}

	options, err := s.quizStore.GetOptionsByIDs(ctx, optionIDs)
	if err != nil {
		return scoreResult{}, fmt.Errorf("failed to get options for round score: %w", err)
	}
	optionsByID := make(map[int64]*quiz.Option, len(options))
	for _, o := range options {
		optionsByID[o.ID] = o
	}

	var result scoreResult
	for _, ga := range answers {
		ga.Option = optionsByID[ga.OptionID]
		if ga.Option == nil {
			continue
		}
		if ga.Option.Correct {
			result.Correct++
		}
		result.Score += s.CalculateScore(ctx, ga)
	}

	return result, nil
}

// collectPlayerAnswers gathers the player's answers across the game's
// issued questions, attaching each answer's owning question so
// [Service.CalculateScore] can read the timing window. When include is
// non-nil, only answers to questions it accepts are returned.
func collectPlayerAnswers(
	g *Game, playerID int64, include func(questionID int64) bool,
) []*Answer {
	var answers []*Answer
	for _, gq := range g.Questions {
		if include != nil && !include(gq.QuestionID) {
			continue
		}
		for _, ga := range gq.Answers {
			if ga.PlayerID != playerID {
				continue
			}
			ga.Question = gq
			answers = append(answers, ga)
		}
	}

	return answers
}

// buildRoundBoundaryItem assembles the round-boundary [Item] for the
// given phase and the quiz question total for the HUD chip. The intro
// phase carries no score (the wire shape omits it), so the running total
// and per-round recap are computed only for the results phase.
func (s *Service) buildRoundBoundaryItem(
	ctx context.Context, g *Game, qz *quiz.Quiz, playerID int64, round *quiz.Round, phase RoundPhase,
) (*Item, error) {
	startedAt := time.Now()
	item := &Item{
		Type:      ItemTypeRoundBoundary,
		Round:     round,
		Total:     len(qz.Questions),
		Phase:     phase,
		StartedAt: startedAt,
		ExpiredAt: startedAt.Add(resolveRoundBoundaryWindow(round, qz)),
	}
	if phase != RoundPhaseResults {
		return item, nil
	}

	score, err := s.computeGameScore(ctx, g, playerID)
	if err != nil {
		return nil, err
	}
	item.Score = score

	recap, err := s.computeRoundRecap(ctx, g, qz, playerID, round.ID)
	if err != nil {
		return nil, err
	}
	item.RoundScore = recap.Score
	item.RoundCorrect = recap.Correct
	item.RoundQuestions = recap.Questions

	return item, nil
}

// roundRecap is the player's self-referential recap for one round: the
// score earned for the round's questions, the number answered
// correctly, and the round's question count (the denominator).
type roundRecap struct {
	Score     int
	Correct   int
	Questions int
}

// computeRoundRecap scores one round for the player, reusing
// [Service.scoreAnswers] scoped to the round's question IDs so there is
// a single scoring path. The denominator counts the round's authored
// questions, not just the issued ones, so a recap shown after the last
// question still reads "n / total".
func (s *Service) computeRoundRecap(
	ctx context.Context, g *Game, qz *quiz.Quiz, playerID, roundID int64,
) (roundRecap, error) {
	roundQuestionIDs := make(map[int64]bool)
	for _, q := range qz.Questions {
		if q.RoundID == roundID {
			roundQuestionIDs[q.ID] = true
		}
	}

	result, err := s.scoreAnswers(ctx, g, playerID, func(questionID int64) bool {
		return roundQuestionIDs[questionID]
	})
	if err != nil {
		return roundRecap{}, err
	}

	return roundRecap{
		Score:     result.Score,
		Correct:   result.Correct,
		Questions: len(roundQuestionIDs),
	}, nil
}
