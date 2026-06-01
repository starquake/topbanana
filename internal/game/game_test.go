package game_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"

	"github.com/starquake/topbanana/internal/dbtest"
	. "github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
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
func (stubStore) CreateGame(_ context.Context, _ *Game) error { return errStub }
func (stubStore) CreateGameAndParticipant(_ context.Context, _ *Game, _ *Participant) error {
	return errStub
}
func (stubStore) StartGame(_ context.Context, _ string) error               { return errStub }
func (stubStore) CreateParticipant(_ context.Context, _ *Participant) error { return errStub }
func (stubStore) CreateQuestion(_ context.Context, _ *Question) error       { return errStub }
func (stubStore) CreateAnswer(_ context.Context, _ *Answer) error           { return errStub }

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
func (stubQuizStore) ListPublicQuizzes(_ context.Context) ([]*quiz.Quiz, error) {
	return nil, errStub
}

func (stubQuizStore) QuestionCountsByQuiz(_ context.Context) (map[int64]int, error) {
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
func (stubQuizStore) CreateQuiz(_ context.Context, _ *quiz.Quiz) error         { return errStub }
func (stubQuizStore) UpdateQuiz(_ context.Context, _ *quiz.Quiz) error         { return errStub }
func (stubQuizStore) DeleteQuiz(_ context.Context, _ int64) error              { return errStub }
func (stubQuizStore) CreateQuestion(_ context.Context, _ *quiz.Question) error { return errStub }
func (stubQuizStore) CreateQuestionAtNextPosition(_ context.Context, _ *quiz.Question) error {
	return errStub
}
func (stubQuizStore) UpdateQuestion(_ context.Context, _ *quiz.Question) error { return errStub }

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

// seededAdminID is the id of the admin row inserted by migration
// 20260111110308_add_admin_player.sql. Quiz fixtures attribute
// themselves to this admin so the NOT NULL created_by_player_id
// column from migration 20260520200000 (#281) is satisfied.
const seededAdminID int64 = 1

func newTestQuiz(t *testing.T) *quiz.Quiz {
	t.Helper()

	return &quiz.Quiz{
		Title:             "Flurpsydurpsy",
		Slug:              "flurpsydurpsy",
		CreatedByPlayerID: seededAdminID,
		Questions: []*quiz.Question{
			{
				Text:     "What is the capital of France?",
				Position: 10,
				Options: []*quiz.Option{
					{Text: "Paris", Correct: true},
					{Text: "London"},
				},
			},
			{
				Text:     "What is the capital of Germany?",
				Position: 20,
				Options: []*quiz.Option{
					{Text: "Berlin", Correct: true},
					{Text: "Hamburg"},
				},
			},
			{
				Text:     "What is the capital of Spain?",
				Position: 30,
				Options: []*quiz.Option{
					{Text: "Madrid", Correct: true},
					{Text: "Barcelona"},
				},
			},
		},
	}
}

// assertBoundaryWindow pins the #548 auto-advance contract at the
// service layer: a round-boundary item carries a non-zero
// StartedAt/ExpiredAt window exactly one quiz-default answer duration
// (timeLimitSeconds) long, for both phases.
func assertBoundaryWindow(t *testing.T, item *Item, timeLimitSeconds int) {
	t.Helper()
	if item.StartedAt.IsZero() {
		t.Error("item.StartedAt is zero, want a populated timestamp")
	}
	if item.ExpiredAt.IsZero() {
		t.Error("item.ExpiredAt is zero, want a populated timestamp")
	}
	want := time.Duration(timeLimitSeconds) * time.Second
	if got := item.ExpiredAt.Sub(item.StartedAt); got != want {
		t.Errorf("item window ExpiredAt-StartedAt = %v, want %v (quiz default)", got, want)
	}
}

func newTestGame(t *testing.T, qz *quiz.Quiz) *Game {
	t.Helper()

	return &Game{
		QuizID: qz.ID,
	}
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

func TestService_GetGameForPlayerOnQuiz(t *testing.T) {
	t.Parallel()

	t.Run("returns existing game with quiz populated", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		const playerID = int64(1)
		created, err := svc.CreateGame(ctx, testQuiz.ID, playerID)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		resumed, err := svc.GetGameForPlayerOnQuiz(ctx, playerID, testQuiz.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := resumed.ID, created.ID; got != want {
			t.Errorf("resumed.ID = %q, want %q", got, want)
		}
		if resumed.Quiz == nil {
			t.Fatal("resumed.Quiz is nil, want populated quiz")
		}
		if got, want := resumed.Quiz.ID, testQuiz.ID; got != want {
			t.Errorf("resumed.Quiz.ID = %d, want %d", got, want)
		}
		// IsCompleted should work because Quiz is populated.
		if got, want := resumed.IsCompleted(), false; got != want {
			t.Errorf("IsCompleted() = %v, want %v (no questions issued yet)", got, want)
		}
	})

	t.Run("returns ErrGameNotFound when player has no game", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		_, err := svc.GetGameForPlayerOnQuiz(ctx, 999, testQuiz.ID)
		if got, want := err, ErrGameNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("returns ErrQuizNotFound when quiz missing", func(t *testing.T) {
		t.Parallel()

		svc := NewService(stubStore{}, stubQuizStore{
			getQuiz: func(_ context.Context, _ int64) (*quiz.Quiz, error) {
				return nil, quiz.ErrQuizNotFound
			},
		}, slog.New(slog.DiscardHandler))

		_, err := svc.GetGameForPlayerOnQuiz(t.Context(), 1, 999)
		if got, want := err, quiz.ErrQuizNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestService_CreateGame_RejectsSecondAttempt(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)

	quizStore := store.NewQuizStore(db, slog.Default())
	gameStore := store.NewGameStore(db, slog.Default())

	testQuiz := newTestQuiz(t)
	if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
		t.Fatalf("failed to create quiz: %v", err)
	}

	svc := NewService(gameStore, quizStore, slog.Default())

	const playerID = int64(1)
	if _, err := svc.CreateGame(ctx, testQuiz.ID, playerID); err != nil {
		t.Fatalf("failed to create initial game: %v", err)
	}

	// Second attempt for the same (player, quiz) must be rejected.
	_, err := svc.CreateGame(ctx, testQuiz.ID, playerID)
	if got, want := err, ErrGameAlreadyExists; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestService_ResetGamesForPlayerOnQuiz(t *testing.T) {
	t.Parallel()

	t.Run("reset clears existing game and lets a fresh CreateGame succeed", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		const playerID = int64(1)
		if _, err := svc.CreateGame(ctx, testQuiz.ID, playerID); err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		// Sanity check: GetGameForPlayerOnQuiz finds the game first.
		if _, err := svc.GetGameForPlayerOnQuiz(ctx, playerID, testQuiz.ID); err != nil {
			t.Fatalf("expected game to exist before reset: %v", err)
		}

		if err := svc.ResetGamesForPlayerOnQuiz(ctx, playerID, testQuiz.ID); err != nil {
			t.Fatalf("ResetGamesForPlayerOnQuiz err = %v, want nil", err)
		}

		_, err := svc.GetGameForPlayerOnQuiz(ctx, playerID, testQuiz.ID)
		if got, want := err, ErrGameNotFound; !errors.Is(got, want) {
			t.Errorf("after reset, err = %v, want %v", got, want)
		}

		if _, err = svc.CreateGame(ctx, testQuiz.ID, playerID); err != nil {
			t.Errorf("CreateGame after reset err = %v, want nil", err)
		}
	})

	t.Run("idempotent - calling reset twice is fine", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		const playerID = int64(1)
		if _, err := svc.CreateGame(ctx, testQuiz.ID, playerID); err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		if err := svc.ResetGamesForPlayerOnQuiz(ctx, playerID, testQuiz.ID); err != nil {
			t.Fatalf("first reset err = %v, want nil", err)
		}
		if err := svc.ResetGamesForPlayerOnQuiz(ctx, playerID, testQuiz.ID); err != nil {
			t.Errorf("second reset err = %v, want nil (reset must be idempotent)", err)
		}
	})

	t.Run("returns ErrQuizNotFound when quiz missing", func(t *testing.T) {
		t.Parallel()

		svc := NewService(stubStore{}, stubQuizStore{
			quizExists: func(_ context.Context, _ int64) (bool, error) {
				return false, nil
			},
		}, slog.New(slog.DiscardHandler))

		err := svc.ResetGamesForPlayerOnQuiz(t.Context(), 1, 999)
		if got, want := err, quiz.ErrQuizNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestService_SubmitAnswer(t *testing.T) {
	t.Parallel()

	t.Run("rejects option from a different question", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		g, err := svc.CreateGame(ctx, testQuiz.ID, 1)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		gq, err := svc.GetNextQuestion(ctx, g.ID, 1)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		// Use a correct option from the second question (different from the active question).
		wrongQuestionOption := testQuiz.Questions[1].Options[0] // Berlin, Correct: true, but for question 2

		_, err = svc.SubmitAnswer(ctx, g.ID, 1, gq.QuizQuestion.ID, wrongQuestionOption.ID, time.Time{})
		if got, want := err, ErrOptionNotInQuestion; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("accepts option belonging to the active question", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		g, err := svc.CreateGame(ctx, testQuiz.ID, 1)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		gq, err := svc.GetNextQuestion(ctx, g.ID, 1)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		correctOption := testQuiz.Questions[0].Options[0] // Paris, Correct: true

		_, err = svc.SubmitAnswer(ctx, g.ID, 1, gq.QuizQuestion.ID, correctOption.ID, time.Time{})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestService_GetResults(t *testing.T) {
	t.Parallel()

	t.Run("returns player with highest score as winner", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		g, err := svc.CreateGame(ctx, testQuiz.ID, 1)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		gq, err := svc.GetNextQuestion(ctx, g.ID, 1)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		insertPlayer2 := `INSERT INTO players (id, display_name, email, created_at) VALUES (2, 'player2', 'player2@test.com', CURRENT_TIMESTAMP)`
		if _, err = db.ExecContext(ctx, insertPlayer2); err != nil {
			t.Fatalf("failed to insert player 2: %v", err)
		}
		// Participant gate (#272): player 2 needs an explicit
		// participant row, otherwise SubmitAnswer rejects them as a
		// non-participant. The bug-fix for #272 made the gate strict;
		// pre-fix this test inadvertently relied on the missing check.
		if err = gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: g.ID, PlayerID: 2, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("failed to create participant for player 2: %v", err)
		}

		correctOption := testQuiz.Questions[0].Options[0] // Paris, Correct: true
		wrongOption := testQuiz.Questions[0].Options[1]   // London, Correct: false

		if _, err = svc.SubmitAnswer(ctx, g.ID, 1, gq.QuizQuestion.ID, correctOption.ID, time.Time{}); err != nil {
			t.Fatalf("failed to submit answer for player 1: %v", err)
		}
		if _, err = svc.SubmitAnswer(ctx, g.ID, 2, gq.QuizQuestion.ID, wrongOption.ID, time.Time{}); err != nil {
			t.Fatalf("failed to submit answer for player 2: %v", err)
		}

		results, err := svc.GetResults(ctx, g.ID, 1)
		if err != nil {
			t.Fatalf("failed to get results: %v", err)
		}

		if got, want := results.Winner, int64(1); got != want {
			t.Errorf("Winner = %v, want %v", got, want)
		}
	})

	t.Run("returns no winner on tie", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		g, err := svc.CreateGame(ctx, testQuiz.ID, 1)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		gq, err := svc.GetNextQuestion(ctx, g.ID, 1)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		insertPlayer2 := `INSERT INTO players (id, display_name, email, created_at) VALUES (2, 'player2', 'player2@test.com', CURRENT_TIMESTAMP)`
		if _, err = db.ExecContext(ctx, insertPlayer2); err != nil {
			t.Fatalf("failed to insert player 2: %v", err)
		}
		// Participant gate (#272): player 2 needs an explicit
		// participant row, otherwise SubmitAnswer rejects them as a
		// non-participant. The bug-fix for #272 made the gate strict;
		// pre-fix this test inadvertently relied on the missing check.
		if err = gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: g.ID, PlayerID: 2, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("failed to create participant for player 2: %v", err)
		}

		wrongOption := testQuiz.Questions[0].Options[1] // London, Correct: false

		if _, err = svc.SubmitAnswer(ctx, g.ID, 1, gq.QuizQuestion.ID, wrongOption.ID, time.Time{}); err != nil {
			t.Fatalf("failed to submit answer for player 1: %v", err)
		}
		if _, err = svc.SubmitAnswer(ctx, g.ID, 2, gq.QuizQuestion.ID, wrongOption.ID, time.Time{}); err != nil {
			t.Fatalf("failed to submit answer for player 2: %v", err)
		}

		results, err := svc.GetResults(ctx, g.ID, 1)
		if err != nil {
			t.Fatalf("failed to get results: %v", err)
		}

		if got, want := results.Winner, int64(0); got != want {
			t.Errorf("Winner = %v, want %v (expected no winner on tie)", got, want)
		}
	})

	t.Run("skips answers whose option was deleted without panicking", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()

		// A finished game with one answer that references option 99,
		// which GetOptionsByIDs no longer returns (the option row was
		// deleted out from under the answer). The score loop must skip
		// it rather than dereference a nil Option.
		gameWithDanglingAnswer := &Game{
			ID:           "game-deleted-opt",
			Participants: []*Participant{{PlayerID: 1}},
			Questions: []*Question{
				{
					ID: 1,
					Answers: []*Answer{
						{PlayerID: 1, OptionID: 99, AnsweredAt: time.Now()},
					},
				},
			},
		}

		gs := stubStore{
			getGame: func(_ context.Context, _ string) (*Game, error) {
				return gameWithDanglingAnswer, nil
			},
		}
		qs := stubQuizStore{
			getOptionsByIDs: func(_ context.Context, _ []int64) ([]*quiz.Option, error) {
				return nil, nil
			},
		}

		svc := NewService(gs, qs, slog.Default())

		results, err := svc.GetResults(ctx, "game-deleted-opt", 1)
		if err != nil {
			t.Fatalf("GetResults err = %v, want nil", err)
		}
		if got, want := results.PlayerScores[1], 0; got != want {
			t.Errorf("PlayerScores[1] = %d, want %d", got, want)
		}
	})
}

func TestService_GetNextQuestion(t *testing.T) {
	t.Parallel()

	t.Run("first question", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()

		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)

		var err error
		err = quizStore.CreateQuiz(ctx, testQuiz)
		if err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		testGame := newTestGame(t, testQuiz)
		err = gameStore.CreateGame(ctx, testGame)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}
		// Participant gate (#272): the service rejects callers that
		// aren't on the participant list. These tests bypass the
		// service's CreateGame, so the participant row has to be
		// seeded explicitly here.
		if err = gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("failed to create participant: %v", err)
		}

		service := NewService(gameStore, quizStore, slog.Default())
		gq, err := service.GetNextQuestion(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		if gq == nil {
			t.Fatal("expected gq to be non-nil")

			return
		}

		if cmp.Diff(gq.QuizQuestion, testQuiz.Questions[0]) != "" {
			t.Errorf("got qs: %+v, want %+v", gq.QuizQuestion, testQuiz.Questions[0])
		}
	})

	t.Run("second question", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()

		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)

		var err error
		err = quizStore.CreateQuiz(ctx, testQuiz)
		if err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		testGame := newTestGame(t, testQuiz)
		err = gameStore.CreateGame(ctx, testGame)
		if err != nil {
			t.Fatalf("failed to create game: %v", err)
		}
		// Participant gate (#272): the service rejects callers that
		// aren't on the participant list. These tests bypass the
		// service's CreateGame, so the participant row has to be
		// seeded explicitly here.
		if err = gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("failed to create participant: %v", err)
		}

		err = gameStore.CreateQuestion(ctx, &Question{GameID: testGame.ID, QuestionID: testQuiz.Questions[0].ID})
		if err != nil {
			t.Fatalf("failed to create game question: %v", err)
		}

		service := NewService(gameStore, quizStore, slog.Default())
		gq, err := service.GetNextQuestion(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("failed to get next question: %v", err)
		}

		if gq == nil {
			t.Fatal("expected gq to be non-nil")

			return
		}

		if cmp.Diff(gq.QuizQuestion, testQuiz.Questions[1]) != "" {
			t.Errorf("got qs: %+v, want %+v", gq.QuizQuestion, testQuiz.Questions[1])
		}
	})

	t.Run("started_at sits in the future to honour the reveal delay", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		// Participant gate (#272): seed the participant directly since
		// these tests bypass Service.CreateGame.
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		service := NewService(gameStore, quizStore, slog.Default())
		issuedAt := time.Now()
		gq, err := service.GetNextQuestion(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("GetNextQuestion err = %v, want nil", err)
		}

		// StartedAt must be at least 2 seconds in the future relative
		// to issuedAt - the 3s reveal delay (#247) gives the player
		// time to read the question before the answer window opens.
		// 2s lower bound is forgiving of clock granularity on the
		// test machine; the production constant is 3s.
		if got, lower := gq.StartedAt.Sub(issuedAt), 2*time.Second; got < lower {
			t.Errorf("StartedAt - issuedAt = %v, want >= %v (reveal delay)", got, lower)
		}
		// ExpiredAt sits one answer window further. The window is 10s
		// (the unexported defaultExpiration constant); duplicated here
		// as a literal so this external_test file doesn't have to
		// reach into package internals.
		if got, want := gq.ExpiredAt.Sub(gq.StartedAt), 10*time.Second; got != want {
			t.Errorf("ExpiredAt - StartedAt = %v, want %v", got, want)
		}
	})

	t.Run("back-to-back calls return the same in-flight question", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		// Participant gate (#272): seed the participant directly since
		// these tests bypass Service.CreateGame.
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		service := NewService(gameStore, quizStore, slog.Default())
		first, err := service.GetNextQuestion(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("first GetNextQuestion err = %v, want nil", err)
		}

		// Second call without submitting an answer must return the same
		// game_questions row - same ID, same timing anchors - so a
		// mid-question reload doesn't skip the question.
		second, err := service.GetNextQuestion(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("second GetNextQuestion err = %v, want nil", err)
		}
		if got, want := second.ID, first.ID; got != want {
			t.Errorf("second.ID = %d, want %d (resume must hand back same row)", got, want)
		}
		if got, want := second.QuizQuestion.ID, first.QuizQuestion.ID; got != want {
			t.Errorf("second.QuizQuestion.ID = %d, want %d", got, want)
		}
		if got, want := second.StartedAt, first.StartedAt; !got.Equal(want) {
			t.Errorf("second.StartedAt = %v, want %v (timing anchor must not reset)", got, want)
		}
		if got, want := second.ExpiredAt, first.ExpiredAt; !got.Equal(want) {
			t.Errorf("second.ExpiredAt = %v, want %v", got, want)
		}

		// And no extra game_questions row was inserted.
		g, err := gameStore.GetGame(ctx, testGame.ID)
		if err != nil {
			t.Fatalf("GetGame err = %v, want nil", err)
		}
		if got, want := len(g.Questions), 1; got != want {
			t.Errorf("game_questions count = %d, want %d (resume must not insert)", got, want)
		}
	})

	t.Run("advance after an expired unanswered question", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		// Seed an unanswered game_question whose answer window has
		// already closed - the timeout path leaves rows like this.
		// The advance branch must move past it instead of pinning the
		// player on the expired question.
		past := time.Now().Add(-1 * time.Minute)
		expired := &Question{
			GameID:     testGame.ID,
			QuestionID: testQuiz.Questions[0].ID,
			StartedAt:  past,
			ExpiredAt:  past.Add(10 * time.Second),
		}
		if err := gameStore.CreateQuestion(ctx, expired); err != nil {
			t.Fatalf("CreateQuestion err = %v, want nil", err)
		}

		service := NewService(gameStore, quizStore, slog.Default())
		gq, err := service.GetNextQuestion(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("GetNextQuestion err = %v, want nil", err)
		}
		if got, want := gq.QuizQuestion.ID, testQuiz.Questions[1].ID; got != want {
			t.Errorf("advanced to QuizQuestion.ID = %d, want %d (expired Q1 must not pin)", got, want)
		}
	})

	t.Run("SetRevealDelay shrinks the reveal-to-answer gap", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		// Participant gate (#272): seed the participant directly since
		// these tests bypass Service.CreateGame.
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		// Sub-second reveal mirrors the e2e config: shorter than the
		// default 3s but still leaves the reveal phase observable.
		service := NewService(gameStore, quizStore, slog.Default())
		service.SetRevealDelay(200 * time.Millisecond)
		issuedAt := time.Now()
		gq, err := service.GetNextQuestion(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("GetNextQuestion err = %v, want nil", err)
		}

		// StartedAt should sit close to (issuedAt + 200ms). Generous
		// upper bound to absorb scheduler jitter on busy CI runners.
		if got, upper := gq.StartedAt.Sub(issuedAt), 1*time.Second; got >= upper {
			t.Errorf("StartedAt - issuedAt = %v, want < %v (override should shrink reveal)", got, upper)
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
func makeAnswer(playerID int64, username string, correct bool) *LeaderboardAnswer {
	return makeAnswerCompleted(playerID, username, correct, true)
}

// makeAnswerCompleted is the long-form factory used by the #244
// in-progress test cases. The default makeAnswer keeps IsCompleted=true
// so existing tests don't need to know about the flag.
func makeAnswerCompleted(playerID int64, username string, correct, isCompleted bool) *LeaderboardAnswer {
	const window = 10 * time.Second
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	return &LeaderboardAnswer{
		PlayerID:          playerID,
		DisplayName:       username,
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

	t.Run("breaks ties by ascending username", func(t *testing.T) {
		t.Parallel()

		svc := NewService(
			stubStore{
				listAnswersForQuizLeaderboard: func(_ context.Context, _ int64) ([]*LeaderboardAnswer, error) {
					// Three players with identical 1000-point runs but
					// usernames intentionally out of order.
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
			t.Errorf("username order mismatch (-want +got):\n%s", diff)
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

// firstRoundID returns the id of the only round a freshly created quiz
// has - the default "Round 1" the store stamps on CreateQuiz (#444).
func firstRoundID(t *testing.T, qz *quiz.Quiz, quizStore *store.QuizStore) int64 {
	t.Helper()
	rounds, err := quizStore.ListRoundsByQuiz(t.Context(), qz.ID)
	if err != nil {
		t.Fatalf("ListRoundsByQuiz err = %v, want nil", err)
	}
	if len(rounds) == 0 {
		t.Fatal("quiz has no rounds, want at least the default")
	}

	return rounds[0].ID
}

// giveRoundSummary stamps a summary on an existing round so its boundary
// fires during play. The play iterator skips a round whose summary is
// empty (#444), so boundary-emission tests must author one first.
func giveRoundSummary(t *testing.T, quizStore *store.QuizStore, roundID int64, summary string) {
	t.Helper()
	round, err := quizStore.GetRound(t.Context(), roundID)
	if err != nil {
		t.Fatalf("GetRound err = %v, want nil", err)
	}
	round.Summary = summary
	if err := quizStore.UpdateRound(t.Context(), round); err != nil {
		t.Fatalf("UpdateRound err = %v, want nil", err)
	}
}

func TestService_GetNext(t *testing.T) {
	t.Parallel()

	t.Run("returns the first question of the first round", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())
		item, err := svc.GetNext(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("GetNext err = %v, want nil", err)
		}
		if got, want := item.Type, ItemTypeQuestion; got != want {
			t.Errorf("item.Type = %q, want %q", got, want)
		}
		if got, want := item.Question.QuizQuestion.ID, testQuiz.Questions[0].ID; got != want {
			t.Errorf("item.Question.QuizQuestion.ID = %d, want %d", got, want)
		}
	})

	t.Run("emits the results boundary after every question in the round is issued", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}
		roundID := firstRoundID(t, testQuiz, quizStore)
		giveRoundSummary(t, quizStore, roundID, "Round one wrapped up")

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		// Issue every question and mark the intro phase seen so the only
		// remaining item is the round's results boundary.
		for _, q := range testQuiz.Questions {
			if err := gameStore.CreateQuestion(
				ctx, &Question{GameID: testGame.ID, QuestionID: q.ID},
			); err != nil {
				t.Fatalf("CreateQuestion err = %v, want nil", err)
			}
		}
		if err := gameStore.MarkRoundSeen(ctx, testGame.ID, roundID, RoundPhaseIntro); err != nil {
			t.Fatalf("MarkRoundSeen (intro) err = %v, want nil", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())
		item, err := svc.GetNext(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("GetNext err = %v, want nil", err)
		}
		if got, want := item.Type, ItemTypeRoundBoundary; got != want {
			t.Errorf("item.Type = %q, want %q", got, want)
		}
		if got, want := item.Phase, RoundPhaseResults; got != want {
			t.Errorf("item.Phase = %q, want %q", got, want)
		}
		if got, want := item.Round.ID, roundID; got != want {
			t.Errorf("item.Round.ID = %d, want %d", got, want)
		}
		if got, want := item.Total, len(testQuiz.Questions); got != want {
			t.Errorf("item.Total = %d, want %d", got, want)
		}
		if got, want := item.RoundQuestions, len(testQuiz.Questions); got != want {
			t.Errorf("item.RoundQuestions = %d, want %d", got, want)
		}
		assertBoundaryWindow(t, item, testQuiz.TimeLimitSeconds)
	})

	t.Run("emits the intro boundary before the round's first question", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}
		roundID := firstRoundID(t, testQuiz, quizStore)
		giveRoundSummary(t, quizStore, roundID, "Round one ahead")

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())
		item, err := svc.GetNext(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("GetNext err = %v, want nil", err)
		}
		if got, want := item.Type, ItemTypeRoundBoundary; got != want {
			t.Errorf("item.Type = %q, want %q", got, want)
		}
		if got, want := item.Phase, RoundPhaseIntro; got != want {
			t.Errorf("item.Phase = %q, want %q", got, want)
		}
		if got, want := item.Round.Summary, "Round one ahead"; got != want {
			t.Errorf("item.Round.Summary = %q, want %q", got, want)
		}
		assertBoundaryWindow(t, item, testQuiz.TimeLimitSeconds)
	})

	t.Run("skips a round boundary the player has already seen", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}
		roundID := firstRoundID(t, testQuiz, quizStore)
		giveRoundSummary(t, quizStore, roundID, "Round one wrapped up")

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}
		for _, q := range testQuiz.Questions {
			if err := gameStore.CreateQuestion(
				ctx, &Question{GameID: testGame.ID, QuestionID: q.ID},
			); err != nil {
				t.Fatalf("CreateQuestion err = %v, want nil", err)
			}
		}
		if err := gameStore.MarkRoundSeen(ctx, testGame.ID, roundID, RoundPhaseIntro); err != nil {
			t.Fatalf("MarkRoundSeen (intro) err = %v, want nil", err)
		}
		if err := gameStore.MarkRoundSeen(ctx, testGame.ID, roundID, RoundPhaseResults); err != nil {
			t.Fatalf("MarkRoundSeen (results) err = %v, want nil", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())
		_, err := svc.GetNext(ctx, testGame.ID, 1)
		if got, want := err, ErrNoMoreQuestions; !errors.Is(got, want) {
			t.Errorf("GetNext err = %v, want %v (all questions issued, both phases seen)", got, want)
		}
	})

	t.Run("walks rounds in position order issuing each round's questions then its boundary", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		// Two rounds: round 1 holds Q1, round 2 holds Q2, each with a
		// summary. The walk should be intro(r1), Q1, results(r1),
		// intro(r2), Q2, results(r2). Each question is answered before
		// advancing so the resume path does not hand the same in-flight
		// question back; each boundary phase is acked so it is not
		// re-emitted.
		testQuiz := &quiz.Quiz{
			Title:             "Two rounds",
			Slug:              "two-rounds",
			CreatedByPlayerID: seededAdminID,
			Questions: []*quiz.Question{
				{Text: "Q1", Position: 10, Options: []*quiz.Option{{Text: "a", Correct: true}, {Text: "b"}}},
				{Text: "Q2", Position: 20, Options: []*quiz.Option{{Text: "c", Correct: true}, {Text: "d"}}},
			},
		}
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}
		round1 := firstRoundID(t, testQuiz, quizStore)
		giveRoundSummary(t, quizStore, round1, "Round one wrapped up")
		round2 := &quiz.Round{QuizID: testQuiz.ID, Position: 1, Title: "Round 2", Summary: "Round two wrapped up"}
		if err := quizStore.CreateRound(ctx, round2); err != nil {
			t.Fatalf("CreateRound err = %v, want nil", err)
		}
		if err := quizStore.MoveQuestionToRound(ctx, testQuiz.ID, testQuiz.Questions[1].ID, round2.ID); err != nil {
			t.Fatalf("MoveQuestionToRound err = %v, want nil", err)
		}

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		answer := func(item *Item) {
			t.Helper()
			optID := item.Question.QuizQuestion.Options[0].ID
			if _, err := svc.SubmitAnswer(
				ctx, testGame.ID, 1, item.Question.QuizQuestion.ID, optID, time.Now(),
			); err != nil {
				t.Fatalf("SubmitAnswer err = %v, want nil", err)
			}
		}

		nextBoundary := func(label string, roundID int64, phase RoundPhase) {
			t.Helper()
			item, err := svc.GetNext(ctx, testGame.ID, 1)
			if err != nil {
				t.Fatalf("GetNext (%s) err = %v, want nil", label, err)
			}
			if got, want := item.Type, ItemTypeRoundBoundary; got != want {
				t.Fatalf("%s item.Type = %q, want %q", label, got, want)
			}
			if got, want := item.Phase, phase; got != want {
				t.Fatalf("%s item.Phase = %q, want %q", label, got, want)
			}
			if got, want := item.Round.ID, roundID; got != want {
				t.Errorf("%s boundary round = %d, want %d", label, got, want)
			}
			if err = svc.MarkRoundSeen(ctx, testGame.ID, 1, roundID, phase); err != nil {
				t.Fatalf("MarkRoundSeen (%s) err = %v, want nil", label, err)
			}
		}
		nextQuestion := func(label string, wantQuestionID int64) {
			t.Helper()
			item, err := svc.GetNext(ctx, testGame.ID, 1)
			if err != nil {
				t.Fatalf("GetNext (%s) err = %v, want nil", label, err)
			}
			if got, want := item.Type, ItemTypeQuestion; got != want {
				t.Fatalf("%s item.Type = %q, want %q", label, got, want)
			}
			if got, want := item.Question.QuizQuestion.ID, wantQuestionID; got != want {
				t.Errorf("%s question = %d, want %d", label, got, want)
			}
			answer(item)
		}

		nextBoundary("intro 1", round1, RoundPhaseIntro)
		nextQuestion("Q1", testQuiz.Questions[0].ID)
		nextBoundary("results 1", round1, RoundPhaseResults)
		nextBoundary("intro 2", round2.ID, RoundPhaseIntro)
		nextQuestion("Q2", testQuiz.Questions[1].ID)
		nextBoundary("results 2", round2.ID, RoundPhaseResults)

		// exhausted
		_, err := svc.GetNext(ctx, testGame.ID, 1)
		if got, want := err, ErrNoMoreQuestions; !errors.Is(got, want) {
			t.Errorf("final GetNext err = %v, want %v", got, want)
		}
	})

	t.Run("resume returns the in-flight question unchanged", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())

		first, err := svc.GetNext(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("first GetNext err = %v, want nil", err)
		}
		second, err := svc.GetNext(ctx, testGame.ID, 1)
		if err != nil {
			t.Fatalf("second GetNext err = %v, want nil", err)
		}
		if got, want := second.Type, ItemTypeQuestion; got != want {
			t.Fatalf("second.Type = %q, want %q", got, want)
		}
		if got, want := second.Question.QuestionID, first.Question.QuestionID; got != want {
			t.Errorf("resume question = %d, want %d (must hand back the same in-flight question)", got, want)
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

func TestService_MarkRoundSeen(t *testing.T) {
	t.Parallel()

	t.Run("idempotent", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}
		roundID := firstRoundID(t, testQuiz, quizStore)

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())
		if err := svc.MarkRoundSeen(ctx, testGame.ID, 1, roundID, RoundPhaseIntro); err != nil {
			t.Errorf("first MarkRoundSeen err = %v, want nil", err)
		}
		if err := svc.MarkRoundSeen(ctx, testGame.ID, 1, roundID, RoundPhaseIntro); err != nil {
			t.Errorf("second MarkRoundSeen err = %v, want nil (idempotent)", err)
		}
	})

	t.Run("unknown phase returns ErrInvalidRoundPhase", func(t *testing.T) {
		t.Parallel()

		svc := NewService(stubStore{}, stubQuizStore{}, slog.Default())
		err := svc.MarkRoundSeen(t.Context(), "game-1", 1, 1, RoundPhase("bogus"))
		if got, want := err, ErrInvalidRoundPhase; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("non-participant returns ErrGameNotFound", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}
		roundID := firstRoundID(t, testQuiz, quizStore)

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())
		err := svc.MarkRoundSeen(ctx, testGame.ID, 999, roundID, RoundPhaseIntro)
		if got, want := err, ErrGameNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("round from a different quiz returns ErrRoundNotFound", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		db := dbtest.Open(t)

		quizStore := store.NewQuizStore(db, slog.Default())
		gameStore := store.NewGameStore(db, slog.Default())

		testQuiz := newTestQuiz(t)
		if err := quizStore.CreateQuiz(ctx, testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}

		quizB := &quiz.Quiz{Title: "Other", Slug: "other", CreatedByPlayerID: seededAdminID}
		if err := quizStore.CreateQuiz(ctx, quizB); err != nil {
			t.Fatalf("CreateQuiz B err = %v, want nil", err)
		}
		groupOnB := firstRoundID(t, quizB, quizStore)

		testGame := newTestGame(t, testQuiz)
		if err := gameStore.CreateGame(ctx, testGame); err != nil {
			t.Fatalf("CreateGame err = %v, want nil", err)
		}
		if err := gameStore.CreateParticipant(
			ctx,
			&Participant{GameID: testGame.ID, PlayerID: 1, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v, want nil", err)
		}

		svc := NewService(gameStore, quizStore, slog.Default())
		err := svc.MarkRoundSeen(ctx, testGame.ID, 1, groupOnB, RoundPhaseIntro)
		if got, want := err, quiz.ErrRoundNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}
