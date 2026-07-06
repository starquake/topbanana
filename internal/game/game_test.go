package game_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	. "github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/quiz"
)

var errStub = errors.New("stub error")

// stubStore satisfies game.Store for service-level tests that do not need a
// live database. Each behaviour is overridable per test via a func field; a
// nil field returns errStub so accidental use surfaces loudly. The
// getGameByPlayerAndQuiz field defaults to "not found" rather than errStub
// so the existing CreateGame happy-path tests do not have to opt in.
type stubStore struct {
	getGame                            func(ctx context.Context, gameID string) (*Game, error)
	listAnswersForQuizLeaderboard      func(ctx context.Context, quizID int64) ([]*LeaderboardAnswer, error)
	listParticipantsForQuizLeaderboard func(ctx context.Context, quizID int64, staleBefore time.Time) ([]*LeaderboardParticipant, error)
	getGameByPlayerAndQuiz             func(ctx context.Context, playerID, quizID int64) (*Game, error)
	getRealGameByPlayerAndQuiz         func(ctx context.Context, playerID, quizID int64) (*Game, error)
	deleteGamesForPlayerOnQuiz         func(ctx context.Context, playerID, quizID int64) error
	listQuizIDsForPlayer               func(ctx context.Context, playerID int64) ([]int64, error)
	markRoundSeen                      func(ctx context.Context, gameID string, roundID int64, phase RoundPhase) error
	listSeenRoundPhasesByGame          func(ctx context.Context, gameID string) ([]SeenRoundPhase, error)
}

func (stubStore) Ping(_ context.Context) error { return nil }

func (s stubStore) GetGame(ctx context.Context, gameID string) (*Game, error) {
	if s.getGame == nil {
		return nil, errStub
	}

	return s.getGame(ctx, gameID)
}

func (s stubStore) GetGameByPlayerAndQuiz(
	ctx context.Context, playerID, quizID int64,
) (*Game, error) {
	if s.getGameByPlayerAndQuiz == nil {
		return nil, ErrGameNotFound
	}

	return s.getGameByPlayerAndQuiz(ctx, playerID, quizID)
}

func (s stubStore) GetRealGameByPlayerAndQuiz(
	ctx context.Context, playerID, quizID int64,
) (*Game, error) {
	if s.getRealGameByPlayerAndQuiz == nil {
		return nil, ErrGameNotFound
	}

	return s.getRealGameByPlayerAndQuiz(ctx, playerID, quizID)
}
func (stubStore) CreateGame(_ context.Context, _ *Game) error { return errStub }
func (stubStore) CreateGameAndParticipant(_ context.Context, _ *Game, _ *Participant) error {
	return errStub
}
func (stubStore) StartGame(_ context.Context, _ string) error                 { return errStub }
func (stubStore) CreateParticipant(_ context.Context, _ *Participant) error   { return errStub }
func (stubStore) CreateQuestion(_ context.Context, _ *Question, _ bool) error { return errStub }
func (stubStore) CreateAnswer(_ context.Context, _ *Answer) error             { return errStub }

func (s stubStore) ListAnswersForQuizLeaderboard(
	ctx context.Context, quizID int64,
) ([]*LeaderboardAnswer, error) {
	if s.listAnswersForQuizLeaderboard == nil {
		return nil, errStub
	}

	return s.listAnswersForQuizLeaderboard(ctx, quizID)
}

// ListParticipantsForQuizLeaderboard serves the participants stub when
// set; otherwise it derives participants from the configured answer
// stub so existing leaderboard tests (which only seeded answers) keep
// passing. The #335 service-level test sets the participants stub
// explicitly to exercise the no-answer-participant path the fallback
// can't represent.
func (s stubStore) ListParticipantsForQuizLeaderboard(
	ctx context.Context, quizID int64, staleBefore time.Time,
) ([]*LeaderboardParticipant, error) {
	if s.listParticipantsForQuizLeaderboard != nil {
		return s.listParticipantsForQuizLeaderboard(ctx, quizID, staleBefore)
	}
	if s.listAnswersForQuizLeaderboard == nil {
		return nil, errStub
	}

	answers, err := s.listAnswersForQuizLeaderboard(ctx, quizID)
	if err != nil {
		return nil, err
	}

	seen := make(map[int64]int) // playerID -> index in out
	var out []*LeaderboardParticipant
	for _, a := range answers {
		if i, ok := seen[a.PlayerID]; ok {
			out[i].IsCompleted = a.IsCompleted

			continue
		}
		seen[a.PlayerID] = len(out)
		out = append(out, &LeaderboardParticipant{
			PlayerID: a.PlayerID, DisplayName: a.DisplayName, IsCompleted: a.IsCompleted,
		})
	}

	return out, nil
}

