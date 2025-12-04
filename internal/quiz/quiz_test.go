package quiz_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/starquake/topbanana/internal/logging"
	"github.com/starquake/topbanana/internal/migrations"
	"github.com/starquake/topbanana/internal/must"
	"github.com/starquake/topbanana/internal/quiz"
	_ "modernc.org/sqlite"
)

func setupTestDBWithMigrations(t *testing.T) *sql.DB {
	t.Helper()

	db := setupTestDBWithoutMigrations(t)

	goose.SetBaseFS(migrations.FS)
	must.OK(goose.SetDialect("sqlite3"))
	must.OK(goose.Up(db, "."))

	return db
}

func setupTestDBWithoutMigrations(t *testing.T) *sql.DB {
	t.Helper()

	db := must.Any(sql.Open("sqlite", ":memory:"))
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	return db
}

func TestTimestamp_Scan(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		value   any
		want    quiz.Timestamp
		wantErr bool
	}{
		{
			name:    "valid timestamp",
			value:   int64(1764575476147),
			want:    quiz.Timestamp(time.Unix(1764575476, 147*int64(time.Millisecond))),
			wantErr: false,
		},
		{
			name:    "invalid timestamp",
			value:   "invalid",
			want:    quiz.Timestamp{},
			wantErr: true,
		},
		{
			name:    "nil timestamp",
			value:   nil,
			want:    quiz.Timestamp{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var ts quiz.Timestamp
			err := ts.Scan(tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("Scan() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestTimestamp_Value(t *testing.T) {
	t.Parallel()
	ts := quiz.Timestamp(time.Unix(1764575476, 147*int64(time.Millisecond)))
	got, err := ts.Value()
	if err != nil {
		t.Errorf("Value() error = %v", err)
	}
	want := int64(1764575476147)
	if got != want {
		t.Errorf("Value() = %v, want %v", got, want)
	}
}

func TestQuiz_Valid(t *testing.T) {
	t.Parallel()

	t.Run("valid quiz", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			quiz quiz.Quiz
		}{
			{
				name: "valid quiz without questions",
				quiz: quiz.Quiz{
					Title:       "Quiz 1",
					Slug:        "quiz-1",
					Description: "Quiz 1 Description",
				},
			},
			{
				name: "valid quiz with questions",
				quiz: quiz.Quiz{
					Title:       "Quiz 2",
					Slug:        "quiz-2",
					Description: "Quiz 2 Description",
					Questions: []*quiz.Question{
						{
							Text: "Question 1",
							Options: []*quiz.Option{
								{Text: "Option 1"},
								{Text: "Option 2"},
							},
						},
						{
							Text: "Question 2",
							Options: []*quiz.Option{
								{Text: "Option 3"},
								{Text: "Option 4"},
							},
						},
					},
				},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				if problems := tc.quiz.Valid(t.Context()); len(problems) > 0 {
					t.Errorf("quiz is not valid: %v", tc.quiz)
					for k, v := range problems {
						t.Errorf("  %s: %s", k, v)
					}
				}
			})
		}
	})

	t.Run("invalid quiz", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			quiz quiz.Quiz
		}{
			{
				name: "quiz without title",
				quiz: quiz.Quiz{
					Slug:        "quiz-1",
					Description: "Quiz 1 Description",
				},
			},
			{
				name: "quiz without slug",
				quiz: quiz.Quiz{
					Title:       "Quiz 2",
					Description: "Quiz 2 Description",
				},
			},
			{
				name: "quiz without description",
				quiz: quiz.Quiz{
					Title: "Quiz 3",
					Slug:  "quiz-3",
				},
			},
			{
				name: "valid quiz with invalid questions (no options)",
				quiz: quiz.Quiz{
					Title:       "Quiz 2",
					Slug:        "quiz-2",
					Description: "Quiz 2 Description",
					Questions: []*quiz.Question{
						{Text: "Question 1"},
						{Text: "Question 2"},
					},
				},
			},
			{
				name: "quiz with invalid question (no text)",
				quiz: quiz.Quiz{
					Title:       "Quiz 4",
					Slug:        "quiz-4",
					Description: "Quiz 4 Description",
					Questions: []*quiz.Question{
						{Text: ""},
					},
				},
			},
			{
				name: "quiz with question with invalid position",
				quiz: quiz.Quiz{
					Title:       "Quiz 5",
					Slug:        "quiz-5",
					Description: "Quiz 5 Description",
					Questions: []*quiz.Question{
						{Text: "Question 1", Position: -1},
					},
				},
			},
			{
				name: "quiz with question with invalid options",
				quiz: quiz.Quiz{
					Title:       "Quiz 6",
					Slug:        "quiz-6",
					Description: "Quiz 6 Description",
					Questions: []*quiz.Question{
						{Text: "Question 1", Options: []*quiz.Option{{Text: ""}}},
					},
				},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				if len(tc.quiz.Valid(t.Context())) == 0 {
					t.Errorf("quiz is valid: %v", tc.quiz)
				}
			})
		}
	})
}

