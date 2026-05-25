package store_test

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/quiz"
	. "github.com/starquake/topbanana/internal/store"
)

func newTestQuizForBreaks(t *testing.T, qs *QuizStore) *quiz.Quiz {
	t.Helper()

	qz := &quiz.Quiz{
		Title:             "Quiz With Breaks",
		Slug:              "quiz-with-breaks",
		Description:       "fixture for break tests",
		CreatedByPlayerID: seededAdminID,
		CreatedByUsername: seededAdminUsername,
		TimeLimitSeconds:  quiz.DefaultTimeLimitSeconds,
		Visibility:        quiz.VisibilityPublic,
	}
	if err := qs.CreateQuiz(t.Context(), qz); err != nil {
		t.Fatalf("failed to create quiz fixture: %v", err)
	}

	return qz
}

func TestQuizStore_CreateBreakAtNextPosition(t *testing.T) {
	t.Parallel()

	t.Run("assigns sequential positions", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		qz := newTestQuizForBreaks(t, quizStore)

		first := &quiz.Break{QuizID: qz.ID, Text: "Halfway!"}
		if err := quizStore.CreateBreakAtNextPosition(t.Context(), first); err != nil {
			t.Fatalf("first CreateBreakAtNextPosition err = %v, want nil", err)
		}
		if got, want := first.Position, 1; got != want {
			t.Errorf("first.Position = %d, want %d", got, want)
		}
		if first.ID == 0 {
			t.Error("first.ID = 0, want non-zero")
		}

		second := &quiz.Break{QuizID: qz.ID, Text: "Almost done"}
		if err := quizStore.CreateBreakAtNextPosition(t.Context(), second); err != nil {
			t.Fatalf("second CreateBreakAtNextPosition err = %v, want nil", err)
		}
		if got, want := second.Position, 2; got != want {
			t.Errorf("second.Position = %d, want %d", got, want)
		}
	})

	t.Run("empty text is allowed", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		qz := newTestQuizForBreaks(t, quizStore)

		b := &quiz.Break{QuizID: qz.ID}
		if err := quizStore.CreateBreakAtNextPosition(t.Context(), b); err != nil {
			t.Fatalf("CreateBreakAtNextPosition err = %v, want nil", err)
		}
		if got, want := b.Text, ""; got != want {
			t.Errorf("b.Text = %q, want %q", got, want)
		}
	})
}

func TestQuizStore_GetBreak(t *testing.T) {
	t.Parallel()

	t.Run("returns existing break", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		qz := newTestQuizForBreaks(t, quizStore)

		created := &quiz.Break{QuizID: qz.ID, Text: "Pause"}
		if err := quizStore.CreateBreakAtNextPosition(t.Context(), created); err != nil {
			t.Fatalf("CreateBreakAtNextPosition err = %v", err)
		}

		reloaded, err := quizStore.GetBreak(t.Context(), created.ID)
		if err != nil {
			t.Fatalf("GetBreak err = %v, want nil", err)
		}
		if got, want := reloaded.Text, "Pause"; got != want {
			t.Errorf("GetBreak.Text = %q, want %q", got, want)
		}
		if got, want := reloaded.QuizID, qz.ID; got != want {
			t.Errorf("GetBreak.QuizID = %d, want %d", got, want)
		}
		if got, want := reloaded.Position, 1; got != want {
			t.Errorf("GetBreak.Position = %d, want %d", got, want)
		}
	})

	t.Run("returns ErrBreakNotFound for missing id", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		_, err := quizStore.GetBreak(t.Context(), 99999)
		if got, want := err, quiz.ErrBreakNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestQuizStore_ListBreaksByQuiz(t *testing.T) {
	t.Parallel()

	t.Run("returns breaks in position order", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		qz := newTestQuizForBreaks(t, quizStore)

		for _, text := range []string{"first", "second", "third"} {
			b := &quiz.Break{QuizID: qz.ID, Text: text}
			if err := quizStore.CreateBreakAtNextPosition(t.Context(), b); err != nil {
				t.Fatalf("CreateBreakAtNextPosition err = %v", err)
			}
		}

		breaks, err := quizStore.ListBreaksByQuiz(t.Context(), qz.ID)
		if err != nil {
			t.Fatalf("ListBreaksByQuiz err = %v, want nil", err)
		}
		if got, want := len(breaks), 3; got != want {
			t.Fatalf("len(breaks) = %d, want %d", got, want)
		}
		for i, wantText := range []string{"first", "second", "third"} {
			if got, want := breaks[i].Text, wantText; got != want {
				t.Errorf("breaks[%d].Text = %q, want %q", i, got, want)
			}
			if got, want := breaks[i].Position, i+1; got != want {
				t.Errorf("breaks[%d].Position = %d, want %d", i, got, want)
			}
		}
	})

	t.Run("empty result for quiz with no breaks", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		qz := newTestQuizForBreaks(t, quizStore)

		breaks, err := quizStore.ListBreaksByQuiz(t.Context(), qz.ID)
		if err != nil {
			t.Fatalf("ListBreaksByQuiz err = %v, want nil", err)
		}
		if got, want := len(breaks), 0; got != want {
			t.Errorf("len(breaks) = %d, want %d", got, want)
		}
	})
}

