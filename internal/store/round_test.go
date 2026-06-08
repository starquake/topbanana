package store_test

import (
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/quiz"
	. "github.com/starquake/topbanana/internal/store"
)

func newTestQuizForGroups(t *testing.T, qs *QuizStore) *quiz.Quiz {
	t.Helper()

	qz := &quiz.Quiz{
		Title:                "Quiz With Groups",
		Slug:                 "quiz-with-rounds",
		Description:          "fixture for round tests",
		CreatedByPlayerID:    seededAdminID,
		CreatedByDisplayName: seededAdminDisplayName,
		TimeLimitSeconds:     quiz.DefaultTimeLimitSeconds,
		Visibility:           quiz.VisibilityPublic,
	}
	if err := qs.CreateQuiz(t.Context(), qz); err != nil {
		t.Fatalf("failed to create quiz fixture: %v", err)
	}

	return qz
}

func TestQuizStore_CreateQuiz_SeedsDefaultGroup(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	qz := newTestQuizForGroups(t, quizStore)

	rounds, err := quizStore.ListRoundsByQuiz(t.Context(), qz.ID)
	if err != nil {
		t.Fatalf("ListRoundsByQuiz err = %v, want nil", err)
	}
	if got, want := len(rounds), 1; got != want {
		t.Fatalf("len(rounds) = %d, want %d", got, want)
	}
	if got, want := rounds[0].Position, 0; got != want {
		t.Errorf("default round Position = %d, want %d", got, want)
	}
	if got, want := rounds[0].Title, "Round 1"; got != want {
		t.Errorf("default round Title = %q, want %q", got, want)
	}
}

// TestQuizStore_CreateQuiz_WithAuthoredRounds pins the import rounds[]
// path (#546): a quiz created with Quiz.Rounds set persists exactly those
// rounds - the first authored round reuses the auto-seeded default round
// rather than leaving a stray empty "Round 1" - and each round's
// questions land on it in quiz-wide position order.
func TestQuizStore_CreateQuiz_WithAuthoredRounds(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())

	q1 := &quiz.Question{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "a", Correct: true}}}
	q2 := &quiz.Question{Text: "Q2", Position: 2, Options: []*quiz.Option{{Text: "b", Correct: true}}}
	q3 := &quiz.Question{Text: "Q3", Position: 3, Options: []*quiz.Option{{Text: "c", Correct: true}}}
	qz := &quiz.Quiz{
		Title:             "Authored Rounds",
		Slug:              "authored-rounds",
		Description:       "fixture",
		CreatedByPlayerID: seededAdminID,
		Rounds: []*quiz.Round{
			{Title: "First", Summary: "intro", Questions: []*quiz.Question{q1, q2}},
			{Title: "Second", Questions: []*quiz.Question{q3}},
		},
	}
	if err := quizStore.CreateQuiz(t.Context(), qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}

	rounds, err := quizStore.ListRoundsByQuiz(t.Context(), qz.ID)
	if err != nil {
		t.Fatalf("ListRoundsByQuiz err = %v, want nil", err)
	}
	if got, want := len(rounds), 2; got != want {
		t.Fatalf("len(rounds) = %d, want %d (no stray default round)", got, want)
	}
	if got, want := rounds[0].Title, "First"; got != want {
		t.Errorf("rounds[0].Title = %q, want %q", got, want)
	}
	if got, want := rounds[0].Summary, "intro"; got != want {
		t.Errorf("rounds[0].Summary = %q, want %q", got, want)
	}
	if got, want := rounds[0].Position, 0; got != want {
		t.Errorf("rounds[0].Position = %d, want %d", got, want)
	}
	if got, want := rounds[1].Title, "Second"; got != want {
		t.Errorf("rounds[1].Title = %q, want %q", got, want)
	}
	if got, want := rounds[1].Position, 1; got != want {
		t.Errorf("rounds[1].Position = %d, want %d", got, want)
	}

	questions, err := quizStore.ListQuestions(t.Context(), qz.ID)
	if err != nil {
		t.Fatalf("ListQuestions err = %v, want nil", err)
	}
	if got, want := len(questions), 3; got != want {
		t.Fatalf("len(questions) = %d, want %d", got, want)
	}
	roundByID := map[int64]string{rounds[0].ID: "First", rounds[1].ID: "Second"}
	wantRound := map[string]string{"Q1": "First", "Q2": "First", "Q3": "Second"}
	for _, qs := range questions {
		if got, want := roundByID[qs.RoundID], wantRound[qs.Text]; got != want {
			t.Errorf("question %q in round %q, want %q", qs.Text, got, want)
		}
	}
}