func (s stubStore) DeleteGamesForPlayerOnQuiz(
	ctx context.Context, playerID, quizID int64,
) error {
	if s.deleteGamesForPlayerOnQuiz == nil {
		return errStub
	}

	return s.deleteGamesForPlayerOnQuiz(ctx, playerID, quizID)
}

func (s stubStore) ListQuizIDsForPlayer(ctx context.Context, playerID int64) ([]int64, error) {
	if s.listQuizIDsForPlayer == nil {
		return nil, errStub
	}

	return s.listQuizIDsForPlayer(ctx, playerID)
}

func (s stubStore) MarkRoundSeen(
	ctx context.Context, gameID string, roundID int64, phase RoundPhase,
) error {
	if s.markRoundSeen == nil {
		return errStub
	}

	return s.markRoundSeen(ctx, gameID, roundID, phase)
}

// ListSeenRoundPhasesByGame defaults to "no seen phases" so tests that
// don't care about round boundaries don't have to wire a stub. The
// round-walking iterator in [Service.GetNext] calls this on every
// request.
func (s stubStore) ListSeenRoundPhasesByGame(
	ctx context.Context, gameID string,
) ([]SeenRoundPhase, error) {
	if s.listSeenRoundPhasesByGame == nil {
		return nil, nil
	}

	return s.listSeenRoundPhasesByGame(ctx, gameID)
}

// stubQuizStore satisfies quiz.Store for service-level tests. Only GetQuiz
// and QuizExists are overridable since the leaderboard/reset paths never
// reach the other methods.
type stubQuizStore struct {
	getQuiz         func(ctx context.Context, id int64) (*quiz.Quiz, error)
	quizExists      func(ctx context.Context, id int64) (bool, error)
	getOptionsByIDs func(ctx context.Context, ids []int64) ([]*quiz.Option, error)
}

func (stubQuizStore) Ping(_ context.Context) error                        { return nil }
func (stubQuizStore) ListQuizzes(_ context.Context) ([]*quiz.Quiz, error) { return nil, errStub }
func (stubQuizStore) ListQuizzesForOwner(_ context.Context, _ int64) ([]*quiz.Quiz, error) {
	return nil, errStub
}

func (stubQuizStore) ListPublicQuizzes(_ context.Context) ([]*quiz.Quiz, error) {
	return nil, errStub
}

func (stubQuizStore) ListLiveQuizzes(_ context.Context) ([]*quiz.Quiz, error) {
	return nil, errStub
}

func (stubQuizStore) ListLiveQuizzesForOwner(_ context.Context, _ int64) ([]*quiz.Quiz, error) {
	return nil, errStub
}

func (stubQuizStore) QuestionCountsByQuiz(_ context.Context) (map[int64]int, error) {
	return nil, errStub
}

func (stubQuizStore) RoundCountsByQuiz(_ context.Context) (map[int64]int, error) {
	return nil, errStub
}

func (s stubQuizStore) GetQuiz(ctx context.Context, id int64) (*quiz.Quiz, error) {
	if s.getQuiz == nil {
		return nil, errStub
	}

	return s.getQuiz(ctx, id)
}

func (s stubQuizStore) QuizExists(ctx context.Context, id int64) (bool, error) {
	if s.quizExists == nil {
		return false, errStub
	}

	return s.quizExists(ctx, id)
}

func (stubQuizStore) GetQuizMeta(_ context.Context, _ int64) (*quiz.Quiz, error) {
	return nil, errStub
}

func (stubQuizStore) GetQuizVisibility(_ context.Context, _ int64) (string, error) {
	return "", errStub
}
func (stubQuizStore) CreateQuiz(_ context.Context, _ *quiz.Quiz) error       { return errStub }
func (stubQuizStore) UpdateQuiz(_ context.Context, _ *quiz.Quiz) error       { return errStub }
func (stubQuizStore) DeleteQuiz(_ context.Context, _ int64) error            { return errStub }
func (stubQuizStore) SetQuizMode(_ context.Context, _ int64, _ string) error { return errStub }
func (stubQuizStore) SetQuizPublished(_ context.Context, _ int64, _ bool) error {
	return errStub
}

func (stubQuizStore) QuizHasRealPlays(_ context.Context, _ int64) (bool, error) {
	return false, errStub
}

func (stubQuizStore) UnpublishQuizIfUnplayed(_ context.Context, _ int64) (bool, error) {
	return false, errStub
}
func (stubQuizStore) CreateQuestion(_ context.Context, _ *quiz.Question) error { return errStub }
func (stubQuizStore) CreateQuestionAtNextPosition(_ context.Context, _ *quiz.Question) error {
	return errStub
}
func (stubQuizStore) UpdateQuestion(_ context.Context, _ *quiz.Question) error { return errStub }

