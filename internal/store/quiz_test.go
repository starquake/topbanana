package store_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/quiz"
	. "github.com/starquake/topbanana/internal/store"
)

var (
	lessQuizzes   = func(a, b *quiz.Quiz) bool { return a.Title < b.Title }
	lessQuestions = func(a, b *quiz.Question) bool { return a.Text < b.Text }
	lessOptions   = func(a, b *quiz.Option) bool { return a.Text < b.Text }
)

func newTestQuizzes() []*quiz.Quiz {
	return []*quiz.Quiz{
		{
			Title:       "Quiz 1",
			Slug:        "quiz-1",
			Description: "Quiz 1 Description",
			Questions: []*quiz.Question{
				{
					Text:     "Question 1-1",
					Position: 10,
					Options: []*quiz.Option{
						{Text: "Option 1-1-1"},
						{Text: "Option 1-1-2"},
						{Text: "Option 1-1-3", Correct: true},
						{Text: "Option 1-1-4"},
					},
				},
				{
					Text:     "Question 2",
					Position: 20,
					Options: []*quiz.Option{
						{Text: "Option 1-2-1"},
						{Text: "Option 1-2-2"},
						{Text: "Option 1-2-3", Correct: true},
						{Text: "Option 1-2-4"},
					},
				},
			},
		},
		{
			Title:       "Quiz 2",
			Slug:        "quiz-2",
			Description: "Quiz 2 Description",
			Questions: []*quiz.Question{
				{
					Text:     "Question 2-1",
					Position: 10,
					Options: []*quiz.Option{
						{Text: "Option 2-1-1"},
						{Text: "Option 2-1-2"},
						{Text: "Option 2-1-3", Correct: true},
						{Text: "Option 2-1-4"},
					},
				},
				{
					Text:     "Question 2-2",
					Position: 20,
					Options: []*quiz.Option{
						{Text: "Option 2-2-1"},
						{Text: "Option 2-2-2"},
						{Text: "Option 2-2-3", Correct: true},
						{Text: "Option 2-2-4"},
					},
				},
			},
		},
	}
}

func existingTestQuizzes() []*quiz.Quiz {
	return []*quiz.Quiz{
		{
			ID:          1,
			Title:       "Quiz 1",
			Slug:        "quiz-1",
			Description: "Quiz 1 Description",
			Questions: []*quiz.Question{
				{
					ID:       1,
					Text:     "Question 1-1",
					Position: 10,
					Options: []*quiz.Option{
						{ID: 1, Text: "Option 1-1-1"},
						{ID: 2, Text: "Option 1-1-2"},
						{ID: 3, Text: "Option 1-1-3", Correct: true},
						{ID: 4, Text: "Option 1-1-4"},
					},
				},
				{
					ID:       2,
					Text:     "Question 2",
					Position: 20,
					Options: []*quiz.Option{
						{ID: 5, Text: "Option 1-2-1"},
						{ID: 6, Text: "Option 1-2-2"},
						{ID: 7, Text: "Option 1-2-3", Correct: true},
						{ID: 8, Text: "Option 1-2-4"},
					},
				},
			},
		},
		{
			Title:       "Quiz 2",
			Slug:        "quiz-2",
			Description: "Quiz 2 Description",
			Questions: []*quiz.Question{
				{
					ID:       3,
					Text:     "Question 2-1",
					Position: 10,
					Options: []*quiz.Option{
						{ID: 9, Text: "Option 2-1-1"},
						{ID: 10, Text: "Option 2-1-2"},
						{ID: 11, Text: "Option 2-1-3", Correct: true},
						{ID: 12, Text: "Option 2-1-4"},
					},
				},
				{
					ID:       4,
					Text:     "Question 2-2",
					Position: 20,
					Options: []*quiz.Option{
						{ID: 13, Text: "Option 2-2-1"},
						{ID: 14, Text: "Option 2-2-2"},
						{ID: 15, Text: "Option 2-2-3", Correct: true},
						{ID: 16, Text: "Option 2-2-4"},
					},
				},
			},
		},
	}
}

func newTestQuestions() []*quiz.Question {
	return []*quiz.Question{
		{
			Text:     "Question 5",
			Position: 10,
			Options: []*quiz.Option{
				{Text: "Option 5-1"},
				{Text: "Option 5-2"},
				{Text: "Option 5-3", Correct: true},
				{Text: "Option 5-4"},
			},
		},
	}
}