func TestQuizStore_UpdateBreak(t *testing.T) {
	t.Parallel()

	t.Run("updates text", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		qz := newTestQuizForBreaks(t, quizStore)

		b := &quiz.Break{QuizID: qz.ID, Text: "before"}
		if err := quizStore.CreateBreakAtNextPosition(t.Context(), b); err != nil {
			t.Fatalf("CreateBreakAtNextPosition err = %v", err)
		}

		b.Text = "after"
		if err := quizStore.UpdateBreak(t.Context(), b); err != nil {
			t.Fatalf("UpdateBreak err = %v, want nil", err)
		}

		reloaded, err := quizStore.GetBreak(t.Context(), b.ID)
		if err != nil {
			t.Fatalf("GetBreak err = %v", err)
		}
		if got, want := reloaded.Text, "after"; got != want {
			t.Errorf("reloaded.Text = %q, want %q", got, want)
		}
	})

	t.Run("returns ErrCannotUpdateBreakWithIDZero when id is unset", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		err := quizStore.UpdateBreak(t.Context(), &quiz.Break{Text: "noop"})
		if got, want := err, quiz.ErrCannotUpdateBreakWithIDZero; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("returns ErrUpdatingBreakNoRowsAffected for a stale id", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		err := quizStore.UpdateBreak(t.Context(), &quiz.Break{ID: 99999, Text: "noop"})
		if got, want := err, quiz.ErrUpdatingBreakNoRowsAffected; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestQuizStore_DeleteBreak(t *testing.T) {
	t.Parallel()

	t.Run("removes the row", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		qz := newTestQuizForBreaks(t, quizStore)

		b := &quiz.Break{QuizID: qz.ID, Text: "doomed"}
		if err := quizStore.CreateBreakAtNextPosition(t.Context(), b); err != nil {
			t.Fatalf("CreateBreakAtNextPosition err = %v", err)
		}

		if err := quizStore.DeleteBreak(t.Context(), b.ID); err != nil {
			t.Fatalf("DeleteBreak err = %v, want nil", err)
		}

		_, err := quizStore.GetBreak(t.Context(), b.ID)
		if got, want := err, quiz.ErrBreakNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("returns ErrDeletingBreakNoRowsAffected for a stale id", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		err := quizStore.DeleteBreak(t.Context(), 99999)
		if got, want := err, quiz.ErrDeletingBreakNoRowsAffected; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestQuizStore_DeleteQuiz_CascadesBreaks(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	qz := newTestQuizForBreaks(t, quizStore)

	b := &quiz.Break{QuizID: qz.ID, Text: "should disappear"}
	if err := quizStore.CreateBreakAtNextPosition(t.Context(), b); err != nil {
		t.Fatalf("CreateBreakAtNextPosition err = %v", err)
	}

	if err := quizStore.DeleteQuiz(t.Context(), qz.ID); err != nil {
		t.Fatalf("DeleteQuiz err = %v, want nil", err)
	}

	// breaks.quiz_id has ON DELETE CASCADE so the row should be gone.
	_, err := quizStore.GetBreak(t.Context(), b.ID)
	if got, want := err, quiz.ErrBreakNotFound; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}
