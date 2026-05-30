package store_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/quiz"
	. "github.com/starquake/topbanana/internal/store"
)

var (
	lessQuizzes   = func(a, b *quiz.Quiz) bool { return a.Title < b.Title }
	lessQuestions = func(a, b *quiz.Question) bool { return a.Text < b.Text }
	lessOptions   = func(a, b *quiz.Option) bool { return a.Text < b.Text }
)

// seededAdminID is the id of the admin row inserted by migration
// 20260111110308_add_admin_player.sql. Test fixtures attribute
// quizzes to this admin so the NOT NULL created_by_player_id FK
// from migration 20260520200000 is satisfied. The seed migration
// hard-codes id = 1, so reference the constant here rather than
// duplicating the literal in every fixture.
const (
	seededAdminID       int64 = 1
	seededAdminUsername       = "admin"
)

func newTestQuizzes() []*quiz.Quiz {
	return []*quiz.Quiz{
		{
			Title:             "Quiz 1",
			Slug:              "quiz-1",
			Description:       "Quiz 1 Description",
			CreatedByPlayerID: seededAdminID,
			CreatedByUsername: seededAdminUsername,
			// #99 / #103: the store normalises a zero TimeLimitSeconds
			// to the project-wide default and a blank Visibility to
			// public before INSERT; the fixture mirrors that so
			// cmp.Diff comparisons against rows read back from the DB
			// succeed without per-test ignore directives.
			TimeLimitSeconds: quiz.DefaultTimeLimitSeconds,
			Visibility:       quiz.VisibilityPublic,
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
			Title:             "Quiz 2",
			Slug:              "quiz-2",
			Description:       "Quiz 2 Description",
			CreatedByPlayerID: seededAdminID,
			CreatedByUsername: seededAdminUsername,
			TimeLimitSeconds:  quiz.DefaultTimeLimitSeconds,
			Visibility:        quiz.VisibilityPublic,
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
			ID:                1,
			Title:             "Quiz 1",
			Slug:              "quiz-1",
			Description:       "Quiz 1 Description",
			CreatedByPlayerID: seededAdminID,
			CreatedByUsername: seededAdminUsername,
			// #99 / #103: the store normalises a zero TimeLimitSeconds
			// to the project-wide default and a blank Visibility to
			// public before INSERT; the fixture mirrors that so
			// cmp.Diff comparisons against rows read back from the DB
			// succeed without per-test ignore directives.
			TimeLimitSeconds: quiz.DefaultTimeLimitSeconds,
			Visibility:       quiz.VisibilityPublic,
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
			Title:             "Quiz 2",
			Slug:              "quiz-2",
			Description:       "Quiz 2 Description",
			CreatedByPlayerID: seededAdminID,
			CreatedByUsername: seededAdminUsername,
			TimeLimitSeconds:  quiz.DefaultTimeLimitSeconds,
			Visibility:        quiz.VisibilityPublic,
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
	// Position 30 avoids colliding with the 10 / 20 positions
	// newTestQuizzes already uses when these questions get attached to
	// existing quizzes - the new UNIQUE(quiz_id, position) index from
	// #352 would otherwise reject the insert.
	return []*quiz.Question{
		{
			Text:     "Question 5",
			Position: 30,
			Options: []*quiz.Option{
				{Text: "Option 5-1"},
				{Text: "Option 5-2"},
				{Text: "Option 5-3", Correct: true},
				{Text: "Option 5-4"},
			},
		},
	}
}

func TestQuizStore_Ping(t *testing.T) {
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

func TestQuizStore_ListQuizzes(t *testing.T) {
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

	// ListQuizzes returns summaries - no questions or options loaded.
	summaries := make([]*quiz.Quiz, 0, len(testQuizzes))
	for _, qz := range testQuizzes {
		summaries = append(summaries, &quiz.Quiz{
			ID:                qz.ID,
			Title:             qz.Title,
			Slug:              qz.Slug,
			Description:       qz.Description,
			CreatedAt:         qz.CreatedAt,
			UpdatedAt:         qz.UpdatedAt,
			CreatedByPlayerID: qz.CreatedByPlayerID,
			CreatedByUsername: qz.CreatedByUsername,
			TimeLimitSeconds:  qz.TimeLimitSeconds,
			Visibility:        qz.Visibility,
		})
	}

	if diff := cmp.Diff(quizzes, summaries,
		cmpopts.SortSlices(lessQuizzes),
		cmpopts.EquateApproxTime(3*time.Second),
	); diff != "" {
		t.Errorf("quizzes diff (-got +want):\n%s", diff)
	}
}

func TestQuizStore_QuestionCountsByQuiz(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())

	// Two quizzes with different question counts; one with no questions
	// at all so we can assert it's absent from the map.
	withQuestions := &quiz.Quiz{
		Title: "With questions", Slug: "with-questions", Description: "x",
		CreatedByPlayerID: seededAdminID,
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "a", Correct: true}}},
			{Text: "Q2", Position: 2, Options: []*quiz.Option{{Text: "b"}}},
			{Text: "Q3", Position: 3, Options: []*quiz.Option{{Text: "c"}}},
		},
	}
	if err := quizStore.CreateQuiz(t.Context(), withQuestions); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}

	empty := &quiz.Quiz{Title: "Empty", Slug: "empty", Description: "y", CreatedByPlayerID: seededAdminID}
	if err := quizStore.CreateQuiz(t.Context(), empty); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}

	counts, err := quizStore.QuestionCountsByQuiz(t.Context())
	if err != nil {
		t.Fatalf("QuestionCountsByQuiz err = %v, want nil", err)
	}

	if got, want := counts[withQuestions.ID], 3; got != want {
		t.Errorf("counts[%d] = %d, want %d", withQuestions.ID, got, want)
	}
	if _, present := counts[empty.ID]; present {
		// A quiz with zero questions should not appear in the map at all;
		// callers treat the missing entry as 0.
		t.Errorf("empty quiz id %d should be absent from counts, got %d", empty.ID, counts[empty.ID])
	}
}