func TestNewSQLiteStore(t *testing.T) {
	t.Parallel()

	store := quiz.NewSQLiteStore(&sql.DB{}, &logging.Logger{})
	if store == nil {
		t.Error("store is nil")
	}
}

func TestSQLiteStore_GetQuizByID(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	db := setupTestDBWithMigrations(t)

	quizStore := quiz.NewSQLiteStore(db, logger)

	testQuiz := &quiz.Quiz{
		Title:       "Quiz 1",
		Slug:        "quiz-1",
		Description: "Quiz 1 Description",
		Questions: []*quiz.Question{
			{
				ID:       1,
				Text:     "Question 1",
				Position: 10,
				Options: []*quiz.Option{
					{Text: "Option 1"},
					{Text: "Option 2"},
				},
			},
			{
				ID:       2,
				Text:     "Question 2",
				Position: 20,
				Options: []*quiz.Option{
					{Text: "Option 3"},
					{Text: "Option 4"},
				},
			},
		},
	}
	err := quizStore.CreateQuiz(t.Context(), testQuiz)
	if err != nil {
		t.Fatalf("error creating quiz: %v", err)
	}

	t.Run("valid quiz ID", func(t *testing.T) {
		t.Parallel()
		qz, err := quizStore.GetQuizByID(t.Context(), testQuiz.ID)
		if err != nil {
			t.Errorf("error getting testQuiz by ID: %v", err)
		}
		if got, want := qz.ID, testQuiz.ID; got != want {
			t.Errorf("qz.ID = %d, want %d", qz.ID, testQuiz.ID)
		}
	})

	t.Run("invalid quiz ID", func(t *testing.T) {
		t.Parallel()
		qz, err := quizStore.GetQuizByID(t.Context(), 999)
		if !errors.Is(err, quiz.ErrQuizNotFound) {
			t.Errorf("error is not sql.ErrNoRows: %v", err)
		}
		if qz != nil {
			t.Errorf("quiz is not nil: %v", qz)
		}
	})
}

func TestSQLiteStore_GetQuizByID_ErrorHandling(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	db := setupTestDBWithMigrations(t)

	quizStore := quiz.NewSQLiteStore(db, logger)

	t.Run("context cancelled", func(t *testing.T) {
		t.Parallel()
		// Create and cancel context to trigger an error
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := quizStore.GetQuizByID(ctx, 1)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "error iterating quizRow"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = '%v', should contain '%v'", got, want)
		}
	})

	t.Run("scan error", func(t *testing.T) {
		t.Parallel()
		// Insert a quiz with an invalid created_at value (string instead of int64) to trigger scan error.
		res, err := db.ExecContext(
			t.Context(),
			`INSERT INTO quizzes (title, slug, description, created_at) VALUES (?, ?, ?, ?)`,
			"Bad Quiz",
			"bad-quiz",
			"Bad Description",
			"not-a-timestamp",
		)
		if err != nil {
			t.Fatalf("error inserting bad quiz: %v", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("error getting last insert ID: %v", err)
		}

		_, err = quizStore.GetQuizByID(t.Context(), id)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "error scanning quizRow"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = '%v', should contain '%v'", got, want)
		}
	})

	t.Run("question scan error", func(t *testing.T) {
		t.Parallel()
		// Insert a quiz with a question with an invalid position value (string instead of int64) to trigger scan error.
		res, err := db.ExecContext(
			t.Context(),
			`INSERT INTO quizzes (title, slug, description, created_at) VALUES (?, ?, ?, ?)`,
			"Bad Quiz 2",
			"bad-quiz-2",
			"Bad Description 2",
			1234,
		)
		if err != nil {
			t.Fatalf("error inserting bad quiz: %v", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("error getting last insert ID: %v", err)
		}
		_, err = db.ExecContext(
			t.Context(),
			`INSERT INTO questions (quiz_id, text, position) VALUES (?, ?, ?)`,
			id,
			"Bad Question",
			"bad-position",
		)
		if err != nil {
			t.Fatalf("error inserting bad question: %v", err)
		}

		_, err = quizStore.GetQuizByID(t.Context(), id)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "error scanning questionRow"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = '%v', should contain '%v'", got, want)
		}
	})
}