func TestQuizStore_CreateQuestion_LandsInDefaultGroup(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	qz := newTestQuizForGroups(t, quizStore)

	deflt, err := quizStore.GetDefaultRound(t.Context(), qz.ID)
	if err != nil {
		t.Fatalf("GetDefaultRound err = %v, want nil", err)
	}

	qs := &quiz.Question{QuizID: qz.ID, Text: "Q", Position: 1}
	if createErr := quizStore.CreateQuestion(t.Context(), qs); createErr != nil {
		t.Fatalf("CreateQuestion err = %v, want nil", createErr)
	}
	if got, want := qs.RoundID, deflt.ID; got != want {
		t.Errorf("question RoundID = %d, want %d (default round)", got, want)
	}

	reloaded, err := quizStore.GetQuestion(t.Context(), qs.ID)
	if err != nil {
		t.Fatalf("GetQuestion err = %v", err)
	}
	if got, want := reloaded.RoundID, deflt.ID; got != want {
		t.Errorf("reloaded RoundID = %d, want %d", got, want)
	}
}

func TestQuizStore_CreateRound(t *testing.T) {
	t.Parallel()

	t.Run("stores the supplied fields", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		qz := newTestQuizForGroups(t, quizStore)

		g := &quiz.Round{QuizID: qz.ID, Position: 1, Title: "Round 2", Summary: "Halftime"}
		if err := quizStore.CreateRound(t.Context(), g); err != nil {
			t.Fatalf("CreateRound err = %v, want nil", err)
		}
		if g.ID == 0 {
			t.Error("g.ID = 0, want non-zero")
		}

		reloaded, err := quizStore.GetRound(t.Context(), g.ID)
		if err != nil {
			t.Fatalf("GetRound err = %v", err)
		}
		if got, want := reloaded.Title, "Round 2"; got != want {
			t.Errorf("reloaded.Title = %q, want %q", got, want)
		}
		if got, want := reloaded.Summary, "Halftime"; got != want {
			t.Errorf("reloaded.Summary = %q, want %q", got, want)
		}
		if got, want := reloaded.Position, 1; got != want {
			t.Errorf("reloaded.Position = %d, want %d", got, want)
		}
	})

	t.Run("position collision returns ErrRoundPositionTaken", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		qz := newTestQuizForGroups(t, quizStore)

		// Position 0 is already taken by the default round.
		g := &quiz.Round{QuizID: qz.ID, Position: 0, Title: "dup"}
		err := quizStore.CreateRound(t.Context(), g)
		if got, want := err, quiz.ErrRoundPositionTaken; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestQuizStore_GetRound(t *testing.T) {
	t.Parallel()

	t.Run("returns ErrRoundNotFound for missing id", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		_, err := quizStore.GetRound(t.Context(), 99999)
		if got, want := err, quiz.ErrRoundNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestQuizStore_GetDefaultRound(t *testing.T) {
	t.Parallel()

	t.Run("returns the lowest-position round", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		qz := newTestQuizForGroups(t, quizStore)

		second := &quiz.Round{QuizID: qz.ID, Position: 1, Title: "Round 2"}
		if err := quizStore.CreateRound(t.Context(), second); err != nil {
			t.Fatalf("CreateRound err = %v", err)
		}

		deflt, err := quizStore.GetDefaultRound(t.Context(), qz.ID)
		if err != nil {
			t.Fatalf("GetDefaultRound err = %v, want nil", err)
		}
		if got, want := deflt.Position, 0; got != want {
			t.Errorf("default Position = %d, want %d", got, want)
		}
	})

	t.Run("returns ErrRoundNotFound when quiz has no rounds", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		_, err := quizStore.GetDefaultRound(t.Context(), 99999)
		if got, want := err, quiz.ErrRoundNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestQuizStore_ListRoundsByQuiz(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	qz := newTestQuizForGroups(t, quizStore)

	// Insert out of order so the ORDER BY in the query is the only thing
	// producing the sorted output the assertions below pin.
	for _, pos := range []int{2, 1} {
		g := &quiz.Round{QuizID: qz.ID, Position: pos, Title: "extra"}
		if err := quizStore.CreateRound(t.Context(), g); err != nil {
			t.Fatalf("CreateRound err = %v", err)
		}
	}

	rounds, err := quizStore.ListRoundsByQuiz(t.Context(), qz.ID)
	if err != nil {
		t.Fatalf("ListRoundsByQuiz err = %v, want nil", err)
	}
	if got, want := len(rounds), 3; got != want {
		t.Fatalf("len(rounds) = %d, want %d", got, want)
	}
	for i, wantPos := range []int{0, 1, 2} {
		if got, want := rounds[i].Position, wantPos; got != want {
			t.Errorf("rounds[%d].Position = %d, want %d", i, got, want)
		}
	}
}

func TestQuizStore_UpdateRound(t *testing.T) {
	t.Parallel()

	t.Run("updates title, summary and position", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		qz := newTestQuizForGroups(t, quizStore)

		g := &quiz.Round{QuizID: qz.ID, Position: 1, Title: "before"}
		if err := quizStore.CreateRound(t.Context(), g); err != nil {
			t.Fatalf("CreateRound err = %v", err)
		}

		g.Title = "after"
		g.Summary = "summary"
		g.Position = 2
		if err := quizStore.UpdateRound(t.Context(), g); err != nil {
			t.Fatalf("UpdateRound err = %v, want nil", err)
		}

		reloaded, err := quizStore.GetRound(t.Context(), g.ID)
		if err != nil {
			t.Fatalf("GetRound err = %v", err)
		}
		if got, want := reloaded.Title, "after"; got != want {
			t.Errorf("reloaded.Title = %q, want %q", got, want)
		}
		if got, want := reloaded.Summary, "summary"; got != want {
			t.Errorf("reloaded.Summary = %q, want %q", got, want)
		}
		if got, want := reloaded.Position, 2; got != want {
			t.Errorf("reloaded.Position = %d, want %d", got, want)
		}
	})

	t.Run("moving onto an occupied slot returns ErrRoundPositionTaken", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		qz := newTestQuizForGroups(t, quizStore)

		second := &quiz.Round{QuizID: qz.ID, Position: 1, Title: "second"}
		if err := quizStore.CreateRound(t.Context(), second); err != nil {
			t.Fatalf("CreateRound err = %v", err)
		}

		// Position 0 belongs to the default round.
		second.Position = 0
		err := quizStore.UpdateRound(t.Context(), second)
		if got, want := err, quiz.ErrRoundPositionTaken; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("returns ErrCannotUpdateRoundWithIDZero when id is unset", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		err := quizStore.UpdateRound(t.Context(), &quiz.Round{Title: "noop"})
		if got, want := err, quiz.ErrCannotUpdateRoundWithIDZero; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("returns ErrUpdatingRoundNoRowsAffected for a stale id", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		err := quizStore.UpdateRound(t.Context(), &quiz.Round{ID: 99999, Title: "noop"})
		if got, want := err, quiz.ErrUpdatingRoundNoRowsAffected; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestQuizStore_DeleteRound(t *testing.T) {
	t.Parallel()

	t.Run("removes the row", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		qz := newTestQuizForGroups(t, quizStore)

		g := &quiz.Round{QuizID: qz.ID, Position: 1, Title: "doomed"}
		if err := quizStore.CreateRound(t.Context(), g); err != nil {
			t.Fatalf("CreateRound err = %v", err)
		}

		if err := quizStore.DeleteRound(t.Context(), g.ID); err != nil {
			t.Fatalf("DeleteRound err = %v, want nil", err)
		}

		_, err := quizStore.GetRound(t.Context(), g.ID)
		if got, want := err, quiz.ErrRoundNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("returns ErrDeletingRoundNoRowsAffected for a stale id", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		err := quizStore.DeleteRound(t.Context(), 99999)
		if got, want := err, quiz.ErrDeletingRoundNoRowsAffected; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("cascades to the round's questions", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		qz := newTestQuizForGroups(t, quizStore)

		g := &quiz.Round{QuizID: qz.ID, Position: 1, Title: "Round 2"}
		if err := quizStore.CreateRound(t.Context(), g); err != nil {
			t.Fatalf("CreateRound err = %v", err)
		}
		qs := &quiz.Question{QuizID: qz.ID, RoundID: g.ID, Text: "Q", Position: 1}
		if err := quizStore.CreateQuestion(t.Context(), qs); err != nil {
			t.Fatalf("CreateQuestion err = %v", err)
		}

		if err := quizStore.DeleteRound(t.Context(), g.ID); err != nil {
			t.Fatalf("DeleteRound err = %v, want nil", err)
		}

		// questions.group_id has ON DELETE CASCADE so the question is gone.
		_, err := quizStore.GetQuestion(t.Context(), qs.ID)
		if got, want := err, quiz.ErrQuestionNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("wipes played game_questions and game_answers for the round's questions", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		qz := newTestQuizForGroups(t, quizStore)

		round := &quiz.Round{QuizID: qz.ID, Position: 1, Title: "Round 2"}
		if err := quizStore.CreateRound(t.Context(), round); err != nil {
			t.Fatalf("CreateRound err = %v", err)
		}
		qs := &quiz.Question{
			QuizID:   qz.ID,
			RoundID:  round.ID,
			Text:     "Q",
			Position: 1,
			Options:  []*quiz.Option{{Text: "A", Correct: true}},
		}
		if err := quizStore.CreateQuestion(t.Context(), qs); err != nil {
			t.Fatalf("CreateQuestion err = %v", err)
		}

		playerStore := NewPlayerStore(db, slog.Default())
		player, err := playerStore.CreateAnonymousPlayer(t.Context(), "anon-round-delete")
		if err != nil {
			t.Fatalf("CreateAnonymousPlayer err = %v", err)
		}

		// Stand up a played game so the round's question is referenced by
		// game_questions.question_id and game_answers.option_id, neither of
		// which has ON DELETE CASCADE. Without the FK cleanup, DeleteRound
		// would fail with FOREIGN KEY constraint failed (787) here (#788).
		gameStore := NewGameStore(db, slog.Default())
		g := &game.Game{QuizID: qz.ID}
		if err = gameStore.CreateGame(t.Context(), g); err != nil {
			t.Fatalf("CreateGame err = %v", err)
		}
		if err = gameStore.CreateParticipant(
			t.Context(), &game.Participant{GameID: g.ID, PlayerID: player.ID, QuizID: qz.ID},
		); err != nil {
			t.Fatalf("CreateParticipant err = %v", err)
		}

		now := time.Now().UTC().Truncate(time.Second)
		gq := &game.Question{
			GameID:     g.ID,
			QuestionID: qs.ID,
			StartedAt:  now,
			ExpiredAt:  now.Add(10 * time.Second),
		}
		if err = gameStore.CreateQuestion(t.Context(), gq); err != nil {
			t.Fatalf("CreateQuestion (game) err = %v", err)
		}
		if err = gameStore.CreateAnswer(t.Context(), &game.Answer{
			GameID:     g.ID,
			PlayerID:   player.ID,
			QuestionID: gq.ID,
			OptionID:   qs.Options[0].ID,
		}); err != nil {
			t.Fatalf("CreateAnswer err = %v", err)
		}

		if err = quizStore.DeleteRound(t.Context(), round.ID); err != nil {
			t.Fatalf("DeleteRound err = %v, want nil", err)
		}

		_, err = quizStore.GetRound(t.Context(), round.ID)
		if got, want := err, quiz.ErrRoundNotFound; !errors.Is(got, want) {
			t.Errorf("GetRound err = %v, want %v", got, want)
		}

		_, err = quizStore.GetQuestion(t.Context(), qs.ID)
		if got, want := err, quiz.ErrQuestionNotFound; !errors.Is(got, want) {
			t.Errorf("GetQuestion err = %v, want %v", got, want)
		}

		assertCount := func(label, sqlStr string, arg any, want int) {
			t.Helper()
			var got int
			if scanErr := db.QueryRowContext(t.Context(), sqlStr, arg).Scan(&got); scanErr != nil {
				t.Fatalf("scan %s err = %v", label, scanErr)
			}
			if got != want {
				t.Errorf("%s count = %d, want %d", label, got, want)
			}
		}

		assertCount("game_questions for deleted question",
			`SELECT COUNT(*) FROM game_questions WHERE question_id = ?`, qs.ID, 0)
		assertCount("game_answers for deleted question's game_question",
			`SELECT COUNT(*) FROM game_answers WHERE game_question_id = ?`, gq.ID, 0)

		// The game itself and its participant survive the round delete.
		assertCount("games", `SELECT COUNT(*) FROM games WHERE id = ?`, g.ID, 1)
		assertCount("game_participants",
			`SELECT COUNT(*) FROM game_participants WHERE game_id = ?`, g.ID, 1)
	})
}

func TestQuizStore_MoveRound(t *testing.T) {
	t.Parallel()

	// Each subtest seeds a quiz that already has the default round at
	// position 0 plus two more so a round has a non-trivial range to move
	// between.
	seed := func(t *testing.T) (*QuizStore, *quiz.Quiz) {
		t.Helper()
		db := dbtest.Open(t)
		qs := NewQuizStore(db, slog.Default())
		qz := newTestQuizForGroups(t, qs)
		for _, pos := range []int{1, 2} {
			if err := qs.CreateRound(t.Context(), &quiz.Round{
				QuizID: qz.ID, Position: pos, Title: "extra",
			}); err != nil {
				t.Fatalf("CreateRound err = %v", err)
			}
		}

		return qs, qz
	}

	groupAt := func(t *testing.T, qs *QuizStore, quizID int64, pos int) *quiz.Round {
		t.Helper()
		rounds, err := qs.ListRoundsByQuiz(t.Context(), quizID)
		if err != nil {
			t.Fatalf("ListRoundsByQuiz err = %v", err)
		}
		for _, g := range rounds {
			if g.Position == pos {
				return g
			}
		}
		t.Fatalf("no round at position %d", pos)

		return nil
	}

	t.Run("move up shifts the round by one slot", func(t *testing.T) {
		t.Parallel()

		qs, qz := seed(t)
		g := groupAt(t, qs, qz.ID, 2)

		if err := qs.MoveRound(t.Context(), qz.ID, g.ID, quiz.DirectionUp); err != nil {
			t.Fatalf("MoveRound up err = %v, want nil", err)
		}

		reloaded, err := qs.GetRound(t.Context(), g.ID)
		if err != nil {
			t.Fatalf("GetRound err = %v", err)
		}
		if got, want := reloaded.Position, 1; got != want {
			t.Errorf("reloaded.Position = %d, want %d", got, want)
		}
	})

	t.Run("move down shifts the round by one slot", func(t *testing.T) {
		t.Parallel()

		qs, qz := seed(t)
		g := groupAt(t, qs, qz.ID, 1)

		if err := qs.MoveRound(t.Context(), qz.ID, g.ID, quiz.DirectionDown); err != nil {
			t.Fatalf("MoveRound down err = %v, want nil", err)
		}

		reloaded, err := qs.GetRound(t.Context(), g.ID)
		if err != nil {
			t.Fatalf("GetRound err = %v", err)
		}
		if got, want := reloaded.Position, 2; got != want {
			t.Errorf("reloaded.Position = %d, want %d", got, want)
		}
	})

	t.Run("move up from the first slot returns ErrRoundMoveImpossible", func(t *testing.T) {
		t.Parallel()

		qs, qz := seed(t)
		g := groupAt(t, qs, qz.ID, 0)

		err := qs.MoveRound(t.Context(), qz.ID, g.ID, quiz.DirectionUp)
		if got, want := err, quiz.ErrRoundMoveImpossible; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("move down from the last slot returns ErrRoundMoveImpossible", func(t *testing.T) {
		t.Parallel()

		qs, qz := seed(t)
		g := groupAt(t, qs, qz.ID, 2)

		err := qs.MoveRound(t.Context(), qz.ID, g.ID, quiz.DirectionDown)
		if got, want := err, quiz.ErrRoundMoveImpossible; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("invalid direction returns ErrInvalidDirection", func(t *testing.T) {
		t.Parallel()

		qs, qz := seed(t)
		g := groupAt(t, qs, qz.ID, 1)

		err := qs.MoveRound(t.Context(), qz.ID, g.ID, "sideways")
		if got, want := err, quiz.ErrInvalidDirection; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("mismatched quiz returns ErrRoundNotFound", func(t *testing.T) {
		t.Parallel()

		qs, qz := seed(t)
		g := groupAt(t, qs, qz.ID, 1)

		err := qs.MoveRound(t.Context(), qz.ID+1, g.ID, quiz.DirectionUp)
		if got, want := err, quiz.ErrRoundNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}

		reloaded, err := qs.GetRound(t.Context(), g.ID)
		if err != nil {
			t.Fatalf("GetRound err = %v", err)
		}
		if got, want := reloaded.Position, 1; got != want {
			t.Errorf("reloaded.Position = %d, want %d (move should not have happened)", got, want)
		}
	})
}

// roundOrder returns the round IDs of a quiz in ascending position order
// and asserts the positions are dense 1..N (the invariant
// MoveRoundToPosition maintains).
func roundOrder(t *testing.T, qs *QuizStore, quizID int64) []int64 {
	t.Helper()
	rounds, err := qs.ListRoundsByQuiz(t.Context(), quizID)
	if err != nil {
		t.Fatalf("ListRoundsByQuiz err = %v", err)
	}
	ids := make([]int64, 0, len(rounds))
	for i, g := range rounds {
		if got, want := g.Position, i+1; got != want {
			t.Errorf("rounds[%d].Position = %d, want %d (dense 1..N)", i, got, want)
		}
		ids = append(ids, g.ID)
	}

	return ids
}

func TestQuizStore_MoveRoundToPosition(t *testing.T) {
	t.Parallel()

	seed := func(t *testing.T) (*QuizStore, *quiz.Quiz, map[string]int64) {
		t.Helper()
		db := dbtest.Open(t)
		qs := NewQuizStore(db, slog.Default())
		qz := newTestQuizForGroups(t, qs)

		ids := map[string]int64{}
		rounds, err := qs.ListRoundsByQuiz(t.Context(), qz.ID)
		if err != nil {
			t.Fatalf("ListRoundsByQuiz err = %v", err)
		}
		ids["A"] = rounds[0].ID
		for i, title := range []string{"B", "C"} {
			g := &quiz.Round{QuizID: qz.ID, Position: i + 1, Title: title}
			if err := qs.CreateRound(t.Context(), g); err != nil {
				t.Fatalf("CreateRound err = %v", err)
			}
			ids[title] = g.ID
		}

		return qs, qz, ids
	}

	t.Run("to first", func(t *testing.T) {
		t.Parallel()

		qs, qz, ids := seed(t)
		if err := qs.MoveRoundToPosition(t.Context(), qz.ID, ids["C"], 1); err != nil {
			t.Fatalf("MoveRoundToPosition err = %v, want nil", err)
		}
		want := []int64{ids["C"], ids["A"], ids["B"]}
		if got := roundOrder(t, qs, qz.ID); !slices.Equal(got, want) {
			t.Errorf("order = %v, want %v", got, want)
		}
	})

	t.Run("to last", func(t *testing.T) {
		t.Parallel()

		qs, qz, ids := seed(t)
		if err := qs.MoveRoundToPosition(t.Context(), qz.ID, ids["A"], 3); err != nil {
			t.Fatalf("MoveRoundToPosition err = %v, want nil", err)
		}
		want := []int64{ids["B"], ids["C"], ids["A"]}
		if got := roundOrder(t, qs, qz.ID); !slices.Equal(got, want) {
			t.Errorf("order = %v, want %v", got, want)
		}
	})

	t.Run("to middle", func(t *testing.T) {
		t.Parallel()

		qs, qz, ids := seed(t)
		if err := qs.MoveRoundToPosition(t.Context(), qz.ID, ids["A"], 2); err != nil {
			t.Fatalf("MoveRoundToPosition err = %v, want nil", err)
		}
		want := []int64{ids["B"], ids["A"], ids["C"]}
		if got := roundOrder(t, qs, qz.ID); !slices.Equal(got, want) {
			t.Errorf("order = %v, want %v", got, want)
		}
	})

	t.Run("no-op same position keeps order", func(t *testing.T) {
		t.Parallel()

		qs, qz, ids := seed(t)
		if err := qs.MoveRoundToPosition(t.Context(), qz.ID, ids["B"], 2); err != nil {
			t.Fatalf("MoveRoundToPosition err = %v, want nil", err)
		}
		want := []int64{ids["A"], ids["B"], ids["C"]}
		if got := roundOrder(t, qs, qz.ID); !slices.Equal(got, want) {
			t.Errorf("order = %v, want %v", got, want)
		}
	})

	t.Run("out-of-range position clamps to last", func(t *testing.T) {
		t.Parallel()

		qs, qz, ids := seed(t)
		if err := qs.MoveRoundToPosition(t.Context(), qz.ID, ids["A"], 99); err != nil {
			t.Fatalf("MoveRoundToPosition err = %v, want nil", err)
		}
		want := []int64{ids["B"], ids["C"], ids["A"]}
		if got := roundOrder(t, qs, qz.ID); !slices.Equal(got, want) {
			t.Errorf("order = %v, want %v", got, want)
		}
	})

	t.Run("missing round returns ErrRoundNotFound", func(t *testing.T) {
		t.Parallel()

		qs, qz, ids := seed(t)
		err := qs.MoveRoundToPosition(t.Context(), qz.ID, ids["C"]+9999, 1)
		if got, want := err, quiz.ErrRoundNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("foreign quiz returns ErrRoundNotFound", func(t *testing.T) {
		t.Parallel()

		qs, qz, ids := seed(t)
		err := qs.MoveRoundToPosition(t.Context(), qz.ID+1, ids["A"], 1)
		if got, want := err, quiz.ErrRoundNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestQuizStore_MoveQuestionToRound(t *testing.T) {
	t.Parallel()

	rounds := []string{"R1", "R2"}
	layout := map[string][]string{
		"R1": {"Q1", "Q2"},
		"R2": {"Q3"},
	}

	t.Run("reassigns the question to the target round", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		f := seedRoundQuiz(t, quizStore, rounds, layout)

		if err := quizStore.MoveQuestionToRound(
			t.Context(), f.quiz.ID, f.questionIDs["Q1"], f.roundIDs["R2"],
		); err != nil {
			t.Fatalf("MoveQuestionToRound err = %v, want nil", err)
		}

		moved, err := quizStore.GetQuestion(t.Context(), f.questionIDs["Q1"])
		if err != nil {
			t.Fatalf("GetQuestion err = %v, want nil", err)
		}
		if got, want := moved.RoundID, f.roundIDs["R2"]; got != want {
			t.Errorf("moved.RoundID = %d, want %d", got, want)
		}
	})

	t.Run("unknown question returns ErrQuestionNotFound", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		f := seedRoundQuiz(t, quizStore, rounds, layout)

		err := quizStore.MoveQuestionToRound(t.Context(), f.quiz.ID, 999999, f.roundIDs["R2"])
		if got, want := err, quiz.ErrQuestionNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("question on a foreign quiz returns ErrQuestionNotFound", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		f := seedRoundQuiz(t, quizStore, rounds, layout)

		err := quizStore.MoveQuestionToRound(t.Context(), f.quiz.ID+1, f.questionIDs["Q1"], f.roundIDs["R2"])
		if got, want := err, quiz.ErrQuestionNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("unknown round returns ErrRoundNotFound", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		f := seedRoundQuiz(t, quizStore, rounds, layout)

		err := quizStore.MoveQuestionToRound(t.Context(), f.quiz.ID, f.questionIDs["Q1"], 999999)
		if got, want := err, quiz.ErrRoundNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("round on a foreign quiz returns ErrRoundNotFound", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		f := seedRoundQuiz(t, quizStore, rounds, layout)

		// Seed a second quiz and target one of its rounds; the cross-quiz
		// guard must reject it even though the round id is real.
		other := seedRoundQuiz2(t, quizStore)
		err := quizStore.MoveQuestionToRound(t.Context(), f.quiz.ID, f.questionIDs["Q1"], other.roundIDs["R1"])
		if got, want := err, quiz.ErrRoundNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

// seedRoundQuiz2 seeds a second quiz with a distinct slug so a cross-quiz
// round-move test has a real but foreign round id to aim at.
func seedRoundQuiz2(t *testing.T, quizStore *QuizStore) roundQuizFixture {
	t.Helper()

	qz := &quiz.Quiz{
		Title:             "Other Round Quiz",
		Slug:              "other-round-quiz",
		Description:       "foreign-round fixture",
		CreatedByPlayerID: seededAdminID,
	}
	if err := quizStore.CreateQuiz(t.Context(), qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}
	deflt, err := quizStore.GetDefaultRound(t.Context(), qz.ID)
	if err != nil {
		t.Fatalf("GetDefaultRound err = %v, want nil", err)
	}

	return roundQuizFixture{quiz: qz, roundIDs: map[string]int64{"R1": deflt.ID}}
}