func TestQuizStore_ListQuizzes_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("context canceled", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := quizStore.ListQuizzes(ctx)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "context canceled"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestQuizStore_GetQuiz(t *testing.T) {
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

func TestQuizStore_GetQuiz_ErrorHandling(t *testing.T) {
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

func TestQuizStore_QuizExists(t *testing.T) {
	t.Parallel()

	t.Run("returns true for an existing quiz", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		exists, err := quizStore.QuizExists(t.Context(), testQuiz.ID)
		if err != nil {
			t.Fatalf("QuizExists err = %v, want nil", err)
		}
		if got, want := exists, true; got != want {
			t.Errorf("QuizExists = %v, want %v", got, want)
		}
	})

	t.Run("returns false for a missing quiz", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		exists, err := quizStore.QuizExists(t.Context(), 999)
		if err != nil {
			t.Fatalf("QuizExists err = %v, want nil", err)
		}
		if got, want := exists, false; got != want {
			t.Errorf("QuizExists = %v, want %v", got, want)
		}
	})
}

func TestQuizStore_GetQuestion(t *testing.T) {
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

func TestQuizStore_GetQuestion_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("context canceled", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())

		ctx, cancel := context.WithCancel(t.Context())
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

func TestQuizStore_CreateQuiz(t *testing.T) {
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

func TestQuizStore_CreateQuiz_ErrorHandling(t *testing.T) {
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

	t.Run("duplicate slug returns ErrSlugTaken", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		first := &quiz.Quiz{
			Title:             "Capitals of Europe",
			Slug:              "capitals-of-europe",
			Description:       "First insert.",
			CreatedByPlayerID: seededAdminID,
		}
		if err := quizStore.CreateQuiz(t.Context(), first); err != nil {
			t.Fatalf("first CreateQuiz err = %v, want nil", err)
		}

		clash := &quiz.Quiz{
			Title:             "Capitals of Europe",
			Slug:              "capitals-of-europe",
			Description:       "Second insert with the same slug - must surface ErrSlugTaken (#293).",
			CreatedByPlayerID: seededAdminID,
		}
		err := quizStore.CreateQuiz(t.Context(), clash)
		if err == nil {
			t.Fatal("got nil, want ErrSlugTaken")
		}
		if got, want := err, quiz.ErrSlugTaken; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestQuizStore_UpdateQuiz(t *testing.T) {
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
		ID:                originalQuiz.ID,
		Title:             originalQuiz.Title + " Updated",
		Slug:              originalQuiz.Slug + "-updated",
		Description:       originalQuiz.Description + " Updated",
		CreatedAt:         originalQuiz.CreatedAt,
		UpdatedAt:         originalQuiz.UpdatedAt,
		CreatedByPlayerID: originalQuiz.CreatedByPlayerID,
		CreatedByUsername: originalQuiz.CreatedByUsername,
		TimeLimitSeconds:  originalQuiz.TimeLimitSeconds,
		Visibility:        originalQuiz.Visibility,
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
		cmpopts.EquateApproxTime(3*time.Second),
		// UpdateQuiz assigns each question to the quiz's default group
		// (#444); the expected literal builds fresh questions and cannot
		// predict that id, so ignore RoundID here. The dedicated group
		// tests pin the assignment.
		cmpopts.IgnoreFields(quiz.Question{}, "RoundID")); diff != "" {
		t.Errorf("quizzes diff (-got +want):\n%s", diff)
	}
}

func TestQuizStore_UpdateQuiz_ErrorHandling(t *testing.T) {
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

func TestQuizStore_CreateQuestion(t *testing.T) {
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

func TestQuizStore_CreateQuestion_ErrorHandling(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	t.Run("fail on nonexisting quizID", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, logger)

		testQuestion := newTestQuestions()[0]

		// A question for a quiz that does not exist has no default group
		// to land in (#444): execCreateQuestion resolves the group before
		// the insert, so the failure surfaces there rather than as the
		// quiz_id FK violation it was before group_id existed.
		err := quizStore.CreateQuestion(t.Context(), testQuestion)
		if got, want := err, quiz.ErrRoundNotFound; !errors.Is(got, want) {
			t.Fatalf("err = %v, want %v", got, want)
		}
	})

	t.Run("fail creating question on nonexisting quiz with a supplied ID", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, logger)

		suppliedQuestionID := int64(1000)

		testQuestion := newTestQuestions()[0]
		testQuestion.ID = suppliedQuestionID

		// Same as above: the quiz does not exist, so default-group
		// resolution fails before any insert.
		err := quizStore.CreateQuestion(t.Context(), testQuestion)
		if got, want := err, quiz.ErrRoundNotFound; !errors.Is(got, want) {
			t.Fatalf("err = %v, want %v", got, want)
		}
	})
}

func TestQuizStore_UpdateQuestion(t *testing.T) {
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
		// UpdateQuestion does not move a question between groups (#444),
		// so the round it was created in (the quiz's default group) is
		// the value GetQuestion reads back.
		RoundID: originalQuiz.Questions[0].RoundID,
		Text:    originalQuiz.Questions[0].Text + " Updated",
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

func TestQuizStore_UpdateQuestion_ErrorHandling(t *testing.T) {
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

func TestQuizStore_ImageURL(t *testing.T) {
	t.Parallel()

	t.Run("create question persists image_url", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		q := &quiz.Question{
			QuizID:   testQuiz.ID,
			Text:     "What is shown in the image?",
			Position: 99,
			ImageURL: "https://example.com/image.png",
			Options:  []*quiz.Option{{Text: "A cat", Correct: true}},
		}
		if err := quizStore.CreateQuestion(t.Context(), q); err != nil {
			t.Fatalf("failed to create question: %v", err)
		}

		qs, err := quizStore.GetQuestion(t.Context(), q.ID)
		if err != nil {
			t.Fatalf("failed to get question: %v", err)
		}
		if got, want := qs.ImageURL, q.ImageURL; got != want {
			t.Errorf("GetQuestion ImageURL = %q, want %q", got, want)
		}
	})

	t.Run("update question persists image_url", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		original := testQuiz.Questions[0]
		updated := &quiz.Question{
			ID:       original.ID,
			QuizID:   testQuiz.ID,
			Text:     original.Text,
			Position: original.Position,
			ImageURL: "https://example.com/updated.png",
			Options:  original.Options,
		}
		if err := quizStore.UpdateQuestion(t.Context(), updated); err != nil {
			t.Fatalf("failed to update question: %v", err)
		}

		qs, err := quizStore.GetQuestion(t.Context(), original.ID)
		if err != nil {
			t.Fatalf("failed to get question: %v", err)
		}
		if got, want := qs.ImageURL, updated.ImageURL; got != want {
			t.Errorf("GetQuestion ImageURL = %q, want %q", got, want)
		}
	})

	t.Run("list questions includes image_url", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		testQuiz.Questions[0].ImageURL = "https://example.com/list.png"
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		questions, err := quizStore.ListQuestions(t.Context(), testQuiz.ID)
		if err != nil {
			t.Fatalf("failed to list questions: %v", err)
		}

		var found bool
		for _, q := range questions {
			if q.ID == testQuiz.Questions[0].ID {
				found = true
				if got, want := q.ImageURL, testQuiz.Questions[0].ImageURL; got != want {
					t.Errorf("ListQuestions ImageURL = %q, want %q", got, want)
				}
			}
		}
		if !found {
			t.Error("question not found in ListQuestions result")
		}
	})
}

func TestQuizStore_GetOptionsByIDs(t *testing.T) {
	t.Parallel()

	t.Run("returns all matching options", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		ids := []int64{
			testQuiz.Questions[0].Options[0].ID,
			testQuiz.Questions[0].Options[1].ID,
		}

		options, err := quizStore.GetOptionsByIDs(t.Context(), ids)
		if err != nil {
			t.Fatalf("failed to get options by IDs: %v", err)
		}

		if got, want := len(options), len(ids); got != want {
			t.Fatalf("len(options) = %d, want %d", got, want)
		}
	})

	t.Run("empty ids returns empty slice", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		options, err := quizStore.GetOptionsByIDs(t.Context(), []int64{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if got, want := len(options), 0; got != want {
			t.Fatalf("len(options) = %d, want %d", got, want)
		}
	})
}

func TestQuizStore_DeleteQuiz(t *testing.T) {
	t.Parallel()

	t.Run("delete quiz cascades to questions and options", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		if err := quizStore.DeleteQuiz(t.Context(), testQuiz.ID); err != nil {
			t.Fatalf("failed to delete quiz: %v", err)
		}

		_, err := quizStore.GetQuiz(t.Context(), testQuiz.ID)
		if got, want := err, quiz.ErrQuizNotFound; !errors.Is(got, want) {
			t.Fatalf("GetQuiz err = %v, want %v", got, want)
		}

		var questionCount int
		if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM questions WHERE quiz_id = ?", testQuiz.ID).
			Scan(&questionCount); err != nil {
			t.Fatalf("failed to count questions: %v", err)
		}
		if got, want := questionCount, 0; got != want {
			t.Fatalf("questions after quiz delete = %d, want %d", got, want)
		}
	})

	t.Run("quiz not found", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		err := quizStore.DeleteQuiz(t.Context(), 999999)
		if got, want := err, quiz.ErrDeletingQuizNoRowsAffected; !errors.Is(got, want) {
			t.Fatalf("DeleteQuiz err = %v, want %v", got, want)
		}
	})

	t.Run("delete quiz also wipes played games and their dependent rows", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		playerStore := NewPlayerStore(db, slog.Default())
		player, err := playerStore.CreateAnonymousPlayer(t.Context(), "anon-quiz-delete")
		if err != nil {
			t.Fatalf("failed to create player: %v", err)
		}

		// Stand up a played game: game + participant + question issued +
		// answer recorded. Without the cascade fix, deleting the quiz
		// would fail with FOREIGN KEY constraint failed because
		// game_answers.option_id and games.quiz_id both reference rows
		// the quiz delete touches.
		gameStore := NewGameStore(db, slog.Default())
		g := &game.Game{QuizID: testQuiz.ID}
		if err = gameStore.CreateGame(t.Context(), g); err != nil {
			t.Fatalf("failed to create game: %v", err)
		}
		if err = gameStore.CreateParticipant(
			t.Context(), &game.Participant{GameID: g.ID, PlayerID: player.ID, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("failed to create participant: %v", err)
		}
		now := time.Now().UTC().Truncate(time.Second)
		gq := &game.Question{
			GameID:     g.ID,
			QuestionID: testQuiz.Questions[0].ID,
			StartedAt:  now,
			ExpiredAt:  now.Add(10 * time.Second),
		}
		if err = gameStore.CreateQuestion(t.Context(), gq); err != nil {
			t.Fatalf("failed to create game question: %v", err)
		}
		if err = gameStore.CreateAnswer(t.Context(), &game.Answer{
			GameID:     g.ID,
			PlayerID:   player.ID,
			QuestionID: gq.ID,
			OptionID:   testQuiz.Questions[0].Options[0].ID,
		}); err != nil {
			t.Fatalf("failed to create answer: %v", err)
		}

		if err = quizStore.DeleteQuiz(t.Context(), testQuiz.ID); err != nil {
			t.Fatalf("DeleteQuiz err = %v, want nil", err)
		}

		assertCount := func(label, sqlStr string, arg any, want int) {
			t.Helper()
			row := db.QueryRowContext(t.Context(), sqlStr, arg)
			var got int
			if scanErr := row.Scan(&got); scanErr != nil {
				t.Fatalf("scan %s err = %v", label, scanErr)
			}
			if got != want {
				t.Errorf("%s count = %d, want %d", label, got, want)
			}
		}
		assertCount("game_answers", `SELECT COUNT(*) FROM game_answers WHERE game_id = ?`, g.ID, 0)
		assertCount("game_questions", `SELECT COUNT(*) FROM game_questions WHERE game_id = ?`, g.ID, 0)
		assertCount("game_participants", `SELECT COUNT(*) FROM game_participants WHERE game_id = ?`, g.ID, 0)
		assertCount("games", `SELECT COUNT(*) FROM games WHERE id = ?`, g.ID, 0)
		assertCount("questions", `SELECT COUNT(*) FROM questions WHERE quiz_id = ?`, testQuiz.ID, 0)
	})
}

