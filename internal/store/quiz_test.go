package store_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
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
	seededAdminID          int64 = 1
	seededAdminDisplayName       = "admin"
)

func newTestQuizzes() []*quiz.Quiz {
	return []*quiz.Quiz{
		{
			Title:                "Quiz 1",
			Slug:                 "quiz-1",
			Description:          "Quiz 1 Description",
			CreatedByPlayerID:    seededAdminID,
			CreatedByDisplayName: seededAdminDisplayName,
			// #99 / #103: the store normalises a zero TimeLimitSeconds
			// to the project-wide default and a blank Visibility to
			// public before INSERT; the fixture mirrors that so
			// cmp.Diff comparisons against rows read back from the DB
			// succeed without per-test ignore directives.
			TimeLimitSeconds: quiz.DefaultTimeLimitSeconds,
			Visibility:       quiz.VisibilityPublic,
			Mode:             quiz.ModeSolo,
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
			Title:                "Quiz 2",
			Slug:                 "quiz-2",
			Description:          "Quiz 2 Description",
			CreatedByPlayerID:    seededAdminID,
			CreatedByDisplayName: seededAdminDisplayName,
			TimeLimitSeconds:     quiz.DefaultTimeLimitSeconds,
			Visibility:           quiz.VisibilityPublic,
			Mode:                 quiz.ModeSolo,
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
			ID:                   1,
			Title:                "Quiz 1",
			Slug:                 "quiz-1",
			Description:          "Quiz 1 Description",
			CreatedByPlayerID:    seededAdminID,
			CreatedByDisplayName: seededAdminDisplayName,
			// #99 / #103: the store normalises a zero TimeLimitSeconds
			// to the project-wide default and a blank Visibility to
			// public before INSERT; the fixture mirrors that so
			// cmp.Diff comparisons against rows read back from the DB
			// succeed without per-test ignore directives.
			TimeLimitSeconds: quiz.DefaultTimeLimitSeconds,
			Visibility:       quiz.VisibilityPublic,
			Mode:             quiz.ModeSolo,
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
			Title:                "Quiz 2",
			Slug:                 "quiz-2",
			Description:          "Quiz 2 Description",
			CreatedByPlayerID:    seededAdminID,
			CreatedByDisplayName: seededAdminDisplayName,
			TimeLimitSeconds:     quiz.DefaultTimeLimitSeconds,
			Visibility:           quiz.VisibilityPublic,
			Mode:                 quiz.ModeSolo,
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
			ID:                   qz.ID,
			Title:                qz.Title,
			Slug:                 qz.Slug,
			Description:          qz.Description,
			CreatedAt:            qz.CreatedAt,
			UpdatedAt:            qz.UpdatedAt,
			CreatedByPlayerID:    qz.CreatedByPlayerID,
			CreatedByDisplayName: qz.CreatedByDisplayName,
			TimeLimitSeconds:     qz.TimeLimitSeconds,
			Visibility:           qz.Visibility,
			Mode:                 qz.Mode,
		})
	}

	if diff := cmp.Diff(quizzes, summaries,
		cmpopts.SortSlices(lessQuizzes),
		cmpopts.EquateApproxTime(3*time.Second),
	); diff != "" {
		t.Errorf("quizzes diff (-got +want):\n%s", diff)
	}
}