func TestSQLiteStore_Ping(t *testing.T) {
	t.Parallel()

	t.Run("ping success", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())

		if err := quizStore.Ping(t.Context()); err != nil {
			t.Errorf("unexpected error pinging database: %v", err)
		}
	})

	t.Run("ping failure", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		// Close the database to trigger a ping error
		if err := db.Close(); err != nil {
			t.Fatalf("failed to close database: %v", err)
		}

		err := quizStore.Ping(t.Context())
		if err == nil {
			t.Fatal("expected error pinging closed database, got nil")
		}

		if got, want := err.Error(), "failed to ping database"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, want it to contain %q", got, want)
		}
	})
}

func TestSQLiteStore_ListQuizzes(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)

	quizStore := NewQuizStore(db, slog.Default())

	testQuizzes := newTestQuizzes()

	for _, testQz := range testQuizzes {
		err := quizStore.CreateQuiz(t.Context(), testQz)
		if err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}
	}

	quizzes, err := quizStore.ListQuizzes(t.Context())
	if err != nil {
		t.Fatalf("failed to list quizzes: %v", err)
	}
	if diff := cmp.Diff(quizzes, testQuizzes,
		cmpopts.SortSlices(lessQuizzes),
		cmpopts.SortSlices(lessQuestions),
		cmpopts.SortSlices(lessOptions),
		cmpopts.EquateApproxTime(3*time.Second),
	); diff != "" {
		t.Errorf("quizzes diff (-got +want):\n%s", diff)
	}
}

func TestSQLiteStore_ListQuizzes_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("context canceled", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := quizStore.ListQuizzes(ctx)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "context canceled"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("list questions error", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]

		var err error
		err = quizStore.CreateQuiz(t.Context(), testQuiz)
		if err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		_, err = db.ExecContext(t.Context(), `ALTER TABLE questions RENAME TO questions_backup;`)
		if err != nil {
			t.Fatalf("failed to rename table questions: %v", err)
		}
		_, err = quizStore.ListQuizzes(t.Context())
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "failed to list questions"; !strings.Contains(got, want) {
			t.Fatalf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("list options error", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]

		var err error
		err = quizStore.CreateQuiz(t.Context(), testQuiz)
		if err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		_, err = db.ExecContext(t.Context(), `ALTER TABLE options RENAME TO options_backup;`)
		if err != nil {
			t.Fatalf("failed to rename table options: %v", err)
		}
		_, err = quizStore.ListQuizzes(t.Context())
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "failed to list options"; !strings.Contains(got, want) {
			t.Fatalf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestSQLiteStore_GetQuiz(t *testing.T) {
	t.Parallel()

	t.Run("valid quiz ID", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]

		err := quizStore.CreateQuiz(t.Context(), testQuiz)
		if err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		qz, err := quizStore.GetQuiz(t.Context(), testQuiz.ID)
		if err != nil {
			t.Errorf("failed to get testQuiz by ID: %v", err)
		}

		if diff := cmp.Diff(qz, testQuiz,
			cmpopts.SortSlices(lessQuestions),
			cmpopts.SortSlices(lessOptions),
			cmpopts.EquateApproxTime(3*time.Second),
		); diff != "" {
			t.Errorf("quizzes diff (-got +want):\n%s", diff)
		}
	})

	t.Run("invalid quiz ID", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())

		qz, err := quizStore.GetQuiz(t.Context(), 999)
		if got, want := err, quiz.ErrQuizNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
		if qz != nil {
			t.Errorf("quiz is not nil: %v", qz)
		}
	})
}

func TestSQLiteStore_GetQuiz_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("context canceled", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())

		// Create and cancel context to trigger an error
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := quizStore.GetQuiz(ctx, 1)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "context canceled"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("list questions error", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]

		var err error
		err = quizStore.CreateQuiz(t.Context(), testQuiz)
		if err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		_, err = db.ExecContext(t.Context(), `ALTER TABLE questions RENAME TO questions_backup;`)
		if err != nil {
			t.Fatalf("failed to rename table questions: %v", err)
		}
		_, err = quizStore.GetQuiz(t.Context(), testQuiz.ID)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "failed to list questions"; !strings.Contains(got, want) {
			t.Fatalf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestSQLiteStore_GetQuestion(t *testing.T) {
	t.Parallel()

	t.Run("valid question ID", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]

		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		qs, err := quizStore.GetQuestion(t.Context(), testQuiz.Questions[0].ID)
		if err != nil {
			t.Errorf("failed to get question by ID: %v", err)
		}
		if qs.ID == int64(0) {
			t.Errorf("qs.ID = %d, should not be 0", qs.ID)
		}
		if diff := cmp.Diff(qs, testQuiz.Questions[0],
			cmpopts.SortSlices(lessOptions),
		); diff != "" {
			t.Errorf("question diff (-got +want):\n%s", diff)
		}
	})

	t.Run("invalid question ID", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())

		qs, err := quizStore.GetQuestion(t.Context(), 999)
		if got, want := err, quiz.ErrQuestionNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
		if qs != nil {
			t.Errorf("question is not nil: %v", qs)
		}
	})
}

