package quiz_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/pressly/goose/v3"
	"github.com/starquake/topbanana/internal/logging"
	"github.com/starquake/topbanana/internal/migrations"
	"github.com/starquake/topbanana/internal/quiz"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func setupTestDBWithMigrations(t *testing.T) *sql.DB {
	t.Helper()

	db := setupTestDBWithoutMigrations(t)

	goose.SetBaseFS(migrations.FS)
	err := goose.SetDialect("sqlite3")
	if err != nil {
		t.Fatalf("error setting dialect: %v", err)
	}
	err = goose.Up(db, ".")
	if err != nil {
		t.Fatalf("error running migrations: %v", err)
	}

	return db
}

func setupTestDBWithoutMigrations(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("error opening SQLite database: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), "PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("error enabling foreign keys: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	return db
}

var (
	lessQuizzes   = func(a, b *quiz.Quiz) bool { return a.Title < b.Title }
	lessQuestions = func(a, b *quiz.Question) bool { return a.Text < b.Text }
	lessOptions   = func(a, b *quiz.Option) bool { return a.Text < b.Text }
)

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

	t.Run("valid quiz ID", func(t *testing.T) {
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
						{Text: "Option 2"},
					},
				},
				{
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

		qz, err := quizStore.GetQuizByID(t.Context(), testQuiz.ID)
		if err != nil {
			t.Errorf("error getting testQuiz by ID: %v", err)
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
			t.Errorf("err.Error() = %q, should contain %q", got, want)
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
			t.Errorf("err.Error() = %q, should contain %q", got, want)
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
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestSQLiteStore_ListQuizzes(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	db := setupTestDBWithMigrations(t)

	quizStore := quiz.NewSQLiteStore(db, logger)
	testQuizzes := []*quiz.Quiz{
		{
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
						{Text: "Option 2"},
						{Text: "Option 3", Correct: true},
					},
				},
				{
					Text:     "Question 2",
					Position: 20,
					Options: []*quiz.Option{
						{Text: "Option 4"},
						{Text: "Option 5"},
						{Text: "Option 6", Correct: true},
					},
				},
			},
		},
		{
			Title:       "Quiz 2",
			Slug:        "quiz-2",
			Description: "Quiz 2 Description",
			CreatedAt:   time.Now().UTC(),
			Questions: []*quiz.Question{
				{
					Text:     "Question 3",
					Position: 10,
					Options: []*quiz.Option{
						{Text: "Option 7"},
						{Text: "Option 8"},
						{Text: "Option 9", Correct: true},
					},
				},
			},
		},
	}
	for _, testQz := range testQuizzes {
		err := quizStore.CreateQuiz(t.Context(), testQz)
		if err != nil {
			t.Fatalf("error creating quiz: %v", err)
		}
	}

	quizzes, err := quizStore.ListQuizzes(t.Context())
	if err != nil {
		t.Fatalf("error listing quizzes: %v", err)
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
			t.Fatalf("err.Error() = %q, should contain %q", got, want)
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
			t.Fatalf("err.Error() = %q, should contain %q", got, want)
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
		if diff := cmp.Diff(qs, testQuiz.Questions[0],
			cmpopts.SortSlices(lessOptions),
		); diff != "" {
			t.Errorf("question diff (-got +want):\n%s", diff)
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

	t.Run("context cancelled", func(t *testing.T) {
		t.Parallel()

		db := setupTestDBWithMigrations(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := quizStore.GetQuestionByID(ctx, 1)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "error iterating questionRow"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("scan error", func(t *testing.T) {
		t.Parallel()

		db := setupTestDBWithMigrations(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		testQuiz := &quiz.Quiz{
			Title:       "Quiz 1",
			Slug:        "quiz-1",
			Description: "Quiz 1 Description",
		}
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("error creating quiz: %v", err)
		}
		// Insert a question with an invalid position value (string instead of int64) to trigger scan error.
		res, err := db.ExecContext(
			t.Context(),
			`INSERT INTO questions (quiz_id, text, position) VALUES (?, ?, ?)`,
			testQuiz.ID,
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
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("option scan error", func(t *testing.T) {
		t.Parallel()

		db := setupTestDBWithMigrations(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		testQuiz := &quiz.Quiz{
			Title:       "Quiz 1",
			Slug:        "quiz-1",
			Description: "Quiz 1 Description",
			Questions: []*quiz.Question{
				{
					Text: "Question 1",
				},
			},
		}
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("error creating quiz: %v", err)
		}
		// Insert a quiz with an option with an invalid is_correct value (string instead of bool) to trigger a scan error.
		res, err := db.ExecContext(
			t.Context(),
			`INSERT INTO questions (quiz_id, text, position) VALUES (?, ?, ?)`,
			testQuiz.ID,
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
			testQuiz.Questions[0].ID,
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
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestSQLiteStore_CreateQuiz(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	db := setupTestDBWithMigrations(t)

	quizStore := quiz.NewSQLiteStore(db, logger)

	t.Run("quiz with questions and options", func(t *testing.T) {
		t.Parallel()
		testQuiz := &quiz.Quiz{
			Title:       "Quiz 1",
			Slug:        "quiz-1",
			Description: "Quiz 1 Description",
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

		qz, err := quizStore.GetQuizByID(t.Context(), testQuiz.ID)
		if err != nil {
			t.Errorf("error getting question by ID: %v", err)
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
		suppliedQuizID := int64(1000)
		suppliedQuestionID := int64(1001)
		suppliedOption1ID := int64(1002)
		suppliedOption2ID := int64(1003)

		testQuiz := &quiz.Quiz{
			ID:          suppliedQuizID,
			Title:       "Quiz 2",
			Slug:        "quiz-2",
			Description: "Quiz 2 Description",
			CreatedAt:   time.Now().UTC(),
			Questions: []*quiz.Question{
				{
					ID:       suppliedQuestionID,
					QuizID:   suppliedQuizID,
					Text:     "Question 1",
					Position: 10,
					Options: []*quiz.Option{
						{
							ID:         suppliedOption1ID,
							QuestionID: suppliedQuestionID,
							Text:       "Option 1",
						},
						{
							ID:         suppliedOption2ID,
							QuestionID: suppliedQuestionID,
							Text:       "Option 2",
						},
					},
				},
			},
		}

		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("error creating quiz: %v", err)
		}

		qz, err := quizStore.GetQuizByID(t.Context(), suppliedQuizID)
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

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	t.Run("insert error", func(t *testing.T) {
		t.Parallel()

		db := setupTestDBWithMigrations(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		// Rename questions table to force an insert error
		_, err := db.ExecContext(t.Context(), "ALTER TABLE questions RENAME TO questions_backup")
		if err != nil {
			t.Fatalf("failed to rename table: %v", err)
		}

		testQuiz := &quiz.Quiz{
			Title:       "Quiz 1",
			Slug:        "quiz-1",
			Description: "Description",
			Questions: []*quiz.Question{
				{
					Text: "Question 1",
				},
			},
		}

		err = quizStore.CreateQuiz(t.Context(), testQuiz)
		if err == nil {
			t.Fatal("got nil, want error")
		}

		if got, want := err.Error(), "error handling questions in transaction"; !strings.Contains(err.Error(), want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("option insert error", func(t *testing.T) {
		t.Parallel()

		db := setupTestDBWithMigrations(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		// Rename options table to force an insert error
		_, err := db.ExecContext(t.Context(), "ALTER TABLE options RENAME TO options_backup")
		if err != nil {
			t.Fatalf("failed to rename table: %v", err)
		}

		testQuiz := &quiz.Quiz{
			Title:       "Quiz 1",
			Slug:        "quiz-1",
			Description: "Description",
			Questions: []*quiz.Question{
				{
					Text: "Question 1",
					Options: []*quiz.Option{
						{Text: "Option 1"},
					},
				},
			},
		}

		err = quizStore.CreateQuiz(t.Context(), testQuiz)
		if err == nil {
			t.Fatal("got nil, want error")
		}

		if got, want := err.Error(), "error handling options in transaction"; !strings.Contains(err.Error(), want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestSQLiteStore_UpdateQuiz(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	t.Run("update quiz, remove questions and options", func(t *testing.T) {
		t.Parallel()

		db := setupTestDBWithMigrations(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		originalQuiz := &quiz.Quiz{
			Title:       "Quiz 1",
			Slug:        "quiz-1",
			Description: "Description",
			CreatedAt:   time.Now().UTC(),
			Questions: []*quiz.Question{
				{
					Text:     "Question 1",
					Position: 10,
					Options: []*quiz.Option{
						{Text: "Option 1-1"},
						{Text: "Option 1-2"},
						{Text: "Option 1-3"},
						{Text: "Option 1-4"},
					},
				},
				{
					Text:     "Question 2",
					Position: 20,
					Options: []*quiz.Option{
						{Text: "Option 2-1"},
						{Text: "Option 2-2"},
						{Text: "Option 2-3"},
						{Text: "Option 2-4"},
					},
				},
			},
		}

		// Create the original quiz
		err := quizStore.CreateQuiz(t.Context(), originalQuiz)
		if err != nil {
			t.Fatalf("error creating quiz: %v", err)
		}

		updatedQuiz := &quiz.Quiz{
			ID:          originalQuiz.ID,
			Title:       originalQuiz.Title + " Updated",
			Slug:        originalQuiz.Slug + " Updated",
			Description: originalQuiz.Description + " Updated",
			CreatedAt:   originalQuiz.CreatedAt,
			Questions: []*quiz.Question{
				{
					ID:       originalQuiz.Questions[0].ID,
					QuizID:   originalQuiz.Questions[0].QuizID,
					Text:     originalQuiz.Questions[0].Text + " Updated",
					Position: originalQuiz.Questions[0].Position + 10,
					Options: []*quiz.Option{
						{
							ID:         originalQuiz.Questions[0].Options[0].ID,
							QuestionID: originalQuiz.Questions[0].Options[0].QuestionID,
							Text:       originalQuiz.Questions[0].Options[0].Text + " Updated",
						},
						{
							ID:         originalQuiz.Questions[0].Options[1].ID,
							QuestionID: originalQuiz.Questions[0].Options[1].QuestionID,
							Text:       originalQuiz.Questions[0].Options[1].Text + " Updated",
						},
						{
							ID:         originalQuiz.Questions[0].Options[2].ID,
							QuestionID: originalQuiz.Questions[0].Options[2].QuestionID,
							Text:       originalQuiz.Questions[0].Options[2].Text + " Updated",
						},
					},
				},
			},
		}

		// Update the quiz
		err = quizStore.UpdateQuiz(t.Context(), updatedQuiz)
		if err != nil {
			t.Fatalf("error updating quiz: %v", err)
		}

		// Get the updated quiz from the database for assertions
		qz, err := quizStore.GetQuizByID(t.Context(), updatedQuiz.ID)
		if err != nil {
			t.Fatalf("error getting quiz by ID: %v", err)
		}

		if diff := cmp.Diff(qz, updatedQuiz,
			cmpopts.SortSlices(lessQuestions),
			cmpopts.SortSlices(lessOptions),
			cmpopts.EquateApproxTime(3*time.Second)); diff != "" {
			t.Errorf("quizzes diff (-got +want):\n%s", diff)
		}
	})

	t.Run("update quiz, add questions and options", func(t *testing.T) {
		t.Parallel()

		db := setupTestDBWithMigrations(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		originalQuiz := &quiz.Quiz{
			Title:       "Quiz 1",
			Slug:        "quiz-1",
			Description: "Description",
			CreatedAt:   time.Now().UTC(),
			Questions: []*quiz.Question{
				{
					Text:     "Question 1",
					Position: 10,
					Options: []*quiz.Option{
						{Text: "Option 1-1"},
						{Text: "Option 1-2"},
						{Text: "Option 1-3"},
					},
				},
			},
		}

		// Create the original quiz
		err := quizStore.CreateQuiz(t.Context(), originalQuiz)
		if err != nil {
			t.Fatalf("error creating quiz: %v", err)
		}

		updatedQuiz := &quiz.Quiz{
			ID:          originalQuiz.ID,
			Title:       originalQuiz.Title + " Updated",
			Slug:        originalQuiz.Slug + " Updated",
			Description: originalQuiz.Description + " Updated",
			CreatedAt:   originalQuiz.CreatedAt,
			Questions: []*quiz.Question{
				{
					ID:       originalQuiz.Questions[0].ID,
					QuizID:   originalQuiz.ID,
					Text:     originalQuiz.Questions[0].Text + " Updated",
					Position: originalQuiz.Questions[0].Position + 10,
					Options: []*quiz.Option{
						{
							ID:         originalQuiz.Questions[0].Options[0].ID,
							QuestionID: originalQuiz.Questions[0].Options[0].QuestionID,
							Text:       originalQuiz.Questions[0].Options[0].Text + " Updated",
						},
						{
							ID:         originalQuiz.Questions[0].Options[1].ID,
							QuestionID: originalQuiz.Questions[0].Options[1].QuestionID,
							Text:       originalQuiz.Questions[0].Options[1].Text + " Updated",
						},
						{
							ID:         originalQuiz.Questions[0].Options[2].ID,
							QuestionID: originalQuiz.Questions[0].Options[2].QuestionID,
							Text:       originalQuiz.Questions[0].Options[2].Text + " Updated",
						},
						{
							Text: "Option 1-4 Added",
						},
					},
				},
				{
					QuizID:   originalQuiz.ID,
					Text:     "Question 2 Added",
					Position: 20,
					Options: []*quiz.Option{
						{Text: "Option 2-1 Added"},
						{Text: "Option 2-2 Added"},
						{Text: "Option 2-3 Added"},
						{Text: "Option 2-4 Added"},
					},
				},
			},
		}

		// Update the quiz
		err = quizStore.UpdateQuiz(t.Context(), updatedQuiz)
		if err != nil {
			t.Fatalf("error updating quiz: %v", err)
		}

		// Get the updated quiz from the database for assertions
		qz, err := quizStore.GetQuizByID(t.Context(), updatedQuiz.ID)
		if err != nil {
			t.Fatalf("error getting quiz by ID: %v", err)
		}

		if diff := cmp.Diff(qz, updatedQuiz,
			cmpopts.SortSlices(lessQuestions),
			cmpopts.SortSlices(lessOptions),
			cmpopts.EquateApproxTime(3*time.Second)); diff != "" {
			t.Errorf("quizzes diff (-got +want):\n%s", diff)
		}
	})
}

func TestSQLiteStore_UpdateQuiz_ErrorHandling(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	t.Run("context cancelled", func(t *testing.T) {
		t.Parallel()
		// Create and cancel context to trigger an error
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		db := setupTestDBWithMigrations(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		qz := quiz.Quiz{}

		err := quizStore.UpdateQuiz(ctx, &qz)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "error starting transaction"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
	t.Run("update error", func(t *testing.T) {
		t.Parallel()
		db := setupTestDBWithMigrations(t)

		// Rename questions table to force an insert error
		_, err := db.ExecContext(t.Context(), "ALTER TABLE quizzes RENAME TO quizzes_backup")
		if err != nil {
			t.Fatalf("failed to rename table: %v", err)
		}

		quizStore := quiz.NewSQLiteStore(db, logger)

		testQuiz := &quiz.Quiz{
			Title:       "Quiz 1",
			Slug:        "quiz-1",
			Description: "Description",
			Questions: []*quiz.Question{
				{
					Text: "Question 1",
				},
			},
		}

		err = quizStore.UpdateQuiz(t.Context(), testQuiz)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "error updating quiz"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("quiz not found", func(t *testing.T) {
		t.Parallel()
		qz := quiz.Quiz{ID: 123456789}

		db := setupTestDBWithMigrations(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		err := quizStore.UpdateQuiz(t.Context(), &qz)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "no rows affected when updating quiz"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("error handling questions", func(t *testing.T) {
		t.Parallel()
		db := setupTestDBWithMigrations(t)

		// Rename questions table to force an insert error
		quizStore := quiz.NewSQLiteStore(db, logger)

		originalQuiz := &quiz.Quiz{
			Title:       "Quiz 1",
			Slug:        "quiz-1",
			Description: "Description",
			Questions: []*quiz.Question{
				{
					Text: "Question 1",
				},
			},
		}

		err := quizStore.CreateQuiz(t.Context(), originalQuiz)
		if err != nil {
			t.Fatalf("error creating quiz: %v", err)
		}

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
		if got, want := err.Error(), "error handling questions in transaction"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestSQLiteStore_CreateQuestion(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	t.Run("create question", func(t *testing.T) {
		t.Parallel()
		db := setupTestDBWithMigrations(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		testQuiz := &quiz.Quiz{
			Title:       "Quiz 1",
			Slug:        "quiz-1",
			Description: "Description",
		}

		err := quizStore.CreateQuiz(t.Context(), testQuiz)
		if err != nil {
			t.Fatalf("error creating quiz: %v", err)
		}

		testQuestion := &quiz.Question{
			QuizID: testQuiz.ID,
			Text:   "Question 1",
			Options: []*quiz.Option{
				{Text: "Option 1-1"},
				{Text: "Option 1-2"},
				{Text: "Option 1-3"},
			},
		}

		err = quizStore.CreateQuestion(t.Context(), testQuestion)
		if err != nil {
			t.Fatalf("error creating question: %v", err)
		}

		qs, err := quizStore.GetQuestionByID(t.Context(), testQuestion.ID)
		if err != nil {
			t.Fatalf("error getting question by ID: %v", err)
		}

		if diff := cmp.Diff(qs, testQuestion,
			cmpopts.SortSlices(lessOptions),
		); diff != "" {
			t.Errorf("questions diff (-got +want):\n%s", diff)
		}
	})

	t.Run("fail on nonexisting quizID", func(t *testing.T) {
		t.Parallel()
		db := setupTestDBWithMigrations(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		testQuestion := &quiz.Question{
			Text: "Question 1",
			Options: []*quiz.Option{
				{Text: "Option 1-1"},
				{Text: "Option 1-2"},
				{Text: "Option 1-3"},
			},
		}

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
		}
	})

	t.Run("fail on nonexisting questionID", func(t *testing.T) {
		t.Parallel()

		db := setupTestDBWithMigrations(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		suppliedQuestionID := int64(1000)

		testQuestion := &quiz.Question{
			ID:   suppliedQuestionID,
			Text: "Question 1",
			Options: []*quiz.Option{
				{
					QuestionID: suppliedQuestionID,
					Text:       "Option 1-1",
				},
				{
					QuestionID: suppliedQuestionID,
					Text:       "Option 1-2",
				},
			},
		}

		err := quizStore.CreateQuestion(t.Context(), testQuestion)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err, quiz.ErrUpdatingQuestionNoRowsAffected; !errors.Is(got, want) {
			t.Errorf("err = %q, want %q", got, want)
		}
	})

	t.Run("ignore supplied option ID", func(t *testing.T) {
		t.Parallel()

		db := setupTestDBWithMigrations(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		suppliedOptionID := int64(1000)

		testQuiz := &quiz.Quiz{
			Title:       "Quiz 1",
			Slug:        "quiz-1",
			Description: "Description",
		}

		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("error creating quiz: %v", err)
		}

		testQuestion := &quiz.Question{
			QuizID: testQuiz.ID,
			Text:   "Question 1",
			Options: []*quiz.Option{
				{
					ID:         suppliedOptionID,
					QuestionID: 1,
					Text:       "Option 1-1",
				},
			},
		}

		err := quizStore.CreateQuestion(t.Context(), testQuestion)
		if err != nil {
			t.Fatalf("error creating question: %v", err)
		}
		if testQuestion.Options[0].ID == suppliedOptionID {
			t.Error("option ID was not ignored")
		}
	})
}