func (stubQuizStore) SetQuestionMedia(_ context.Context, _ int64, _, _ *int64, _ bool) error {
	return errStub
}

func (stubQuizStore) SwapQuestionPositions(_ context.Context, _, _ int64, _ string) error {
	return errStub
}
func (stubQuizStore) DeleteQuestion(_ context.Context, _ int64) error { return errStub }
func (stubQuizStore) ListQuestions(_ context.Context, _ int64) ([]*quiz.Question, error) {
	return nil, errStub
}

func (stubQuizStore) GetQuestion(_ context.Context, _ int64) (*quiz.Question, error) {
	return nil, errStub
}

func (stubQuizStore) GetOption(_ context.Context, _ int64) (*quiz.Option, error) {
	return nil, errStub
}

func (s stubQuizStore) GetOptionsByIDs(ctx context.Context, ids []int64) ([]*quiz.Option, error) {
	if s.getOptionsByIDs == nil {
		return nil, errStub
	}

	return s.getOptionsByIDs(ctx, ids)
}

// ListRoundsByQuiz defaults to "no rounds" so GetNext-style tests that
// don't care about round boundaries don't have to wire a stub. The
// round-walking iterator in game.Service.GetNext calls this on every
// request.
func (stubQuizStore) ListRoundsByQuiz(_ context.Context, _ int64) ([]*quiz.Round, error) {
	return nil, errStub
}

func (stubQuizStore) GetRound(_ context.Context, _ int64) (*quiz.Round, error) {
	return nil, errStub
}

func (stubQuizStore) GetDefaultRound(_ context.Context, _ int64) (*quiz.Round, error) {
	return nil, errStub
}
func (stubQuizStore) CreateRound(_ context.Context, _ *quiz.Round) error { return errStub }
func (stubQuizStore) UpdateRound(_ context.Context, _ *quiz.Round) error { return errStub }
func (stubQuizStore) DeleteRound(_ context.Context, _ int64) error       { return errStub }
func (stubQuizStore) MoveRound(_ context.Context, _, _ int64, _ string) error {
	return errStub
}

func (stubQuizStore) MoveQuestionToRound(_ context.Context, _, _, _ int64) error {
	return errStub
}

func (stubQuizStore) MoveRoundToPosition(_ context.Context, _, _ int64, _ int) error {
	return errStub
}

func (stubQuizStore) MoveQuestionToPosition(_ context.Context, _, _, _ int64, _ int) error {
	return errStub
}

func TestGame_IsCompleted(t *testing.T) {
	t.Parallel()

	t.Run("true when issued questions match quiz length", func(t *testing.T) {
		t.Parallel()

		g := &Game{
			Quiz: &quiz.Quiz{
				Questions: []*quiz.Question{{ID: 1}, {ID: 2}},
			},
			Questions: []*Question{
				{QuestionID: 1},
				{QuestionID: 2},
			},
		}
		if got, want := g.IsCompleted(), true; got != want {
			t.Errorf("IsCompleted() = %v, want %v", got, want)
		}
	})

	t.Run("false when fewer questions issued than quiz length", func(t *testing.T) {
		t.Parallel()

		g := &Game{
			Quiz: &quiz.Quiz{
				Questions: []*quiz.Question{{ID: 1}, {ID: 2}, {ID: 3}},
			},
			Questions: []*Question{
				{QuestionID: 1},
			},
		}
		if got, want := g.IsCompleted(), false; got != want {
			t.Errorf("IsCompleted() = %v, want %v", got, want)
		}
	})

	t.Run("false when zero questions issued and quiz has some", func(t *testing.T) {
		t.Parallel()

		g := &Game{
			Quiz: &quiz.Quiz{
				Questions: []*quiz.Question{{ID: 1}},
			},
		}
		if got, want := g.IsCompleted(), false; got != want {
			t.Errorf("IsCompleted() = %v, want %v", got, want)
		}
	})

	t.Run("false when quiz is not populated", func(t *testing.T) {
		t.Parallel()

		g := &Game{
			Questions: []*Question{{QuestionID: 1}},
		}
		if got, want := g.IsCompleted(), false; got != want {
			t.Errorf("IsCompleted() = %v, want %v (without Quiz the game cannot be known to be complete)", got, want)
		}
	})
}