func TestQuizStore_DeleteQuestion(t *testing.T) {
	t.Parallel()

	t.Run("delete question cascades to options", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		questionID := testQuiz.Questions[0].ID
		if err := quizStore.DeleteQuestion(t.Context(), questionID); err != nil {
			t.Fatalf("failed to delete question: %v", err)
		}

		_, err := quizStore.GetQuestion(t.Context(), questionID)
		if got, want := err, quiz.ErrQuestionNotFound; !errors.Is(got, want) {
			t.Fatalf("GetQuestion err = %v, want %v", got, want)
		}

		var optionCount int
		if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM options WHERE question_id = ?", questionID).
			Scan(&optionCount); err != nil {
			t.Fatalf("failed to count options: %v", err)
		}
		if got, want := optionCount, 0; got != want {
			t.Fatalf("options after question delete = %d, want %d", got, want)
		}
	})

	t.Run("question not found", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		err := quizStore.DeleteQuestion(t.Context(), 999999)
		if got, want := err, quiz.ErrDeletingQuestionNoRowsAffected; !errors.Is(got, want) {
			t.Fatalf("DeleteQuestion err = %v, want %v", got, want)
		}
	})

	t.Run("delete question also wipes played game_questions and game_answers for that question", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		playerStore := NewPlayerStore(db, slog.Default())
		player, err := playerStore.CreateAnonymousPlayer(t.Context(), "anon-question-delete")
		if err != nil {
			t.Fatalf("failed to create player: %v", err)
		}

		// Stand up a played game in which BOTH questions have been issued
		// and answered. Deleting question[0] must wipe its game_questions
		// and game_answers rows, but the rows tied to question[1] in the
		// same game must remain - that is the difference from the quiz
		// delete cascade. Without the FK cascade fix, the question delete
		// would fail with FOREIGN KEY constraint failed because
		// game_questions.question_id and game_answers.option_id both
		// reference rows the delete touches.
		gameStore := NewGameStore(db, slog.Default())
		g := &game.Game{QuizID: testQuiz.ID}
		if err = gameStore.CreateGame(t.Context(), g); err != nil {
			t.Fatalf("failed to create game: %v", err)
		}
		if err = gameStore.CreateParticipant(
			t.Context(), &game.Participant{GameID: g.ID, PlayerID: player.ID, QuizID: testQuiz.ID},
		); err != nil {
			t.Fatalf("failed to create participant: %v", err)
		}

		now := time.Now().UTC().Truncate(time.Second)
		gq0 := &game.Question{
			GameID:     g.ID,
			QuestionID: testQuiz.Questions[0].ID,
			StartedAt:  now,
			ExpiredAt:  now.Add(10 * time.Second),
		}
		if err = gameStore.CreateQuestion(t.Context(), gq0); err != nil {
			t.Fatalf("failed to create game question 0: %v", err)
		}
		if err = gameStore.CreateAnswer(t.Context(), &game.Answer{
			GameID:     g.ID,
			PlayerID:   player.ID,
			QuestionID: gq0.ID,
			OptionID:   testQuiz.Questions[0].Options[0].ID,
		}); err != nil {
			t.Fatalf("failed to create answer for question 0: %v", err)
		}

		gq1 := &game.Question{
			GameID:     g.ID,
			QuestionID: testQuiz.Questions[1].ID,
			StartedAt:  now,
			ExpiredAt:  now.Add(10 * time.Second),
		}
		if err = gameStore.CreateQuestion(t.Context(), gq1); err != nil {
			t.Fatalf("failed to create game question 1: %v", err)
		}
		if err = gameStore.CreateAnswer(t.Context(), &game.Answer{
			GameID:     g.ID,
			PlayerID:   player.ID,
			QuestionID: gq1.ID,
			OptionID:   testQuiz.Questions[1].Options[0].ID,
		}); err != nil {
			t.Fatalf("failed to create answer for question 1: %v", err)
		}

		question0ID := testQuiz.Questions[0].ID
		question1ID := testQuiz.Questions[1].ID
		if err = quizStore.DeleteQuestion(t.Context(), question0ID); err != nil {
			t.Fatalf("DeleteQuestion err = %v, want nil", err)
		}

		assertCount := func(label, sqlStr string, arg any, want int) {
			t.Helper()
			row := db.QueryRowContext(t.Context(), sqlStr, arg)
			var got int
			if scanErr := row.Scan(&got); scanErr != nil {
				t.Fatalf("scan %s err = %v", label, scanErr)
			}
			if got != want {
				t.Errorf("%s count = %d, want %d", label, got, want)
			}
		}

		// Rows for the deleted question are gone.
		assertCount(
			"game_questions for deleted question",
			`SELECT COUNT(*) FROM game_questions WHERE question_id = ?`, question0ID, 0,
		)
		assertCount(
			"game_answers for deleted question's game_question",
			`SELECT COUNT(*) FROM game_answers WHERE game_question_id = ?`, gq0.ID, 0,
		)

		// Rows for the OTHER question in the same game are untouched.
		// This is the bit that distinguishes the question delete from the
		// quiz delete: only the deleted question's chain is wiped.
		assertCount(
			"game_questions for sibling question",
			`SELECT COUNT(*) FROM game_questions WHERE question_id = ?`, question1ID, 1,
		)
		assertCount(
			"game_answers for sibling question's game_question",
			`SELECT COUNT(*) FROM game_answers WHERE game_question_id = ?`, gq1.ID, 1,
		)

		// The game itself and its participant survive - only the
		// per-question rows for the deleted question were dropped.
		assertCount("games", `SELECT COUNT(*) FROM games WHERE id = ?`, g.ID, 1)
		assertCount("game_participants", `SELECT COUNT(*) FROM game_participants WHERE game_id = ?`, g.ID, 1)
	})
}