func TestSQLiteStore_ListQuizzes(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	db := setupTestDBWithMigrations(t)

	quizStore := quiz.NewSQLiteStore(db, logger)
	err := quizStore.CreateQuiz(t.Context(), &quiz.Quiz{
		Title:       "Quiz 1",
		Slug:        "quiz-1",
		Description: "Quiz 1 Description",
	})
	if err != nil {
		t.Fatalf("error creating quiz: %v", err)
	}
	err = quizStore.CreateQuiz(t.Context(), &quiz.Quiz{
		Title:       "Quiz 2",
		Slug:        "quiz-2",
		Description: "Quiz 2 Description",
	})
	if err != nil {
		t.Fatalf("error creating quiz: %v", err)
	}

	quizzes, err := quizStore.ListQuizzes(t.Context())
	if err != nil {
		t.Fatalf("error listing quizzes: %v", err)
	}
	if got, want := len(quizzes), 2; got != want {
		t.Fatalf("len(quizzes) = %d, want %d", len(quizzes), want)
	}
}

func TestSQLiteStore_ListQuizzes_ErrorHandling(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	t.Run("context cancelled", func(t *testing.T) {
		t.Parallel()

		db := setupTestDBWithMigrations(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := quizStore.ListQuizzes(ctx)
		if err == nil {
			t.Fatal("got nil, want error")
		}
	})

	t.Run("scan error", func(t *testing.T) {
		t.Parallel()

		db := setupTestDBWithMigrations(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		// Insert a quiz with an invalid created_at value (string instead of int64) to trigger scan error.
		_, err := db.ExecContext(
			t.Context(),
			`INSERT INTO quizzes (title, slug, description, created_at) VALUES (?, ?, ?, ?)`,
			"Bad Quiz",
			"bad-quiz",
			"Bad Description",
			"not-a-timestamp",
		)
		if err != nil {
			t.Fatalf("error inserting bad quiz: %v", err)
		}
		_, err = quizStore.ListQuizzes(t.Context())
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "error scanning quizRow"; !strings.Contains(got, want) {
			t.Fatalf("err.Error() = '%v', should contain '%v'", got, want)
		}
	})

	t.Run("question scan error", func(t *testing.T) {
		t.Parallel()

		db := setupTestDBWithMigrations(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		// Insert a quiz with a question with an invalid position value (string instead of int64) to trigger scan error.
		_, err := db.ExecContext(
			t.Context(),
			`INSERT INTO quizzes (title, slug, description, created_at) VALUES (?, ?, ?, ?)`,
			"Bad Quiz 2",
			"bad-quiz-2",
			"Bad Description 2",
			1234,
		)
		if err != nil {
			t.Fatalf("error inserting bad quiz: %v", err)
		}
		_, err = db.ExecContext(
			t.Context(),
			`INSERT INTO questions (quiz_id, text, position) VALUES (?, ?, ?)`,
			1,
			"Bad Question",
			"bad-position",
		)
		if err != nil {
			t.Fatalf("error inserting bad question: %v", err)
		}
		quizzes, err := quizStore.ListQuizzes(t.Context())
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "error scanning questionRow"; !strings.Contains(got, want) {
			t.Fatalf("err.Error() = '%v', should contain '%v'", got, want)
		}
		if quizzes != nil {
			t.Errorf("quizzes = %v, want nil", quizzes)
		}
	})
}

func TestSQLiteStore_GetQuestionByID(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	db := setupTestDBWithMigrations(t)

	quizStore := quiz.NewSQLiteStore(db, logger)

	t.Run("valid question ID", func(t *testing.T) {
		t.Parallel()

		testQuiz := &quiz.Quiz{
			Title:       "Quiz 1",
			Slug:        "quiz-1",
			Description: "Quiz 1 Description",
			CreatedAt:   time.Now().UTC(),
			Questions: []*quiz.Question{
				{
					Text:     "Question 1",
					Position: 10,
					Options: []*quiz.Option{
						{Text: "Option 1"},
						{Text: "Option 2", Correct: true},
					},
				},
			},
		}

		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("error creating quiz: %v", err)
		}

		qs, err := quizStore.GetQuestionByID(t.Context(), testQuiz.Questions[0].ID)
		if err != nil {
			t.Errorf("error getting question by ID: %v", err)
		}
		if qs.ID == int64(0) {
			t.Errorf("qs.ID = %d, should not be 0", qs.ID)
		}
		if got, want := qs.QuizID, testQuiz.ID; got != want {
			t.Errorf("qs.QuizID = %d, want %d", got, want)
		}
		if got, want := qs.Text, "Question 1"; got != want {
			t.Errorf("qs.Text = %q, want %q", got, want)
		}
		if got, want := qs.Position, 10; got != want {
			t.Errorf("qs.Position = %d, want %d", got, want)
		}
		if got, want := len(qs.Options), 2; got != want {
			t.Errorf("len(qs.Options) = %d, want %d", got, want)
		}
		if got, want := qs.Options[0].Text, "Option 1"; got != want {
			t.Errorf("qs.Options[0].Text = %q, want %q", got, want)
		}
		if got, want := qs.Options[1].Text, "Option 2"; got != want {
			t.Errorf("qs.Options[1].Text = %q, want %q", got, want)
		}
	})

	t.Run("invalid question ID", func(t *testing.T) {
		t.Parallel()
		qs, err := quizStore.GetQuestionByID(t.Context(), 999)
		if !errors.Is(err, quiz.ErrQuestionNotFound) {
			t.Errorf("error is not sql.ErrNoRows: %v", err)
		}
		if qs != nil {
			t.Errorf("question is not nil: %v", qs)
		}
	})
}

func TestSQLiteStore_GetQuestionByID_ErrorHandling(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	db := setupTestDBWithMigrations(t)

	quizStore := quiz.NewSQLiteStore(db, logger)

	t.Run("context cancelled", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := quizStore.GetQuestionByID(ctx, 1)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "error iterating questionRow"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = '%v', should contain '%v'", got, want)
		}
	})

	t.Run("scan error", func(t *testing.T) {
		t.Parallel()
		// Insert a question with an invalid position value (string instead of int64) to trigger scan error.
		res, err := db.ExecContext(
			t.Context(),
			`INSERT INTO questions (quiz_id, text, position) VALUES (?, ?, ?)`,
			1,
			"Bad Question",
			"bad-position",
		)
		if err != nil {
			t.Fatalf("error inserting bad question: %v", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("error getting last insert ID: %v", err)
		}

		_, err = quizStore.GetQuestionByID(t.Context(), id)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "error scanning questionRow"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = '%v', should contain '%v'", got, want)
		}
	})

	t.Run("option scan error", func(t *testing.T) {
		t.Parallel()
		// Insert a quiz with an option with invalid is_correct value (string instead of bool) to trigger scan error.
		res, err := db.ExecContext(
			t.Context(),
			`INSERT INTO questions (quiz_id, text, position) VALUES (?, ?, ?)`,
			1,
			"Question 1",
			10,
		)
		if err != nil {
			t.Fatalf("error inserting bad option: %v", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("error getting last insert ID: %v", err)
		}
		_, err = db.ExecContext(
			t.Context(),
			`INSERT INTO options (id, question_id, text,is_correct) VALUES (?, ?, ?, ?)`,
			1,
			id,
			"Option 1",
			"Bad Boolean",
		)
		if err != nil {
			t.Fatalf("error inserting bad option: %v", err)
		}

		_, err = quizStore.GetQuestionByID(t.Context(), id)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "error scanning optionRow"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = '%v', should contain '%v'", got, want)
		}
	})
}