func TestSQLiteStore_GetQuestion_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("context canceled", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := quizStore.GetQuestion(ctx, 1)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "context canceled"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("list options error", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]

		var err error
		err = quizStore.CreateQuiz(t.Context(), testQuiz)
		if err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		_, err = db.ExecContext(t.Context(), `ALTER TABLE options RENAME TO options_backup;`)
		if err != nil {
			t.Fatalf("failed to rename table options: %v", err)
		}
		_, err = quizStore.GetQuestion(t.Context(), testQuiz.ID)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "failed to list options"; !strings.Contains(got, want) {
			t.Fatalf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestSQLiteStore_CreateQuiz(t *testing.T) {
	t.Parallel()

	t.Run("quiz with questions and options", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]

		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		qz, err := quizStore.GetQuiz(t.Context(), testQuiz.ID)
		if err != nil {
			t.Errorf("failed to get question by ID: %v", err)
		}

		if diff := cmp.Diff(qz, testQuiz,
			cmpopts.SortSlices(lessQuestions),
			cmpopts.SortSlices(lessOptions),
			cmpopts.EquateApproxTime(3*time.Second),
		); diff != "" {
			t.Errorf("quizzes diff (-got +want):\n%s", diff)
		}
	})

	t.Run("ignore supplied ID's", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())

		suppliedQuizID := int64(1000)
		suppliedQuestionID := int64(1001)
		suppliedOption1ID := int64(1002)
		suppliedOption2ID := int64(1003)

		testQuiz := newTestQuizzes()[0]
		testQuiz.ID = suppliedQuizID
		testQuiz.Questions[0].ID = suppliedQuestionID
		testQuiz.Questions[0].Options[0].ID = suppliedOption1ID
		testQuiz.Questions[0].Options[1].ID = suppliedOption2ID

		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		qz, err := quizStore.GetQuiz(t.Context(), suppliedQuizID)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if !errors.Is(err, quiz.ErrQuizNotFound) {
			t.Errorf("err = %v, want %v", err, quiz.ErrQuizNotFound)
		}
		if qz != nil {
			t.Errorf("qz = %v, want nil", qz)
		}

		if testQuiz.ID == suppliedQuizID {
			t.Fatalf("testQuiz.ID = %d, should not be %d", testQuiz.ID, suppliedQuizID)
		}
		if testQuiz.Questions[0].ID == suppliedQuestionID {
			t.Fatalf("testQuiz.Questions[0].ID = %d, should not be %d", testQuiz.Questions[0].ID, suppliedQuestionID)
		}
		if testQuiz.Questions[0].Options[0].ID == suppliedOption1ID {
			t.Fatalf(
				"testQuiz.Questions[0].Options[0].ID = %d, should not be %d",
				testQuiz.Questions[0].Options[0].ID,
				suppliedOption1ID,
			)
		}
		if testQuiz.Questions[0].Options[1].ID == suppliedOption2ID {
			t.Fatalf(
				"testQuiz.Questions[0].Options[1].ID = %d, should not be %d",
				testQuiz.Questions[0].Options[1].ID,
				suppliedOption2ID,
			)
		}
	})
}