// SQLite's CURRENT_TIMESTAMP has 1-second granularity, so the bump tests
// sleep just over a second between the baseline snapshot and the mutation
// to make sure the timestamp can advance.
const updatedAtBumpDelay = 1100 * time.Millisecond

func TestQuizStore_UpdateQuiz_BumpsUpdatedAt(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())

	original := newTestQuizzes()[0]
	if err := quizStore.CreateQuiz(t.Context(), original); err != nil {
		t.Fatalf("failed to create quiz: %v", err)
	}

	before := original.UpdatedAt
	time.Sleep(updatedAtBumpDelay)

	original.Title += " edited"
	if err := quizStore.UpdateQuiz(t.Context(), original); err != nil {
		t.Fatalf("failed to update quiz: %v", err)
	}

	got, err := quizStore.GetQuiz(t.Context(), original.ID)
	if err != nil {
		t.Fatalf("failed to get quiz: %v", err)
	}

	if !got.UpdatedAt.After(before) {
		t.Errorf("UpdatedAt = %v, want after %v", got.UpdatedAt, before)
	}
}

func TestQuizStore_CreateQuestion_BumpsParentQuizUpdatedAt(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())

	parent := newTestQuizzes()[0]
	if err := quizStore.CreateQuiz(t.Context(), parent); err != nil {
		t.Fatalf("failed to create quiz: %v", err)
	}

	before := parent.UpdatedAt
	time.Sleep(updatedAtBumpDelay)

	newQuestion := &quiz.Question{
		QuizID:   parent.ID,
		Text:     "New question",
		Position: 30,
		Options: []*quiz.Option{
			{Text: "A"},
			{Text: "B", Correct: true},
		},
	}
	if err := quizStore.CreateQuestion(t.Context(), newQuestion); err != nil {
		t.Fatalf("failed to create question: %v", err)
	}

	got, err := quizStore.GetQuiz(t.Context(), parent.ID)
	if err != nil {
		t.Fatalf("failed to get quiz: %v", err)
	}

	if !got.UpdatedAt.After(before) {
		t.Errorf("UpdatedAt = %v, want after %v", got.UpdatedAt, before)
	}
}