func TestSQLiteStore_CreateQuiz(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	db := setupTestDBWithMigrations(t)

	quizStore := quiz.NewSQLiteStore(db, logger)

	t.Run("quiz with questions", func(t *testing.T) {
		t.Parallel()
		testQuiz := &quiz.Quiz{
			Title:       "Quiz 1",
			Slug:        "quiz-1",
			Description: "Quiz 1 Description",
			CreatedAt:   time.Now().UTC(),
			Questions: []*quiz.Question{
				{
					Text:     "Question 1",
					Position: 10,
					Options: []*quiz.Option{
						{Text: "Option 1"},
						{Text: "Option 2", Correct: true},
					},
				},
			},
		}

		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("error creating quiz: %v", err)
		}

		qz, err := quizStore.GetQuizByID(t.Context(), testQuiz.Questions[0].ID)
		if err != nil {
			t.Errorf("error getting question by ID: %v", err)
		}
		if got, want := qz.ID, testQuiz.ID; got != want {
			t.Errorf("qz.QuizID = %d, want %d", got, want)
		}
		if got, want := qz.Title, testQuiz.Title; got != want {
			t.Errorf("qz.Title = %q, want %q", got, want)
		}
		if got, want := qz.Slug, testQuiz.Slug; got != want {
			t.Errorf("qz.Slug = %q, want %q", got, want)
		}
		if got, want := qz.Description, testQuiz.Description; got != want {
			t.Errorf("qz.Description = %q, want %q", got, want)
		}
		if got, want := testQuiz.CreatedAt.Sub(qz.CreatedAt), 3*time.Second; got > want {
			t.Errorf("testQuiz.CreatedAt.Sub(qz.CreatedAt) = %v, want %v", got, want)
		}
		if got, want := len(qz.Questions), 1; got != want {
			t.Errorf("len(qz.Questions) = %d, want %d", got, want)
		}
		for i, q := range qz.Questions {
			if got, want := q.ID, testQuiz.Questions[i].ID; got != want {
				t.Errorf("qz.Questions[%d].ID = %d, want %d", i, got, want)
			}
			if got, want := q.QuizID, testQuiz.ID; got != want {
				t.Errorf("qz.Questions[%d].QuizID = %d, want %d", i, got, want)
			}
			if got, want := q.Text, testQuiz.Questions[i].Text; got != want {
				t.Errorf("qz.Questions[%d].Text = %q, want %q", i, got, want)
			}
			if got, want := q.Position, testQuiz.Questions[i].Position; got != want {
				t.Errorf("qz.Questions[%d].Position = %d, want %d", i, got, want)
			}
			if got, want := len(q.Options), 2; got != want {
				t.Errorf("len(qz.Questions[%d].Options) = %d, want %d", i, got, want)
			}
			for j, o := range q.Options {
				if got, want := o.ID, testQuiz.Questions[i].Options[j].ID; got != want {
					t.Errorf("qz.Questions[%d].Options[%d].ID = %d, want %d", i, j, got, want)
				}
				if got, want := o.QuestionID, testQuiz.Questions[i].ID; got != want {
					t.Errorf("qz.Questions[%d].Options[%d].QuestionID = %d, want %d", i, j, got, want)
				}
				if got, want := o.Correct, testQuiz.Questions[i].Options[j].Correct; got != want {
					t.Errorf("qz.Questions[%d].Options[%d].Correct = %t, want %t", i, j, got, want)
				}
			}
		}
	})

	t.Run("ignore supplied ID's", func(t *testing.T) {
		t.Parallel()
		testQuiz := &quiz.Quiz{
			ID:          1234,
			Title:       "Quiz 2",
			Slug:        "quiz-2",
			Description: "Quiz 2 Description",
			Questions: []*quiz.Question{
				{
					ID:       5678,
					QuizID:   4321,
					Text:     "Question 1",
					Position: 10,
					Options: []*quiz.Option{
						{Text: "Option 1"},
						{Text: "Option 2"},
					},
				},
			},
		}

		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("error creating quiz: %v", err)
		}

		qz, err := quizStore.GetQuestionByID(t.Context(), testQuiz.Questions[0].ID)
		if err != nil {
			t.Errorf("error getting question by ID: %v", err)
		}
		if got, want := qz.QuizID, testQuiz.ID; got != want {
			t.Errorf("qz.QuizID = %d, want %d", got, want)
		}
	})
}