func TestQuizStore_ListLiveQuizzes(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.New(slog.DiscardHandler))

	soloQz := &quiz.Quiz{
		Title: "Solo One", Slug: "solo-one", Description: "x",
		CreatedByPlayerID: seededAdminID, Mode: quiz.ModeSolo,
	}
	liveA := &quiz.Quiz{
		Title: "Live A", Slug: "live-a", Description: "x",
		CreatedByPlayerID: seededAdminID, Mode: quiz.ModeSolo,
	}
	liveB := &quiz.Quiz{
		Title: "Live B", Slug: "live-b", Description: "x",
		CreatedByPlayerID: seededAdminID, Mode: quiz.ModeSolo,
	}
	for _, qz := range []*quiz.Quiz{soloQz, liveA, liveB} {
		if err := quizStore.CreateQuiz(t.Context(), qz); err != nil {
			t.Fatalf("CreateQuiz(%s) err = %v, want nil", qz.Title, err)
		}
	}
	// CreateQuiz defaults to solo regardless of the Mode field, so flip the two
	// live quizzes explicitly.
	for _, qz := range []*quiz.Quiz{liveA, liveB} {
		if err := quizStore.SetQuizMode(t.Context(), qz.ID, quiz.ModeLive); err != nil {
			t.Fatalf("SetQuizMode(%s, live) err = %v, want nil", qz.Title, err)
		}
	}

	quizzes, err := quizStore.ListLiveQuizzes(t.Context())
	if err != nil {
		t.Fatalf("ListLiveQuizzes err = %v, want nil", err)
	}

	titles := make([]string, 0, len(quizzes))
	for _, qz := range quizzes {
		if got, want := qz.Mode, quiz.ModeLive; got != want {
			t.Errorf("quiz %q Mode = %q, want %q", qz.Title, got, want)
		}
		titles = append(titles, qz.Title)
	}
	slices.Sort(titles)
	if got, want := titles, []string{"Live A", "Live B"}; !slices.Equal(got, want) {
		t.Errorf("live quiz titles = %v, want %v", got, want)
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

func TestQuizStore_RoundCountsByQuiz(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())

	// CreateQuiz seeds a default round (#444), so every quiz starts with
	// one. multiRound gets two more (three total); single keeps just the
	// seeded default (one).
	multiRound := &quiz.Quiz{
		Title: "Multi round", Slug: "multi-round", Description: "x",
		CreatedByPlayerID: seededAdminID,
	}
	if err := quizStore.CreateQuiz(t.Context(), multiRound); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}
	for i, title := range []string{"Round Two", "Round Three"} {
		if err := quizStore.CreateRound(t.Context(), &quiz.Round{
			QuizID: multiRound.ID, Position: 10 + i, Title: title,
		}); err != nil {
			t.Fatalf("CreateRound err = %v, want nil", err)
		}
	}

	single := &quiz.Quiz{
		Title: "Single round", Slug: "single-round", Description: "y",
		CreatedByPlayerID: seededAdminID,
	}
	if err := quizStore.CreateQuiz(t.Context(), single); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}

	counts, err := quizStore.RoundCountsByQuiz(t.Context())
	if err != nil {
		t.Fatalf("RoundCountsByQuiz err = %v, want nil", err)
	}

	if got, want := counts[multiRound.ID], 3; got != want {
		t.Errorf("counts[%d] = %d, want %d", multiRound.ID, got, want)
	}
	if got, want := counts[single.ID], 1; got != want {
		t.Errorf("counts[%d] = %d, want %d", single.ID, got, want)
	}
	// A quiz id with no rounds row is absent from the map; callers treat
	// the missing entry as 0.
	if _, present := counts[multiRound.ID+9999]; present {
		t.Error("unknown quiz id should be absent from counts")
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

func TestQuizStore_ListQuestions(t *testing.T) {
	t.Parallel()

	t.Run("groups options under their question in order", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		questions, err := quizStore.ListQuestions(t.Context(), testQuiz.ID)
		if err != nil {
			t.Fatalf("ListQuestions err = %v, want nil", err)
		}

		if got, want := len(questions), len(testQuiz.Questions); got != want {
			t.Fatalf("len(questions) = %d, want %d", got, want)
		}

		// ListQuestionsByQuizID orders by position, so the slice order is
		// stable and each question must carry exactly its own options.
		for i, qs := range questions {
			wantQS := testQuiz.Questions[i]
			if got, want := qs.ID, wantQS.ID; got != want {
				t.Errorf("question[%d].ID = %d, want %d", i, got, want)
			}

			if got, want := len(qs.Options), len(wantQS.Options); got != want {
				t.Fatalf("question[%d] len(Options) = %d, want %d", i, got, want)
			}
			for j, opt := range qs.Options {
				if got, want := opt.QuestionID, qs.ID; got != want {
					t.Errorf("question[%d].Options[%d].QuestionID = %d, want %d", i, j, got, want)
				}
				if got, want := opt.ID, wantQS.Options[j].ID; got != want {
					t.Errorf("question[%d].Options[%d].ID = %d, want %d", i, j, got, want)
				}
				if got, want := opt.Text, wantQS.Options[j].Text; got != want {
					t.Errorf("question[%d].Options[%d].Text = %q, want %q", i, j, got, want)
				}
			}
		}
	})

	t.Run("question with no options yields a non-nil empty slice", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		testQuiz.Questions = nil
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		optionless := &quiz.Question{
			QuizID:   testQuiz.ID,
			Text:     "Optionless question",
			Position: 10,
		}
		if err := quizStore.CreateQuestion(t.Context(), optionless); err != nil {
			t.Fatalf("failed to create optionless question: %v", err)
		}

		questions, err := quizStore.ListQuestions(t.Context(), testQuiz.ID)
		if err != nil {
			t.Fatalf("ListQuestions err = %v, want nil", err)
		}
		if got, want := len(questions), 1; got != want {
			t.Fatalf("len(questions) = %d, want %d", got, want)
		}
		if questions[0].Options == nil {
			t.Error("question.Options = nil, want non-nil empty slice")
		}
		if got, want := len(questions[0].Options), 0; got != want {
			t.Errorf("len(question.Options) = %d, want %d", got, want)
		}
	})
}

func TestQuizStore_GetQuizVisibility(t *testing.T) {
	t.Parallel()

	t.Run("returns the visibility of an existing quiz", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		testQuiz.Visibility = quiz.VisibilityPrivate
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		visibility, err := quizStore.GetQuizVisibility(t.Context(), testQuiz.ID)
		if err != nil {
			t.Fatalf("GetQuizVisibility err = %v, want nil", err)
		}
		if got, want := visibility, quiz.VisibilityPrivate; got != want {
			t.Errorf("GetQuizVisibility = %q, want %q", got, want)
		}
	})

	t.Run("returns ErrQuizNotFound for a missing quiz", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		_, err := quizStore.GetQuizVisibility(t.Context(), 999)
		if got, want := err, quiz.ErrQuizNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
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
		ID:                   originalQuiz.ID,
		Title:                originalQuiz.Title + " Updated",
		Slug:                 originalQuiz.Slug + "-updated",
		Description:          originalQuiz.Description + " Updated",
		CreatedAt:            originalQuiz.CreatedAt,
		UpdatedAt:            originalQuiz.UpdatedAt,
		CreatedByPlayerID:    originalQuiz.CreatedByPlayerID,
		CreatedByDisplayName: originalQuiz.CreatedByDisplayName,
		TimeLimitSeconds:     originalQuiz.TimeLimitSeconds,
		Visibility:           originalQuiz.Visibility,
		Mode:                 originalQuiz.Mode,
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

// modeOf reads a quiz's persisted play mode, failing the test if the read
// errors. Keeps the SetQuizMode assertions free of repeated GetQuiz plumbing.
func modeOf(t *testing.T, s *QuizStore, id int64) string {
	t.Helper()

	qz, err := s.GetQuiz(t.Context(), id)
	if err != nil {
		t.Fatalf("GetQuiz(%d) err = %v, want nil", id, err)
	}

	return qz.Mode
}

func TestQuizStore_SetQuizMode(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	t.Run("flips solo to live and back", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, logger)

		qz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), qz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}

		if err := quizStore.SetQuizMode(t.Context(), qz.ID, quiz.ModeLive); err != nil {
			t.Fatalf("SetQuizMode(live) err = %v, want nil", err)
		}
		if got, want := modeOf(t, quizStore, qz.ID), quiz.ModeLive; got != want {
			t.Errorf("Mode = %q, want %q", got, want)
		}

		if err := quizStore.SetQuizMode(t.Context(), qz.ID, quiz.ModeSolo); err != nil {
			t.Fatalf("SetQuizMode(solo) err = %v, want nil", err)
		}
		if got, want := modeOf(t, quizStore, qz.ID), quiz.ModeSolo; got != want {
			t.Errorf("Mode = %q, want %q", got, want)
		}
	})

	t.Run("rejects an unknown mode", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, logger)

		qz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), qz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}

		err := quizStore.SetQuizMode(t.Context(), qz.ID, "sideways")
		if got, want := err, quiz.ErrInvalidMode; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("missing quiz returns ErrQuizNotFound", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, logger)

		err := quizStore.SetQuizMode(t.Context(), 9999, quiz.ModeLive)
		if got, want := err, quiz.ErrQuizNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
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