func TestSQLiteStore_CreateQuiz_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("quiz insert error", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())

		// Rename questions table to force an insert error
		_, err := db.ExecContext(t.Context(), "ALTER TABLE quizzes RENAME TO quizzes_backup")
		if err != nil {
			t.Fatalf("failed to rename table: %v", err)
		}

		testQuiz := newTestQuizzes()[0]

		err = quizStore.CreateQuiz(t.Context(), testQuiz)
		if err == nil {
			t.Fatal("got nil, want error")
		}

		if got, want := err.Error(), "failed to create quiz"; !strings.Contains(err.Error(), want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("question insert error", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())

		// Rename questions table to force an insert error
		_, err := db.ExecContext(t.Context(), "ALTER TABLE questions RENAME TO questions_backup")
		if err != nil {
			t.Fatalf("failed to rename table: %v", err)
		}

		testQuiz := newTestQuizzes()[0]

		err = quizStore.CreateQuiz(t.Context(), testQuiz)
		if err == nil {
			t.Fatal("got nil, want error")
		}

		if got, want := err.Error(), "failed to handle questions"; !strings.Contains(err.Error(), want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("option insert error", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())

		// Rename options table to force an insert error
		_, err := db.ExecContext(t.Context(), "ALTER TABLE options RENAME TO options_backup")
		if err != nil {
			t.Fatalf("failed to rename table: %v", err)
		}

		testQuiz := newTestQuizzes()[0]
		err = quizStore.CreateQuiz(t.Context(), testQuiz)
		if err == nil {
			t.Fatal("got nil, want error")
		}

		if got, want := err.Error(), "failed to handle questions"; !strings.Contains(err.Error(), want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestSQLiteStore_UpdateQuiz(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	db := dbtest.Open(t)

	quizStore := NewQuizStore(db, logger)

	originalQuiz := newTestQuizzes()[0]

	// Create the original quiz
	err := quizStore.CreateQuiz(t.Context(), originalQuiz)
	if err != nil {
		t.Fatalf("failed to create quiz: %v", err)
	}

	updatedQuiz := &quiz.Quiz{
		ID:          originalQuiz.ID,
		Title:       originalQuiz.Title + " Updated",
		Slug:        originalQuiz.Slug + "-updated",
		Description: originalQuiz.Description + " Updated",
		CreatedAt:   originalQuiz.CreatedAt,
		Questions: []*quiz.Question{
			{
				ID:     originalQuiz.Questions[0].ID,
				QuizID: originalQuiz.ID,
				Text:   originalQuiz.Questions[0].Text + " Updated",
				Options: []*quiz.Option{
					{
						ID:      originalQuiz.Questions[0].Options[1].ID,
						Text:    originalQuiz.Questions[0].Options[1].Text + " Updated",
						Correct: !originalQuiz.Questions[0].Options[1].Correct,
					},
					{
						ID:      originalQuiz.Questions[0].Options[2].ID,
						Text:    originalQuiz.Questions[0].Options[2].Text + " Updated",
						Correct: !originalQuiz.Questions[0].Options[2].Correct,
					},
					{
						Text: "Option Added",
					},
				},
			},
		},
	}

	// Update the quiz
	err = quizStore.UpdateQuiz(t.Context(), updatedQuiz)
	if err != nil {
		t.Fatalf("failed to update quiz: %v", err)
	}

	// Get the updated quiz from the database for assertions
	qz, err := quizStore.GetQuiz(t.Context(), updatedQuiz.ID)
	if err != nil {
		t.Fatalf("failed to get quiz by ID: %v", err)
	}

	if qz == updatedQuiz {
		t.Fatalf("qz = %v, want different from updatedQuiz", qz)
	}
	if diff := cmp.Diff(qz, updatedQuiz,
		cmpopts.SortSlices(lessQuestions),
		cmpopts.SortSlices(lessOptions),
		cmpopts.EquateApproxTime(3*time.Second)); diff != "" {
		t.Errorf("quizzes diff (-got +want):\n%s", diff)
	}
}

func TestSQLiteStore_UpdateQuiz_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("bad quiz ID", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, logger)

		err := quizStore.UpdateQuiz(t.Context(), &quiz.Quiz{ID: 0})
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err, quiz.ErrCannotUpdateQuizWithIDZero; !errors.Is(got, want) {
			t.Errorf("err = %q, want %q", got, want)
		}
	})

	t.Run("update error", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		db := dbtest.Open(t)

		// Rename questions table to force an insert error
		_, err := db.ExecContext(t.Context(), "ALTER TABLE quizzes RENAME TO quizzes_backup")
		if err != nil {
			t.Fatalf("failed to rename table: %v", err)
		}

		quizStore := NewQuizStore(db, logger)

		testQuiz := existingTestQuizzes()[0]

		err = quizStore.UpdateQuiz(t.Context(), testQuiz)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "failed to update quiz"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("quiz not found", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, logger)

		qz := quiz.Quiz{ID: 123456789}
		err := quizStore.UpdateQuiz(t.Context(), &qz)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err, quiz.ErrUpdatingQuizNoRowsAffected; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("failed to handle questions", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, logger)

		originalQuiz := newTestQuizzes()[0]

		err := quizStore.CreateQuiz(t.Context(), originalQuiz)
		if err != nil {
			t.Fatalf("failed to create questions: %v", err)
		}

		// Rename questions table to force an insert error
		_, err = db.ExecContext(t.Context(), "ALTER TABLE questions RENAME TO questions_backup")
		if err != nil {
			t.Fatalf("failed to rename table: %v", err)
		}

		updatedQuiz := &quiz.Quiz{
			ID:          originalQuiz.ID,
			Title:       "Quiz 1",
			Slug:        "quiz-1",
			Description: "Description",
			Questions: []*quiz.Question{
				{
					Text: "Question 1 Updated",
				},
			},
		}

		err = quizStore.UpdateQuiz(t.Context(), updatedQuiz)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "failed to handle questions"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestSQLiteStore_CreateQuestion(t *testing.T) {
	t.Parallel()

	t.Run("create question", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, logger)

		existingQuizzes := newTestQuizzes()

		for _, qz := range existingQuizzes {
			err := quizStore.CreateQuiz(t.Context(), qz)
			if err != nil {
				t.Fatalf("failed to create quiz: %v", err)
			}
		}

		testQuestion := newTestQuestions()[0]
		testQuestion.QuizID = existingQuizzes[0].ID

		err := quizStore.CreateQuestion(t.Context(), testQuestion)
		if err != nil {
			t.Fatalf("failed to create question: %v", err)
		}

		qs, err := quizStore.GetQuestion(t.Context(), testQuestion.ID)
		if err != nil {
			t.Fatalf("failed to get question by ID: %v", err)
		}

		if diff := cmp.Diff(qs, testQuestion,
			cmpopts.SortSlices(lessOptions),
		); diff != "" {
			t.Errorf("questions diff (-got +want):\n%s", diff)
		}
		var count int
		err = db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM questions").Scan(&count)
		if err != nil {
			t.Fatalf("failed to count rows: %v", err)
		}
		if got, want := count, 5; got != want {
			t.Fatalf("count = %d, want %d", got, want)
		}
	})

	t.Run("ignore supplied option ID", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, logger)

		suppliedOptionID := int64(1000)

		testQuiz := newTestQuizzes()[0]

		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		testQuestion := newTestQuestions()[0]
		testQuestion.QuizID = testQuiz.ID
		testQuestion.Options[0].ID = suppliedOptionID

		err := quizStore.CreateQuestion(t.Context(), testQuestion)
		if err != nil {
			t.Fatalf("failed to create question: %v", err)
		}
		if testQuestion.Options[0].ID == suppliedOptionID {
			t.Error("option ID was not ignored")
		}
	})
}