func TestQuizStore_UpdateQuestion_BumpsParentQuizUpdatedAt(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())

	parent := newTestQuizzes()[0]
	if err := quizStore.CreateQuiz(t.Context(), parent); err != nil {
		t.Fatalf("failed to create quiz: %v", err)
	}

	before := parent.UpdatedAt
	time.Sleep(updatedAtBumpDelay)

	question := parent.Questions[0]
	question.Text += " edited"
	if err := quizStore.UpdateQuestion(t.Context(), question); err != nil {
		t.Fatalf("failed to update question: %v", err)
	}

	got, err := quizStore.GetQuiz(t.Context(), parent.ID)
	if err != nil {
		t.Fatalf("failed to get quiz: %v", err)
	}

	if !got.UpdatedAt.After(before) {
		t.Errorf("UpdatedAt = %v, want after %v", got.UpdatedAt, before)
	}
}

func TestQuizStore_DeleteOption_BumpsParentQuizUpdatedAt(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())

	parent := newTestQuizzes()[0]
	if err := quizStore.CreateQuiz(t.Context(), parent); err != nil {
		t.Fatalf("failed to create quiz: %v", err)
	}

	before := parent.UpdatedAt
	time.Sleep(updatedAtBumpDelay)

	// Drop one option from the first question and update - this routes
	// through UpdateQuestion, which deletes orphaned options and so should
	// fire the option-delete trigger that bumps the parent quiz.
	question := parent.Questions[0]
	question.Options = question.Options[:len(question.Options)-1]
	if err := quizStore.UpdateQuestion(t.Context(), question); err != nil {
		t.Fatalf("failed to update question: %v", err)
	}

	got, err := quizStore.GetQuiz(t.Context(), parent.ID)
	if err != nil {
		t.Fatalf("failed to get quiz: %v", err)
	}

	if !got.UpdatedAt.After(before) {
		t.Errorf("UpdatedAt = %v, want after %v", got.UpdatedAt, before)
	}
}