// seedQuizMedia creates a media row in the given quiz's library and returns its
// id, so a question test can attach a real, same-quiz image (#937).
func seedQuizMedia(t *testing.T, db *sql.DB, quizID int64) int64 {
	t.Helper()

	created, err := NewMediaStore(db, slog.Default()).CreateMedia(t.Context(), newMediaRow(quizID))
	if err != nil {
		t.Fatalf("CreateMedia err = %v, want nil", err)
	}

	return created.ID
}

// TestQuizStore_SetQuestionMedia pins the media-only patch (#1113): it sets a
// question's image and audio ids plus the repeat flag without touching the
// question's text, position, or options, clears them on a nil patch, and returns
// the no-rows sentinel for an unknown id.
func TestQuizStore_SetQuestionMedia(t *testing.T) {
	t.Parallel()

	t.Run("sets and clears media references", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		mediaStore := NewMediaStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("CreateQuiz err = %v, want nil", err)
		}
		imageID := seedQuizMedia(t, db, testQuiz.ID)
		audioRow, err := mediaStore.CreateMedia(t.Context(), newAudioMediaRow(testQuiz.ID))
		if err != nil {
			t.Fatalf("CreateMedia (audio) err = %v, want nil", err)
		}

		question := testQuiz.Questions[0]
		originalText := question.Text
		originalOptionCount := len(question.Options)

		if err = quizStore.SetQuestionMedia(t.Context(), question.ID, &imageID, &audioRow.ID, true); err != nil {
			t.Fatalf("SetQuestionMedia err = %v, want nil", err)
		}

		gotQ, err := quizStore.GetQuestion(t.Context(), question.ID)
		if err != nil {
			t.Fatalf("GetQuestion err = %v, want nil", err)
		}
		if gotQ.ImageMediaID == nil || *gotQ.ImageMediaID != imageID {
			t.Errorf("ImageMediaID = %v, want %d", gotQ.ImageMediaID, imageID)
		}
		if gotQ.AudioMediaID == nil || *gotQ.AudioMediaID != audioRow.ID {
			t.Errorf("AudioMediaID = %v, want %d", gotQ.AudioMediaID, audioRow.ID)
		}
		if !gotQ.AudioRepeat {
			t.Error("AudioRepeat = false, want true")
		}
		// The patch must not rewrite the question's text or options.
		if got, want := gotQ.Text, originalText; got != want {
			t.Errorf("Text = %q, want %q (patch must not touch text)", got, want)
		}
		if got, want := len(gotQ.Options), originalOptionCount; got != want {
			t.Errorf("option count = %d, want %d (patch must not touch options)", got, want)
		}

		// A nil patch clears both references.
		if err = quizStore.SetQuestionMedia(t.Context(), question.ID, nil, nil, false); err != nil {
			t.Fatalf("SetQuestionMedia (clear) err = %v, want nil", err)
		}
		gotQ, err = quizStore.GetQuestion(t.Context(), question.ID)
		if err != nil {
			t.Fatalf("GetQuestion err = %v, want nil", err)
		}
		if gotQ.ImageMediaID != nil {
			t.Errorf("ImageMediaID = %v, want nil after clear", *gotQ.ImageMediaID)
		}
		if gotQ.AudioMediaID != nil {
			t.Errorf("AudioMediaID = %v, want nil after clear", *gotQ.AudioMediaID)
		}
		if gotQ.AudioRepeat {
			t.Error("AudioRepeat = true, want false after clear")
		}
	})

	t.Run("unknown id returns no-rows sentinel", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		err := quizStore.SetQuestionMedia(t.Context(), 999999, nil, nil, false)
		if got, want := err, quiz.ErrUpdatingQuestionNoRowsAffected; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestQuizStore_ImageMediaID(t *testing.T) {
	t.Parallel()

	t.Run("create question persists image_media_id", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}
		mediaID := seedQuizMedia(t, db, testQuiz.ID)

		q := &quiz.Question{
			QuizID:       testQuiz.ID,
			Text:         "What is shown in the image?",
			Position:     99,
			ImageMediaID: &mediaID,
			Options:      []*quiz.Option{{Text: "A cat", Correct: true}},
		}
		if err := quizStore.CreateQuestion(t.Context(), q); err != nil {
			t.Fatalf("failed to create question: %v", err)
		}

		qs, err := quizStore.GetQuestion(t.Context(), q.ID)
		if err != nil {
			t.Fatalf("failed to get question: %v", err)
		}
		if qs.ImageMediaID == nil {
			t.Fatalf("GetQuestion ImageMediaID = nil, want %d", mediaID)
		}
		if got, want := *qs.ImageMediaID, mediaID; got != want {
			t.Errorf("GetQuestion ImageMediaID = %d, want %d", got, want)
		}
	})

	t.Run("update question persists and clears image_media_id", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}
		mediaID := seedQuizMedia(t, db, testQuiz.ID)

		original := testQuiz.Questions[0]
		attached := &quiz.Question{
			ID:           original.ID,
			QuizID:       testQuiz.ID,
			Text:         original.Text,
			Position:     original.Position,
			ImageMediaID: &mediaID,
			Options:      original.Options,
		}
		if err := quizStore.UpdateQuestion(t.Context(), attached); err != nil {
			t.Fatalf("failed to update question: %v", err)
		}

		qs, err := quizStore.GetQuestion(t.Context(), original.ID)
		if err != nil {
			t.Fatalf("failed to get question: %v", err)
		}
		if qs.ImageMediaID == nil || *qs.ImageMediaID != mediaID {
			t.Fatalf("GetQuestion ImageMediaID = %v, want %d", qs.ImageMediaID, mediaID)
		}

		// A nil ImageMediaID on a later save clears the attachment (NULL).
		detached := &quiz.Question{
			ID:           original.ID,
			QuizID:       testQuiz.ID,
			Text:         original.Text,
			Position:     original.Position,
			ImageMediaID: nil,
			Options:      original.Options,
		}
		if err = quizStore.UpdateQuestion(t.Context(), detached); err != nil {
			t.Fatalf("failed to update question: %v", err)
		}
		qs, err = quizStore.GetQuestion(t.Context(), original.ID)
		if err != nil {
			t.Fatalf("failed to get question: %v", err)
		}
		if qs.ImageMediaID != nil {
			t.Errorf("GetQuestion ImageMediaID = %v, want nil after detach", *qs.ImageMediaID)
		}
	})

	t.Run("list questions includes image_media_id", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}
		mediaID := seedQuizMedia(t, db, testQuiz.ID)

		target := testQuiz.Questions[0]
		target.ImageMediaID = &mediaID
		if err := quizStore.UpdateQuestion(t.Context(), target); err != nil {
			t.Fatalf("failed to update question: %v", err)
		}

		questions, err := quizStore.ListQuestions(t.Context(), testQuiz.ID)
		if err != nil {
			t.Fatalf("failed to list questions: %v", err)
		}

		var found bool
		for _, q := range questions {
			if q.ID == target.ID {
				found = true
				if q.ImageMediaID == nil || *q.ImageMediaID != mediaID {
					t.Errorf("ListQuestions ImageMediaID = %v, want %d", q.ImageMediaID, mediaID)
				}
			}
		}
		if !found {
			t.Error("question not found in ListQuestions result")
		}
	})

	t.Run("deleting attached media clears the question's image_media_id", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		mediaStore := NewMediaStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}
		mediaID := seedQuizMedia(t, db, testQuiz.ID)

		target := testQuiz.Questions[0]
		target.ImageMediaID = &mediaID
		if err := quizStore.UpdateQuestion(t.Context(), target); err != nil {
			t.Fatalf("failed to update question: %v", err)
		}

		if err := mediaStore.DeleteMedia(t.Context(), mediaID); err != nil {
			t.Fatalf("DeleteMedia err = %v, want nil", err)
		}

		qs, err := quizStore.GetQuestion(t.Context(), target.ID)
		if err != nil {
			t.Fatalf("GetQuestion err = %v, want nil (question must survive media delete)", err)
		}
		if qs.ImageMediaID != nil {
			t.Errorf(
				"GetQuestion ImageMediaID = %v, want nil after image delete (ON DELETE SET NULL)",
				*qs.ImageMediaID,
			)
		}
	})
}