func TestSQLiteStore_CreateQuestion_ErrorHandling(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	t.Run("fail on nonexisting quizID", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, logger)

		testQuestion := newTestQuestions()[0]

		err := quizStore.CreateQuestion(t.Context(), testQuestion)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		sqliteErr := &sqlite.Error{}
		if errors.As(err, &sqliteErr) {
			code := sqliteErr.Code()
			if got, want := code, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY; got != want {
				t.Fatalf("got error code %d, want %d", code, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY)
			}
		} else {
			t.Fatalf("got error %v, want sqlite.Error", err)
		}
	})

	t.Run("fail creating option with nonexisting questionID", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, logger)

		suppliedQuestionID := int64(1000)

		testQuestion := newTestQuestions()[0]
		testQuestion.ID = suppliedQuestionID

		err := quizStore.CreateQuestion(t.Context(), testQuestion)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		sqliteErr := &sqlite.Error{}
		if errors.As(err, &sqliteErr) {
			code := sqliteErr.Code()
			if got, want := code, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY; got != want {
				t.Fatalf("got error code %d, want %d", code, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY)
			}
		} else {
			t.Fatalf("got error %v, want sqlite error", err)
		}
	})
}

func TestSQLiteStore_UpdateQuestion(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	db := dbtest.Open(t)

	quizStore := NewQuizStore(db, logger)

	originalQuiz := newTestQuizzes()[0]

	if err := quizStore.CreateQuiz(t.Context(), originalQuiz); err != nil {
		t.Fatalf("failed to create quiz: %v", err)
	}

	updatedQuestion := &quiz.Question{
		ID:     originalQuiz.Questions[0].ID,
		QuizID: originalQuiz.ID,
		Text:   originalQuiz.Questions[0].Text + " Updated",
		Options: []*quiz.Option{
			{
				ID:      originalQuiz.Questions[0].Options[1].ID,
				Text:    originalQuiz.Questions[0].Options[1].Text + " Updated",
				Correct: true,
			},
			{
				ID:      originalQuiz.Questions[0].Options[2].ID,
				Text:    originalQuiz.Questions[0].Options[2].Text + " Updated",
				Correct: false,
			},
			{
				Text: "Option 1-4 Added",
			},
		},
	}

	err := quizStore.UpdateQuestion(t.Context(), updatedQuestion)
	if err != nil {
		t.Fatalf("failed to update question: %v", err)
	}

	qs, err := quizStore.GetQuestion(t.Context(), updatedQuestion.ID)
	if err != nil {
		t.Fatalf("failed to get question by ID: %v", err)
	}

	if diff := cmp.Diff(qs, updatedQuestion, cmpopts.SortSlices(lessOptions)); diff != "" {
		t.Errorf("questions diff (-got +want):\n%s", diff)
	}
}