func TestQuizStore_ListQuizzes_OrderedByUpdatedAtDesc(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())

	first := &quiz.Quiz{Title: "First", Slug: "first", Description: "first", CreatedByPlayerID: seededAdminID}
	if err := quizStore.CreateQuiz(t.Context(), first); err != nil {
		t.Fatalf("failed to create first quiz: %v", err)
	}

	time.Sleep(updatedAtBumpDelay)

	second := &quiz.Quiz{Title: "Second", Slug: "second", Description: "second", CreatedByPlayerID: seededAdminID}
	if err := quizStore.CreateQuiz(t.Context(), second); err != nil {
		t.Fatalf("failed to create second quiz: %v", err)
	}

	quizzes, err := quizStore.ListQuizzes(t.Context())
	if err != nil {
		t.Fatalf("failed to list quizzes: %v", err)
	}
	if got, want := len(quizzes), 2; got != want {
		t.Fatalf("len(quizzes) = %d, want %d", got, want)
	}
	// Most-recent first.
	if got, want := quizzes[0].Title, "Second"; got != want {
		t.Errorf("quizzes[0].Title = %q, want %q", got, want)
	}
	if got, want := quizzes[1].Title, "First"; got != want {
		t.Errorf("quizzes[1].Title = %q, want %q", got, want)
	}

	// Edit the older quiz and confirm it floats to the top.
	time.Sleep(updatedAtBumpDelay)
	first.Title = "First Edited"
	if updateErr := quizStore.UpdateQuiz(t.Context(), first); updateErr != nil {
		t.Fatalf("failed to update first quiz: %v", updateErr)
	}

	quizzes, err = quizStore.ListQuizzes(t.Context())
	if err != nil {
		t.Fatalf("failed to list quizzes: %v", err)
	}
	if got, want := quizzes[0].Title, "First Edited"; got != want {
		t.Errorf("quizzes[0].Title = %q, want %q", got, want)
	}
}