func TestQuizStore_AudioMediaID(t *testing.T) {
	t.Parallel()

	t.Run("create question persists audio_media_id alongside image_media_id", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}
		imageID := seedQuizMedia(t, db, testQuiz.ID)
		audioID := seedQuizMedia(t, db, testQuiz.ID)

		q := &quiz.Question{
			QuizID:       testQuiz.ID,
			Text:         "What is this sound?",
			Position:     99,
			ImageMediaID: &imageID,
			AudioMediaID: &audioID,
			Options:      []*quiz.Option{{Text: "A bell", Correct: true}},
		}
		if err := quizStore.CreateQuestion(t.Context(), q); err != nil {
			t.Fatalf("failed to create question: %v", err)
		}

		qs, err := quizStore.GetQuestion(t.Context(), q.ID)
		if err != nil {
			t.Fatalf("failed to get question: %v", err)
		}
		if qs.AudioMediaID == nil {
			t.Fatalf("GetQuestion AudioMediaID = nil, want %d", audioID)
		}
		if got, want := *qs.AudioMediaID, audioID; got != want {
			t.Errorf("GetQuestion AudioMediaID = %d, want %d", got, want)
		}
		// A question carries both an image and a sound independently.
		if qs.ImageMediaID == nil || *qs.ImageMediaID != imageID {
			t.Errorf("GetQuestion ImageMediaID = %v, want %d", qs.ImageMediaID, imageID)
		}
	})

	t.Run("update question persists and clears audio_media_id", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}
		audioID := seedQuizMedia(t, db, testQuiz.ID)

		original := testQuiz.Questions[0]
		attached := &quiz.Question{
			ID:           original.ID,
			QuizID:       testQuiz.ID,
			Text:         original.Text,
			Position:     original.Position,
			AudioMediaID: &audioID,
			Options:      original.Options,
		}
		if err := quizStore.UpdateQuestion(t.Context(), attached); err != nil {
			t.Fatalf("failed to update question: %v", err)
		}

		qs, err := quizStore.GetQuestion(t.Context(), original.ID)
		if err != nil {
			t.Fatalf("failed to get question: %v", err)
		}
		if qs.AudioMediaID == nil || *qs.AudioMediaID != audioID {
			t.Fatalf("GetQuestion AudioMediaID = %v, want %d", qs.AudioMediaID, audioID)
		}

		detached := &quiz.Question{
			ID:           original.ID,
			QuizID:       testQuiz.ID,
			Text:         original.Text,
			Position:     original.Position,
			AudioMediaID: nil,
			Options:      original.Options,
		}
		if err = quizStore.UpdateQuestion(t.Context(), detached); err != nil {
			t.Fatalf("failed to update question: %v", err)
		}
		qs, err = quizStore.GetQuestion(t.Context(), original.ID)
		if err != nil {
			t.Fatalf("failed to get question: %v", err)
		}
		if qs.AudioMediaID != nil {
			t.Errorf("GetQuestion AudioMediaID = %v, want nil after detach", *qs.AudioMediaID)
		}
	})

	t.Run("list questions includes audio_media_id", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}
		audioID := seedQuizMedia(t, db, testQuiz.ID)

		target := testQuiz.Questions[0]
		target.AudioMediaID = &audioID
		if err := quizStore.UpdateQuestion(t.Context(), target); err != nil {
			t.Fatalf("failed to update question: %v", err)
		}

		questions, err := quizStore.ListQuestions(t.Context(), testQuiz.ID)
		if err != nil {
			t.Fatalf("failed to list questions: %v", err)
		}

		var found bool
		for _, q := range questions {
			if q.ID == target.ID {
				found = true
				if q.AudioMediaID == nil || *q.AudioMediaID != audioID {
					t.Errorf("ListQuestions AudioMediaID = %v, want %d", q.AudioMediaID, audioID)
				}
			}
		}
		if !found {
			t.Error("question not found in ListQuestions result")
		}
	})

	t.Run("deleting attached sound clears the question's audio_media_id", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		mediaStore := NewMediaStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}
		audioID := seedQuizMedia(t, db, testQuiz.ID)

		target := testQuiz.Questions[0]
		target.AudioMediaID = &audioID
		if err := quizStore.UpdateQuestion(t.Context(), target); err != nil {
			t.Fatalf("failed to update question: %v", err)
		}

		if err := mediaStore.DeleteMedia(t.Context(), audioID); err != nil {
			t.Fatalf("DeleteMedia err = %v, want nil", err)
		}

		qs, err := quizStore.GetQuestion(t.Context(), target.ID)
		if err != nil {
			t.Fatalf("GetQuestion err = %v, want nil (question must survive sound delete)", err)
		}
		if qs.AudioMediaID != nil {
			t.Errorf(
				"GetQuestion AudioMediaID = %v, want nil after sound delete (ON DELETE SET NULL)",
				*qs.AudioMediaID,
			)
		}
	})
}