func TestSQLiteStore_UpdateQuestion_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("bad question ID", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, logger)

		err := quizStore.UpdateQuestion(t.Context(), &quiz.Question{ID: 0})
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err, quiz.ErrCannotUpdateQuestionWithIDZero; !errors.Is(got, want) {
			t.Errorf("err = %q, want %q", got, want)
		}
	})

	t.Run("question not found", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, logger)

		testQuestion := &quiz.Question{
			ID:   123456789,
			Text: "Question 1",
		}
		err := quizStore.UpdateQuestion(t.Context(), testQuestion)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err, quiz.ErrUpdatingQuestionNoRowsAffected; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("update question error", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, logger)

		testQuiz := newTestQuizzes()[0]

		err := quizStore.CreateQuiz(t.Context(), testQuiz)
		if err != nil {
			t.Fatalf("failed to create question: %v", err)
		}

		// Rename questions table to force an insert error
		_, err = db.ExecContext(t.Context(), "ALTER TABLE questions RENAME TO questions_backup")
		if err != nil {
			t.Fatalf("failed to rename table: %v", err)
		}

		updatedQuestion := &quiz.Question{
			ID:   testQuiz.Questions[0].ID,
			Text: testQuiz.Questions[0].Text + " Updated",
		}

		err = quizStore.UpdateQuestion(t.Context(), updatedQuestion)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "failed to update question"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("update option error", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, logger)

		testQuiz := newTestQuizzes()[0]
		err := quizStore.CreateQuiz(t.Context(), testQuiz)
		if err != nil {
			t.Fatalf("failed to create question: %v", err)
		}

		// Rename questions table to force an insert error
		_, err = db.ExecContext(t.Context(), "ALTER TABLE options RENAME TO options_backup")
		if err != nil {
			t.Fatalf("failed to rename table: %v", err)
		}

		updatedQuestion := &quiz.Question{
			ID:   testQuiz.ID,
			Text: testQuiz.Questions[0].Text + " Updated",
			Options: []*quiz.Option{
				{
					ID:      testQuiz.Questions[0].Options[0].ID,
					Text:    testQuiz.Questions[0].Options[0].Text + " Updated",
					Correct: true,
				},
			},
		}

		err = quizStore.UpdateQuestion(t.Context(), updatedQuestion)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "failed to handle options"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("delete option error", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, logger)

		testQuiz := newTestQuizzes()[0]

		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		// Make deletes from options fail, while allowing updates/inserts to succeed.
		_, err := db.ExecContext(t.Context(), `
			CREATE TRIGGER options_no_delete
			BEFORE DELETE ON options
			BEGIN
				SELECT RAISE(ABORT, 'no deletes');
			END;
		`)
		if err != nil {
			t.Fatalf("failed to create trigger: %v", err)
		}

		// Keep the first option, drop the second, forces a DELETE during UpdateQuestion.
		updatedQuestion := &quiz.Question{
			ID:     testQuiz.Questions[0].ID,
			QuizID: testQuiz.ID,
			Text:   testQuiz.Questions[0].Text + " Updated",
			Options: []*quiz.Option{
				{
					ID:      testQuiz.Questions[0].Options[0].ID,
					Text:    testQuiz.Questions[0].Options[0].Text + " Updated",
					Correct: false,
				},
			},
		}

		err = quizStore.UpdateQuestion(t.Context(), updatedQuestion)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "failed to delete options"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
		if got, want := err.Error(), "no deletes"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}