func TestGame_HasOpenQuestion(t *testing.T) {
	t.Parallel()

	t.Run("true when latest issued question is unanswered and unexpired", func(t *testing.T) {
		t.Parallel()

		future := time.Now().Add(5 * time.Second)
		g := &Game{
			Questions: []*Question{
				{ID: 1, ExpiredAt: future},
			},
		}
		if got, want := g.HasOpenQuestion(), true; got != want {
			t.Errorf("HasOpenQuestion() = %v, want %v", got, want)
		}
	})

	t.Run("false when latest question has an answer", func(t *testing.T) {
		t.Parallel()

		future := time.Now().Add(5 * time.Second)
		g := &Game{
			Questions: []*Question{
				{ID: 1, ExpiredAt: future, Answers: []*Answer{{ID: 1}}},
			},
		}
		if got, want := g.HasOpenQuestion(), false; got != want {
			t.Errorf("HasOpenQuestion() = %v, want %v (answered question cannot be resumed)", got, want)
		}
	})

	t.Run("false when latest question has expired", func(t *testing.T) {
		t.Parallel()

		past := time.Now().Add(-1 * time.Minute)
		g := &Game{
			Questions: []*Question{
				{ID: 1, ExpiredAt: past},
			},
		}
		if got, want := g.HasOpenQuestion(), false; got != want {
			t.Errorf("HasOpenQuestion() = %v, want %v (expired question cannot be resumed)", got, want)
		}
	})

	t.Run("false when no questions issued yet", func(t *testing.T) {
		t.Parallel()

		g := &Game{}
		if got, want := g.HasOpenQuestion(), false; got != want {
			t.Errorf("HasOpenQuestion() = %v, want %v", got, want)
		}
	})

	t.Run("true on the final question when window still open", func(t *testing.T) {
		t.Parallel()

		// Pins the #310 lockout fix: a game with every quiz question
		// issued must still report HasOpenQuestion when the last one
		// is unanswered and unexpired, so the /my-game probe can
		// flip its `completed` field to false and let the client
		// resume instead of dumping the player on the leaderboard.
		future := time.Now().Add(5 * time.Second)
		g := &Game{
			Quiz: &quiz.Quiz{
				Questions: []*quiz.Question{{ID: 1}, {ID: 2}},
			},
			Questions: []*Question{
				{ID: 1, ExpiredAt: future.Add(-10 * time.Second), Answers: []*Answer{{ID: 1}}},
				{ID: 2, ExpiredAt: future},
			},
		}
		if got, want := g.IsCompleted(), true; got != want {
			t.Errorf("IsCompleted() = %v, want %v (every quiz question has been issued)", got, want)
		}
		if got, want := g.HasOpenQuestion(), true; got != want {
			t.Errorf("HasOpenQuestion() = %v, want %v (last question must be resumable)", got, want)
		}
	})
}

func TestService_CalculateScore_EarlyAnswerClamps(t *testing.T) {
	t.Parallel()

	// Hand-crafted client could POST an answer before StartedAt (which
	// sits in the future during the reveal delay - #247). The clamp
	// in CalculateScore must treat the answer as arriving AT
	// StartedAt rather than producing a score above maxPoints from a
	// negative duration.
	svc := NewService(stubStore{}, stubQuizStore{}, slog.New(slog.DiscardHandler))

	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	a := &Answer{
		AnsweredAt: start.Add(-1 * time.Second), // 1s before the reveal lands
		Question: &Question{
			StartedAt: start,
			ExpiredAt: start.Add(10 * time.Second),
		},
		Option: &quiz.Option{Correct: true},
	}

	if got, want := svc.CalculateScore(t.Context(), a), 1000; got != want {
		t.Errorf("CalculateScore for AnsweredAt - StartedAt = -1s, got %d, want %d (clamped to maxPoints)", got, want)
	}
}

// makeAnswer produces a flat LeaderboardAnswer answered at the start of the
// 10s answer window (matching defaultExpiration) so CalculateScore yields a
// predictable maxPoints (1000) for a correct answer or 0 for a wrong one.
func makeAnswer(playerID int64, displayName string, correct bool) *LeaderboardAnswer {
	return makeAnswerCompleted(playerID, displayName, correct, true)
}

// makeAnswerCompleted is the long-form factory used by the #244
// in-progress test cases. The default makeAnswer keeps IsCompleted=true
// so existing tests don't need to know about the flag.
func makeAnswerCompleted(playerID int64, displayName string, correct, isCompleted bool) *LeaderboardAnswer {
	const window = 10 * time.Second
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	return &LeaderboardAnswer{
		PlayerID:          playerID,
		DisplayName:       displayName,
		QuestionStartedAt: start,
		QuestionExpiredAt: start.Add(window),
		AnsweredAt:        start,
		Correct:           correct,
		IsCompleted:       isCompleted,
	}
}