func TestQuizStore_AudioRepeat(t *testing.T) {
	t.Parallel()

	t.Run("create with audio_repeat true round-trips, update to false clears it", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}
		audioID := seedQuizMedia(t, db, testQuiz.ID)

		q := &quiz.Question{
			QuizID:       testQuiz.ID,
			Text:         "Name that tune",
			Position:     99,
			AudioMediaID: &audioID,
			AudioRepeat:  true,
			Options:      []*quiz.Option{{Text: "A bell", Correct: true}},
		}
		if err := quizStore.CreateQuestion(t.Context(), q); err != nil {
			t.Fatalf("failed to create question: %v", err)
		}

		qs, err := quizStore.GetQuestion(t.Context(), q.ID)
		if err != nil {
			t.Fatalf("failed to get question: %v", err)
		}
		if got, want := qs.AudioRepeat, true; got != want {
			t.Errorf("GetQuestion AudioRepeat = %v, want %v", got, want)
		}

		qs.AudioRepeat = false
		if err = quizStore.UpdateQuestion(t.Context(), qs); err != nil {
			t.Fatalf("failed to update question: %v", err)
		}
		qs, err = quizStore.GetQuestion(t.Context(), q.ID)
		if err != nil {
			t.Fatalf("failed to get question: %v", err)
		}
		if got, want := qs.AudioRepeat, false; got != want {
			t.Errorf("GetQuestion AudioRepeat = %v, want %v after update", got, want)
		}
	})

	t.Run("create without setting audio_repeat defaults to false", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		q := &quiz.Question{
			QuizID:   testQuiz.ID,
			Text:     "No repeat here",
			Position: 99,
			Options:  []*quiz.Option{{Text: "A bell", Correct: true}},
		}
		if err := quizStore.CreateQuestion(t.Context(), q); err != nil {
			t.Fatalf("failed to create question: %v", err)
		}

		qs, err := quizStore.GetQuestion(t.Context(), q.ID)
		if err != nil {
			t.Fatalf("failed to get question: %v", err)
		}
		if got, want := qs.AudioRepeat, false; got != want {
			t.Errorf("GetQuestion AudioRepeat = %v, want %v (default)", got, want)
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
		if err = gameStore.CreateQuestion(t.Context(), gq, false); err != nil {
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
		if err = gameStore.CreateQuestion(t.Context(), gq0, false); err != nil {
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
		if err = gameStore.CreateQuestion(t.Context(), gq1, false); err != nil {
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

// rewindQuizUpdatedAt walks a quiz's updated_at one minute backwards so
// the bump-and-ordering tests can assert strict timestamp ordering
// without paying for SQLite's 1-second CURRENT_TIMESTAMP granularity in
// real wall-clock sleeps.
func rewindQuizUpdatedAt(t *testing.T, db *sql.DB, quizID int64) time.Time {
	t.Helper()

	if _, err := db.ExecContext(
		t.Context(),
		`UPDATE quizzes SET updated_at = datetime(updated_at, '-60 seconds') WHERE id = ?`,
		quizID,
	); err != nil {
		t.Fatalf("failed to rewind updated_at for quiz %d: %v", quizID, err)
	}

	var rewound time.Time
	if err := db.QueryRowContext(
		t.Context(),
		`SELECT updated_at FROM quizzes WHERE id = ?`,
		quizID,
	).Scan(&rewound); err != nil {
		t.Fatalf("failed to read rewound updated_at for quiz %d: %v", quizID, err)
	}

	return rewound
}

func TestQuizStore_UpdateQuiz_BumpsUpdatedAt(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())

	original := newTestQuizzes()[0]
	if err := quizStore.CreateQuiz(t.Context(), original); err != nil {
		t.Fatalf("failed to create quiz: %v", err)
	}

	before := rewindQuizUpdatedAt(t, db, original.ID)

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

	before := rewindQuizUpdatedAt(t, db, parent.ID)

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

	before := rewindQuizUpdatedAt(t, db, parent.ID)

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

	before := rewindQuizUpdatedAt(t, db, parent.ID)

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

// TestQuizStore_UpdateQuestion_RejectsCrossQuestionOptionID: an option UPDATE
// targeting another question's option id affects no rows (#1165).
func TestQuizStore_UpdateQuestion_RejectsCrossQuestionOptionID(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())

	quizzes := newTestQuizzes()
	attacker, victim := quizzes[0], quizzes[1]
	if err := quizStore.CreateQuiz(t.Context(), attacker); err != nil {
		t.Fatalf("failed to create attacker quiz: %v", err)
	}
	if err := quizStore.CreateQuiz(t.Context(), victim); err != nil {
		t.Fatalf("failed to create victim quiz: %v", err)
	}

	victimOption := victim.Questions[0].Options[0]
	wantText := victimOption.Text
	wantCorrect := victimOption.Correct

	attackerQuestion := attacker.Questions[0]
	attackerQuestion.Options = []*quiz.Option{
		{ID: victimOption.ID, QuestionID: attackerQuestion.ID, Text: "HACKED", Correct: !wantCorrect},
	}

	err := quizStore.UpdateQuestion(t.Context(), attackerQuestion)
	if got, want := err, quiz.ErrUpdatingOptionNoRowsAffected; !errors.Is(got, want) {
		t.Fatalf("err = %v, want %v", got, want)
	}

	gotVictim, err := quizStore.GetQuestion(t.Context(), victim.Questions[0].ID)
	if err != nil {
		t.Fatalf("failed to get victim question: %v", err)
	}

	var found *quiz.Option
	for _, o := range gotVictim.Options {
		if o.ID == victimOption.ID {
			found = o

			break
		}
	}
	if found == nil {
		t.Fatalf("victim option %d was deleted", victimOption.ID)
	}
	if got, want := found.Text, wantText; got != want {
		t.Errorf("victim option Text = %q, want %q", got, want)
	}
	if got, want := found.Correct, wantCorrect; got != want {
		t.Errorf("victim option Correct = %v, want %v", got, want)
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

	// Rewind first so the next insert wins the most-recent slot even when
	// both rows land in the same CURRENT_TIMESTAMP second.
	rewindQuizUpdatedAt(t, db, first.ID)

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

	// Edit the older quiz and confirm it floats to the top. Rewind second
	// so the about-to-fire CURRENT_TIMESTAMP on first beats it.
	rewindQuizUpdatedAt(t, db, second.ID)
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

// roundQuizFixture is a quiz seeded with named rounds and questions for
// MoveQuestionToPosition tests. roundIDs and questionIDs are keyed by the
// names passed to seedRoundQuiz.
type roundQuizFixture struct {
	quiz        *quiz.Quiz
	roundIDs    map[string]int64
	questionIDs map[string]int64
}

// seedRoundQuiz builds a quiz whose rounds (in order) each hold the named
// questions listed for them. The auto-seeded default round is renamed to
// the first round's name so the quiz has exactly the requested rounds.
// Questions are created in the order given so their quiz-wide positions
// run 1..N down the rounds.
func seedRoundQuiz(
	t *testing.T, quizStore *QuizStore, rounds []string, byRound map[string][]string,
) roundQuizFixture {
	t.Helper()

	qz := &quiz.Quiz{
		Title:             "Round Move Quiz",
		Slug:              "round-move-quiz",
		Description:       "for position-move tests",
		CreatedByPlayerID: seededAdminID,
	}
	if err := quizStore.CreateQuiz(t.Context(), qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}

	roundIDs := map[string]int64{}
	deflt, err := quizStore.GetDefaultRound(t.Context(), qz.ID)
	if err != nil {
		t.Fatalf("GetDefaultRound err = %v", err)
	}
	for i, name := range rounds {
		if i == 0 {
			deflt.Title = name
			if err := quizStore.UpdateRound(t.Context(), deflt); err != nil {
				t.Fatalf("UpdateRound err = %v", err)
			}
			roundIDs[name] = deflt.ID

			continue
		}
		g := &quiz.Round{QuizID: qz.ID, Position: i, Title: name}
		if err := quizStore.CreateRound(t.Context(), g); err != nil {
			t.Fatalf("CreateRound err = %v", err)
		}
		roundIDs[name] = g.ID
	}

	questionIDs := map[string]int64{}
	pos := 0
	for _, roundName := range rounds {
		for _, qText := range byRound[roundName] {
			pos++
			qs := &quiz.Question{
				QuizID:   qz.ID,
				RoundID:  roundIDs[roundName],
				Text:     qText,
				Position: pos,
				Options:  []*quiz.Option{{Text: "A", Correct: true}},
			}
			if err := quizStore.CreateQuestion(t.Context(), qs); err != nil {
				t.Fatalf("CreateQuestion %q err = %v", qText, err)
			}
			questionIDs[qText] = qs.ID
		}
	}

	return roundQuizFixture{quiz: qz, roundIDs: roundIDs, questionIDs: questionIDs}
}

// assertQuestionLayout reloads the quiz and asserts the quiz-wide question
// order (by text), dense 1..N positions, and each question's round id.
func assertQuestionLayout(
	t *testing.T,
	quizStore *QuizStore,
	quizID int64,
	wantOrder []string,
	wantRoundByText map[string]int64,
) {
	t.Helper()
	listed, err := quizStore.ListQuestions(t.Context(), quizID)
	if err != nil {
		t.Fatalf("ListQuestions err = %v", err)
	}
	gotOrder := make([]string, 0, len(listed))
	for i, qs := range listed {
		gotOrder = append(gotOrder, qs.Text)
		if got, want := qs.Position, i+1; got != want {
			t.Errorf("question %q Position = %d, want %d (dense 1..N)", qs.Text, got, want)
		}
		if got, want := qs.RoundID, wantRoundByText[qs.Text]; got != want {
			t.Errorf("question %q RoundID = %d, want %d", qs.Text, got, want)
		}
	}
	if diff := cmp.Diff(wantOrder, gotOrder); diff != "" {
		t.Errorf("question order mismatch (-want +got):\n%s", diff)
	}
}

func TestQuizStore_MoveQuestionToPosition(t *testing.T) {
	t.Parallel()

	rounds := []string{"R1", "R2"}
	layout := map[string][]string{
		"R1": {"Q1", "Q2", "Q3"},
		"R2": {"Q4", "Q5"},
	}

	t.Run("within-round move to top", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		f := seedRoundQuiz(t, quizStore, rounds, layout)

		if err := quizStore.MoveQuestionToPosition(
			t.Context(), f.quiz.ID, f.questionIDs["Q3"], f.roundIDs["R1"], 1,
		); err != nil {
			t.Fatalf("MoveQuestionToPosition err = %v, want nil", err)
		}
		assertQuestionLayout(t, quizStore, f.quiz.ID,
			[]string{"Q3", "Q1", "Q2", "Q4", "Q5"},
			map[string]int64{
				"Q1": f.roundIDs["R1"], "Q2": f.roundIDs["R1"], "Q3": f.roundIDs["R1"],
				"Q4": f.roundIDs["R2"], "Q5": f.roundIDs["R2"],
			})
	})

	t.Run("within-round move to bottom", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		f := seedRoundQuiz(t, quizStore, rounds, layout)

		if err := quizStore.MoveQuestionToPosition(
			t.Context(), f.quiz.ID, f.questionIDs["Q1"], f.roundIDs["R1"], 3,
		); err != nil {
			t.Fatalf("MoveQuestionToPosition err = %v, want nil", err)
		}
		assertQuestionLayout(t, quizStore, f.quiz.ID,
			[]string{"Q2", "Q3", "Q1", "Q4", "Q5"},
			map[string]int64{
				"Q1": f.roundIDs["R1"], "Q2": f.roundIDs["R1"], "Q3": f.roundIDs["R1"],
				"Q4": f.roundIDs["R2"], "Q5": f.roundIDs["R2"],
			})
	})

	t.Run("within-round move to middle", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		f := seedRoundQuiz(t, quizStore, rounds, layout)

		if err := quizStore.MoveQuestionToPosition(
			t.Context(), f.quiz.ID, f.questionIDs["Q1"], f.roundIDs["R1"], 2,
		); err != nil {
			t.Fatalf("MoveQuestionToPosition err = %v, want nil", err)
		}
		assertQuestionLayout(t, quizStore, f.quiz.ID,
			[]string{"Q2", "Q1", "Q3", "Q4", "Q5"},
			map[string]int64{
				"Q1": f.roundIDs["R1"], "Q2": f.roundIDs["R1"], "Q3": f.roundIDs["R1"],
				"Q4": f.roundIDs["R2"], "Q5": f.roundIDs["R2"],
			})
	})

	t.Run("cross-round move changes round_id and renumbers", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		f := seedRoundQuiz(t, quizStore, rounds, layout)

		// Move Q2 (R1) into R2 at slot 1.
		if err := quizStore.MoveQuestionToPosition(
			t.Context(), f.quiz.ID, f.questionIDs["Q2"], f.roundIDs["R2"], 1,
		); err != nil {
			t.Fatalf("MoveQuestionToPosition err = %v, want nil", err)
		}
		assertQuestionLayout(t, quizStore, f.quiz.ID,
			[]string{"Q1", "Q3", "Q2", "Q4", "Q5"},
			map[string]int64{
				"Q1": f.roundIDs["R1"], "Q3": f.roundIDs["R1"],
				"Q2": f.roundIDs["R2"], "Q4": f.roundIDs["R2"], "Q5": f.roundIDs["R2"],
			})
	})

	t.Run("move into an empty round", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		f := seedRoundQuiz(t, quizStore, []string{"R1", "R2", "R3"}, map[string][]string{
			"R1": {"Q1", "Q2"},
			"R2": {"Q3"},
			"R3": {},
		})

		if err := quizStore.MoveQuestionToPosition(
			t.Context(), f.quiz.ID, f.questionIDs["Q1"], f.roundIDs["R3"], 1,
		); err != nil {
			t.Fatalf("MoveQuestionToPosition err = %v, want nil", err)
		}
		assertQuestionLayout(t, quizStore, f.quiz.ID,
			[]string{"Q2", "Q3", "Q1"},
			map[string]int64{
				"Q2": f.roundIDs["R1"], "Q3": f.roundIDs["R2"], "Q1": f.roundIDs["R3"],
			})
	})

	t.Run("out-of-range position clamps to round bottom", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		f := seedRoundQuiz(t, quizStore, rounds, layout)

		// newPosition 99 in R1 clamps to the end of R1's list.
		if err := quizStore.MoveQuestionToPosition(
			t.Context(), f.quiz.ID, f.questionIDs["Q1"], f.roundIDs["R1"], 99,
		); err != nil {
			t.Fatalf("MoveQuestionToPosition err = %v, want nil", err)
		}
		assertQuestionLayout(t, quizStore, f.quiz.ID,
			[]string{"Q2", "Q3", "Q1", "Q4", "Q5"},
			map[string]int64{
				"Q1": f.roundIDs["R1"], "Q2": f.roundIDs["R1"], "Q3": f.roundIDs["R1"],
				"Q4": f.roundIDs["R2"], "Q5": f.roundIDs["R2"],
			})
	})

	t.Run("unknown question returns ErrQuestionNotFound", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		f := seedRoundQuiz(t, quizStore, rounds, layout)

		err := quizStore.MoveQuestionToPosition(t.Context(), f.quiz.ID, 999999, f.roundIDs["R1"], 1)
		if got, want := err, quiz.ErrQuestionNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("foreign quiz returns ErrQuestionNotFound", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		f := seedRoundQuiz(t, quizStore, rounds, layout)

		err := quizStore.MoveQuestionToPosition(
			t.Context(), f.quiz.ID+1, f.questionIDs["Q1"], f.roundIDs["R1"], 1,
		)
		if got, want := err, quiz.ErrQuestionNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("foreign round returns ErrRoundNotFound", func(t *testing.T) {
		t.Parallel()
		quizStore := NewQuizStore(dbtest.Open(t), slog.Default())
		f := seedRoundQuiz(t, quizStore, rounds, layout)

		err := quizStore.MoveQuestionToPosition(
			t.Context(), f.quiz.ID, f.questionIDs["Q1"], 999999, 1,
		)
		if got, want := err, quiz.ErrRoundNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestQuizStore_GetOption(t *testing.T) {
	t.Parallel()

	t.Run("returns the stored option", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		want := testQuiz.Questions[0].Options[2]
		got, err := quizStore.GetOption(t.Context(), want.ID)
		if err != nil {
			t.Fatalf("GetOption err = %v, want nil", err)
		}
		if diff := cmp.Diff(got, want); diff != "" {
			t.Errorf("option diff (-got +want):\n%s", diff)
		}
	})

	t.Run("returns ErrOptionNotFound for a missing option", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		opt, err := quizStore.GetOption(t.Context(), 999)
		if got, want := err, quiz.ErrOptionNotFound; !errors.Is(got, want) {
			t.Errorf("GetOption err = %v, want %v", got, want)
		}
		if opt != nil {
			t.Errorf("option = %v, want nil", opt)
		}
	})

	t.Run("wraps the underlying error on a closed DB", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		if err := db.Close(); err != nil {
			t.Fatalf("failed to close database: %v", err)
		}

		_, err := quizStore.GetOption(t.Context(), 1)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "failed to get option"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestQuizStore_CreateQuestionAtNextPosition(t *testing.T) {
	t.Parallel()

	t.Run("appends after the highest existing position", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		// newTestQuizzes seeds positions 10 and 20, so the next slot is 21.
		qs := &quiz.Question{
			QuizID:  testQuiz.ID,
			Text:    "Appended question",
			Options: []*quiz.Option{{Text: "A", Correct: true}},
		}
		if err := quizStore.CreateQuestionAtNextPosition(t.Context(), qs); err != nil {
			t.Fatalf("CreateQuestionAtNextPosition err = %v, want nil", err)
		}
		if got, want := qs.Position, 21; got != want {
			t.Errorf("qs.Position = %d, want %d", got, want)
		}
	})

	t.Run("wraps the underlying error on a closed DB", func(t *testing.T) {
		t.Parallel()

		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("failed to close database: %v", err)
		}

		qs := &quiz.Question{QuizID: testQuiz.ID, Text: "doomed"}
		err := quizStore.CreateQuestionAtNextPosition(t.Context(), qs)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "failed to create question at next position"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}