// seedQuizWithQuestions creates a quiz with `n` questions at positions
// 10, 20, ..., 10*n and returns it. Helper for the position-reorder
// tests so each subtest can start from a known order without rebuilding
// the fixture inline.
func seedQuizWithQuestions(t *testing.T, quizStore *QuizStore, n int) *quiz.Quiz {
	t.Helper()

	qz := &quiz.Quiz{
		Title:             "Reorder Quiz",
		Slug:              "reorder-quiz",
		Description:       "for reorder tests",
		CreatedByPlayerID: seededAdminID,
	}
	for i := 1; i <= n; i++ {
		qz.Questions = append(qz.Questions, &quiz.Question{
			Text:     fmt.Sprintf("Q%d", i),
			Position: i * 10,
			Options: []*quiz.Option{
				{Text: "A", Correct: true},
				{Text: "B"},
			},
		})
	}

	if err := quizStore.CreateQuiz(t.Context(), qz); err != nil {
		t.Fatalf("failed to seed quiz: %v", err)
	}

	return qz
}

func TestQuizStore_SwapQuestionPositions(t *testing.T) {
	t.Parallel()

	t.Run("swap down moves a question past its successor", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		qz := seedQuizWithQuestions(t, quizStore, 3) // Q1@10, Q2@20, Q3@30

		err := quizStore.SwapQuestionPositions(t.Context(), qz.ID, qz.Questions[0].ID, quiz.DirectionDown)
		if err != nil {
			t.Fatalf("SwapQuestionPositions err = %v, want nil", err)
		}

		// After swap Q1 should hold position 20 and Q2 should hold 10,
		// so listing by position yields Q2, Q1, Q3.
		listed, err := quizStore.ListQuestions(t.Context(), qz.ID)
		if err != nil {
			t.Fatalf("ListQuestions err = %v, want nil", err)
		}
		got := []string{listed[0].Text, listed[1].Text, listed[2].Text}
		want := []string{"Q2", "Q1", "Q3"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("order mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("swap up moves a question past its predecessor", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		qz := seedQuizWithQuestions(t, quizStore, 3)

		err := quizStore.SwapQuestionPositions(t.Context(), qz.ID, qz.Questions[2].ID, quiz.DirectionUp)
		if err != nil {
			t.Fatalf("SwapQuestionPositions err = %v, want nil", err)
		}

		listed, err := quizStore.ListQuestions(t.Context(), qz.ID)
		if err != nil {
			t.Fatalf("ListQuestions err = %v, want nil", err)
		}
		got := []string{listed[0].Text, listed[1].Text, listed[2].Text}
		want := []string{"Q1", "Q3", "Q2"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("order mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("swap up from the first question returns ErrQuestionAtTop", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		qz := seedQuizWithQuestions(t, quizStore, 3)

		err := quizStore.SwapQuestionPositions(t.Context(), qz.ID, qz.Questions[0].ID, quiz.DirectionUp)
		if got, want := err, quiz.ErrQuestionAtTop; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("swap down from the last question returns ErrQuestionAtBottom", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		qz := seedQuizWithQuestions(t, quizStore, 3)

		err := quizStore.SwapQuestionPositions(t.Context(), qz.ID, qz.Questions[2].ID, quiz.DirectionDown)
		if got, want := err, quiz.ErrQuestionAtBottom; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("invalid direction returns ErrInvalidDirection", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		qz := seedQuizWithQuestions(t, quizStore, 2)

		err := quizStore.SwapQuestionPositions(t.Context(), qz.ID, qz.Questions[0].ID, "sideways")
		if got, want := err, quiz.ErrInvalidDirection; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("unknown question ID returns ErrQuestionNotFound", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		qz := seedQuizWithQuestions(t, quizStore, 2)

		err := quizStore.SwapQuestionPositions(t.Context(), qz.ID, 9999, quiz.DirectionUp)
		if got, want := err, quiz.ErrQuestionNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}