func TestService_GetQuizLeaderboard(t *testing.T) {
	t.Parallel()

	t.Run("returns 404 when quiz not found", func(t *testing.T) {
		t.Parallel()

		svc := NewService(stubStore{}, stubQuizStore{
			quizExists: func(_ context.Context, _ int64) (bool, error) {
				return false, nil
			},
		}, slog.New(slog.DiscardHandler))

		_, err := svc.GetQuizLeaderboard(t.Context(), 1, 0, 10)
		if got, want := err, quiz.ErrQuizNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("returns empty slice when no games", func(t *testing.T) {
		t.Parallel()

		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					return nil, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		got, err := svc.GetQuizLeaderboard(t.Context(), 1, 0, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Entries) != 0 {
			t.Errorf("len(entries) = %d, want 0", len(got.Entries))
		}
		if got.CurrentPlayer != nil {
			t.Errorf("CurrentPlayer = %+v, want nil", got.CurrentPlayer)
		}
	})

	t.Run("sums a single player's answers", func(t *testing.T) {
		t.Parallel()

		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					// Two correct answers + one wrong -> 1000 + 1000 + 0 = 2000.
					return []*LeaderboardAnswer{
						makeAnswer(1, "alice", true),
						makeAnswer(1, "alice", true),
						makeAnswer(1, "alice", false),
					}, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		result, err := svc.GetQuizLeaderboard(t.Context(), 1, 0, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := len(result.Entries), 1; got != want {
			t.Fatalf("len(entries) = %d, want %d", got, want)
		}
		if got, want := result.Entries[0].PlayerID, int64(1); got != want {
			t.Errorf("entries[0].PlayerID = %d, want %d", got, want)
		}
		if got, want := result.Entries[0].DisplayName, "alice"; got != want {
			t.Errorf("entries[0].DisplayName = %q, want %q", got, want)
		}
		if got, want := result.Entries[0].Score, 2000; got != want {
			t.Errorf("entries[0].Score = %d, want %d", got, want)
		}
		if got, want := result.Entries[0].Rank, 1; got != want {
			t.Errorf("entries[0].Rank = %d, want %d", got, want)
		}
	})

	t.Run("participant with no answers appears with score 0 and Completed=false", func(t *testing.T) {
		t.Parallel()

		// #335: alice has clicked Start but not submitted an answer
		// yet; bob has answered two correct questions. Both must
		// appear, bob ranked first with 2000 and alice ranked second
		// with 0 - and alice's entry must carry Completed=false so
		// the client renders the in-progress dot.
		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					return []*LeaderboardAnswer{
						makeAnswerCompleted(2, "bob", true, false),
						makeAnswerCompleted(2, "bob", true, false),
					}, nil
				},
				listParticipantsForQuizLeaderboard: func(_ context.Context, _ int64, _ time.Time) ([]*LeaderboardParticipant, error) {
					return []*LeaderboardParticipant{
						{PlayerID: 1, DisplayName: "alice", IsCompleted: false},
						{PlayerID: 2, DisplayName: "bob", IsCompleted: false},
					}, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		result, err := svc.GetQuizLeaderboard(t.Context(), 1, 0, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := len(result.Entries), 2; got != want {
			t.Fatalf("len(entries) = %d, want %d", got, want)
		}
		if got, want := result.Entries[0].DisplayName, "bob"; got != want {
			t.Errorf("entries[0].DisplayName = %q, want %q", got, want)
		}
		if got, want := result.Entries[0].Score, 2000; got != want {
			t.Errorf("entries[0].Score = %d, want %d", got, want)
		}
		if got, want := result.Entries[0].Rank, 1; got != want {
			t.Errorf("entries[0].Rank = %d, want %d", got, want)
		}
		if got, want := result.Entries[0].Completed, false; got != want {
			t.Errorf("entries[0].Completed = %v, want %v (bob's participant row has IsCompleted=false)", got, want)
		}
		if got, want := result.Entries[1].DisplayName, "alice"; got != want {
			t.Errorf("entries[1].DisplayName = %q, want %q", got, want)
		}
		if got, want := result.Entries[1].Score, 0; got != want {
			t.Errorf("entries[1].Score = %d, want %d (no-answer participant)", got, want)
		}
		if got, want := result.Entries[1].Rank, 2; got != want {
			t.Errorf("entries[1].Rank = %d, want %d", got, want)
		}
		if got, want := result.Entries[1].Completed, false; got != want {
			t.Errorf("entries[1].Completed = %v, want %v (no-answer participants are in-progress)", got, want)
		}
	})

	t.Run("in-progress player is counted with partial score and Completed=false", func(t *testing.T) {
		t.Parallel()

		// #244: a mid-quiz player's rows arrive with IsCompleted=false;
		// the service must still aggregate them into a leaderboard
		// entry, stamp Completed=false on the entry, and keep the
		// running partial score.
		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					return []*LeaderboardAnswer{
						makeAnswerCompleted(1, "alice", true, false),
						makeAnswerCompleted(1, "alice", true, false),
					}, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		result, err := svc.GetQuizLeaderboard(t.Context(), 1, 0, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := len(result.Entries), 1; got != want {
			t.Fatalf("len(entries) = %d, want %d", got, want)
		}
		if got, want := result.Entries[0].Score, 2000; got != want {
			t.Errorf("entries[0].Score = %d, want %d (partial total)", got, want)
		}
		if got, want := result.Entries[0].Completed, false; got != want {
			t.Errorf("entries[0].Completed = %v, want %v", got, want)
		}
	})

	t.Run("sorts two players by score descending", func(t *testing.T) {
		t.Parallel()

		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					return []*LeaderboardAnswer{
						// alice: 1000.
						makeAnswer(1, "alice", true),
						// bob: 2000.
						makeAnswer(2, "bob", true),
						makeAnswer(2, "bob", true),
					}, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		result, err := svc.GetQuizLeaderboard(t.Context(), 1, 0, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := len(result.Entries), 2; got != want {
			t.Fatalf("len(entries) = %d, want %d", got, want)
		}
		if got, want := result.Entries[0].DisplayName, "bob"; got != want {
			t.Errorf("entries[0].DisplayName = %q, want %q", got, want)
		}
		if got, want := result.Entries[1].DisplayName, "alice"; got != want {
			t.Errorf("entries[1].DisplayName = %q, want %q", got, want)
		}
		if got, want := result.Entries[0].Rank, 1; got != want {
			t.Errorf("entries[0].Rank = %d, want %d", got, want)
		}
		if got, want := result.Entries[1].Rank, 2; got != want {
			t.Errorf("entries[1].Rank = %d, want %d", got, want)
		}
	})

	t.Run("breaks ties by ascending displayName", func(t *testing.T) {
		t.Parallel()

		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					// Three players with identical 1000-point runs but
					// displayNames intentionally out of order.
					return []*LeaderboardAnswer{
						makeAnswer(1, "charlie", true),
						makeAnswer(2, "alice", true),
						makeAnswer(3, "bob", true),
					}, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		result, err := svc.GetQuizLeaderboard(t.Context(), 1, 0, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gotNames := []string{
			result.Entries[0].DisplayName,
			result.Entries[1].DisplayName,
			result.Entries[2].DisplayName,
		}
		wantNames := []string{"alice", "bob", "charlie"}
		if diff := cmp.Diff(wantNames, gotNames); diff != "" {
			t.Errorf("displayName order mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("flags the entry of the current player", func(t *testing.T) {
		t.Parallel()

		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					return []*LeaderboardAnswer{
						makeAnswer(1, "alice", true),
						makeAnswer(2, "bob", true),
						makeAnswer(2, "bob", true),
					}, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		result, err := svc.GetQuizLeaderboard(t.Context(), 1, 1, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var aliceEntry, bobEntry LeaderboardEntry
		for _, e := range result.Entries {
			switch e.PlayerID {
			case 1:
				aliceEntry = e
			case 2:
				bobEntry = e
			default:
				t.Fatalf("unexpected player ID %d", e.PlayerID)
			}
		}

		if got, want := aliceEntry.IsCurrentPlayer, true; got != want {
			t.Errorf("aliceEntry.IsCurrentPlayer = %v, want %v", got, want)
		}
		if got, want := bobEntry.IsCurrentPlayer, false; got != want {
			t.Errorf("bobEntry.IsCurrentPlayer = %v, want %v", got, want)
		}
		// CurrentPlayer is also surfaced separately so off-leaderboard
		// players can see their own row (#181). Alice is in the top-N
		// here so it duplicates her entry, but the field is populated
		// either way.
		if result.CurrentPlayer == nil {
			t.Fatal("CurrentPlayer = nil, want alice's standing")
		}
		if got, want := result.CurrentPlayer.PlayerID, int64(1); got != want {
			t.Errorf("CurrentPlayer.PlayerID = %d, want %d", got, want)
		}
		if got, want := result.CurrentPlayer.Rank, 2; got != want {
			t.Errorf("CurrentPlayer.Rank = %d, want %d (alice trails bob)", got, want)
		}
	})

	t.Run("truncates results to the supplied limit", func(t *testing.T) {
		t.Parallel()

		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					answers := make([]*LeaderboardAnswer, 0, 5)
					for i := int64(1); i <= 5; i++ {
						// Player i answers (5-i+1) correct questions so
						// scores are strictly decreasing: 5000, 4000, 3000, 2000, 1000.
						for range 6 - i {
							answers = append(answers, makeAnswer(i, "p", true))
						}
					}

					return answers, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		result, err := svc.GetQuizLeaderboard(t.Context(), 1, 0, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := len(result.Entries), 3; got != want {
			t.Errorf("len(entries) = %d, want %d", got, want)
		}
		if got, want := result.Entries[0].PlayerID, int64(1); got != want {
			t.Errorf("entries[0].PlayerID = %d, want %d", got, want)
		}
	})

	t.Run("defaults limit to 10 when limit <= 0", func(t *testing.T) {
		t.Parallel()

		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					answers := make([]*LeaderboardAnswer, 0, 15)
					for i := int64(1); i <= 15; i++ {
						answers = append(answers, makeAnswer(i, "p", true))
					}

					return answers, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		result, err := svc.GetQuizLeaderboard(t.Context(), 1, 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := len(result.Entries), 10; got != want {
			t.Errorf("len(entries) = %d, want %d (default limit)", got, want)
		}
	})

	t.Run("surfaces CurrentPlayer when current player is outside the top-N", func(t *testing.T) {
		t.Parallel()

		// Five players, strictly decreasing scores (5000, 4000, 3000,
		// 2000, 1000). Limit to top-3. Player 5 (lowest score) is the
		// requesting player - they should NOT appear in Entries but
		// SHOULD appear in CurrentPlayer with Rank=5. This is the
		// scenario from #181.
		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					answers := make([]*LeaderboardAnswer, 0, 5)
					for i := int64(1); i <= 5; i++ {
						for range 6 - i {
							answers = append(answers, makeAnswer(i, fmt.Sprintf("p%d", i), true))
						}
					}

					return answers, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		result, err := svc.GetQuizLeaderboard(t.Context(), 1, 5, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := len(result.Entries), 3; got != want {
			t.Fatalf("len(entries) = %d, want %d", got, want)
		}
		for _, e := range result.Entries {
			if e.PlayerID == 5 {
				t.Errorf("player 5 should be outside the top-3, found in entries: %+v", e)
			}
		}
		if result.CurrentPlayer == nil {
			t.Fatal("CurrentPlayer = nil, want player 5's standing")
		}
		if got, want := result.CurrentPlayer.PlayerID, int64(5); got != want {
			t.Errorf("CurrentPlayer.PlayerID = %d, want %d", got, want)
		}
		if got, want := result.CurrentPlayer.Rank, 5; got != want {
			t.Errorf("CurrentPlayer.Rank = %d, want %d", got, want)
		}
		if got, want := result.CurrentPlayer.Score, 1000; got != want {
			t.Errorf("CurrentPlayer.Score = %d, want %d", got, want)
		}
		if got, want := result.CurrentPlayer.IsCurrentPlayer, true; got != want {
			t.Errorf("CurrentPlayer.IsCurrentPlayer = %v, want %v", got, want)
		}
	})

	t.Run("CurrentPlayer is nil when current player has no row", func(t *testing.T) {
		t.Parallel()

		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					return []*LeaderboardAnswer{
						makeAnswer(1, "alice", true),
					}, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		// Request as player 99, who never played.
		result, err := svc.GetQuizLeaderboard(t.Context(), 1, 99, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.CurrentPlayer != nil {
			t.Errorf("CurrentPlayer = %+v, want nil (player has no row)", result.CurrentPlayer)
		}
	})

	t.Run("stale participant has InProgress=false but stays on the board (#336)", func(t *testing.T) {
		t.Parallel()

		// Three participants: alice (active, mid-quiz), bob (completed),
		// carol (stale: not completed but flagged abandoned by the
		// store). The wire-shape consumer renders the live dot from
		// InProgress, so carol must drop the dot even though her game
		// is technically still open.
		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					return nil, nil
				},
				listParticipantsForQuizLeaderboard: func(_ context.Context, _ int64, _ time.Time) ([]*LeaderboardParticipant, error) {
					return []*LeaderboardParticipant{
						{PlayerID: 1, DisplayName: "alice", IsCompleted: false, IsStale: false},
						{PlayerID: 2, DisplayName: "bob", IsCompleted: true, IsStale: false},
						{PlayerID: 3, DisplayName: "carol", IsCompleted: false, IsStale: true},
					}, nil
				},
			},
			stubQuizStore{
				quizExists: func(_ context.Context, _ int64) (bool, error) {
					return true, nil
				},
			},
			slog.New(slog.DiscardHandler),
		)

		result, err := svc.GetQuizLeaderboard(t.Context(), 1, 0, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := len(result.Entries), 3; got != want {
			t.Fatalf("len(entries) = %d, want %d (stale participants stay on the board)", got, want)
		}

		byName := make(map[string]LeaderboardEntry, len(result.Entries))
		for _, e := range result.Entries {
			byName[e.DisplayName] = e
		}

		alice := byName["alice"]
		if got, want := alice.InProgress, true; got != want {
			t.Errorf("alice.InProgress = %v, want %v (active mid-quiz)", got, want)
		}
		if got, want := alice.Completed, false; got != want {
			t.Errorf("alice.Completed = %v, want %v", got, want)
		}

		bob := byName["bob"]
		if got, want := bob.InProgress, false; got != want {
			t.Errorf("bob.InProgress = %v, want %v (completed)", got, want)
		}
		if got, want := bob.Completed, true; got != want {
			t.Errorf("bob.Completed = %v, want %v", got, want)
		}

		carol := byName["carol"]
		if got, want := carol.InProgress, false; got != want {
			t.Errorf("carol.InProgress = %v, want %v (stale: abandoned mid-quiz)", got, want)
		}
		if got, want := carol.Completed, false; got != want {
			t.Errorf("carol.Completed = %v, want %v (stale != completed)", got, want)
		}
	})
}

func TestNextRoundSlot(t *testing.T) {
	t.Parallel()

	// Two rounds: round 1 has a summary (so it shows both boundary
	// phases), round 2 has none (so it shows neither).
	rounds := []*quiz.Round{
		{ID: 10, Position: 1, Title: "First", Summary: "recap one"},
		{ID: 20, Position: 2, Title: "Second", Summary: ""},
	}
	questions := []*quiz.Question{
		{ID: 1, RoundID: 10},
		{ID: 2, RoundID: 10},
		{ID: 3, RoundID: 20},
	}

	t.Run("intro before the round's first question", func(t *testing.T) {
		t.Parallel()

		slot := ExportNextRoundSlot(rounds, questions, map[int64]bool{}, nil)
		if got, want := slot.Kind, ExportSlotKindRoundBoundary; got != want {
			t.Fatalf("Kind = %q, want %q", got, want)
		}
		if got, want := slot.Phase, RoundPhaseIntro; got != want {
			t.Errorf("Phase = %q, want %q", got, want)
		}
		if got, want := slot.Round.ID, int64(10); got != want {
			t.Errorf("Round.ID = %d, want %d", got, want)
		}
	})

	t.Run("intro suppressed once seen yields the first question", func(t *testing.T) {
		t.Parallel()

		seen := []SeenRoundPhase{{RoundID: 10, Phase: RoundPhaseIntro}}
		slot := ExportNextRoundSlot(rounds, questions, map[int64]bool{}, seen)
		if got, want := slot.Kind, ExportSlotKindQuestion; got != want {
			t.Fatalf("Kind = %q, want %q", got, want)
		}
		if got, want := slot.Question.ID, int64(1); got != want {
			t.Errorf("Question.ID = %d, want %d", got, want)
		}
	})

	t.Run("results after the round's questions are asked", func(t *testing.T) {
		t.Parallel()

		asked := map[int64]bool{1: true, 2: true}
		seen := []SeenRoundPhase{{RoundID: 10, Phase: RoundPhaseIntro}}
		slot := ExportNextRoundSlot(rounds, questions, asked, seen)
		if got, want := slot.Kind, ExportSlotKindRoundBoundary; got != want {
			t.Fatalf("Kind = %q, want %q", got, want)
		}
		if got, want := slot.Phase, RoundPhaseResults; got != want {
			t.Errorf("Phase = %q, want %q", got, want)
		}
		if got, want := slot.Round.ID, int64(10); got != want {
			t.Errorf("Round.ID = %d, want %d", got, want)
		}
	})

	t.Run("results suppressed once seen advances to the next round", func(t *testing.T) {
		t.Parallel()

		asked := map[int64]bool{1: true, 2: true}
		seen := []SeenRoundPhase{
			{RoundID: 10, Phase: RoundPhaseIntro},
			{RoundID: 10, Phase: RoundPhaseResults},
		}
		// Round 2 has no summary, so its only slot is its question.
		slot := ExportNextRoundSlot(rounds, questions, asked, seen)
		if got, want := slot.Kind, ExportSlotKindQuestion; got != want {
			t.Fatalf("Kind = %q, want %q", got, want)
		}
		if got, want := slot.Question.ID, int64(3); got != want {
			t.Errorf("Question.ID = %d, want %d", got, want)
		}
	})

	t.Run("round without a summary emits neither boundary", func(t *testing.T) {
		t.Parallel()

		// Only round 2 (no summary) and its question; once its question
		// is asked the walk ends with no boundary.
		soloRounds := []*quiz.Round{{ID: 20, Position: 1, Title: "Solo", Summary: ""}}
		soloQuestions := []*quiz.Question{{ID: 3, RoundID: 20}}

		before := ExportNextRoundSlot(soloRounds, soloQuestions, map[int64]bool{}, nil)
		if got, want := before.Kind, ExportSlotKindQuestion; got != want {
			t.Fatalf("before Kind = %q, want %q", got, want)
		}

		after := ExportNextRoundSlot(soloRounds, soloQuestions, map[int64]bool{3: true}, nil)
		if got, want := after.Kind, ""; got != want {
			t.Errorf("after Kind = %q, want %q (no boundary)", got, want)
		}
	})
}
