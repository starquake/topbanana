package quiz_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
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

var (
	lessQuizzes   = func(a, b *quiz.Quiz) bool { return a.Title < b.Title }
	lessQuestions = func(a, b *quiz.Question) bool { return a.Text < b.Text }
	lessOptions   = func(a, b *quiz.Option) bool { return a.Text < b.Text }
)

// open opens a database connection with migrations applied.
func open(t *testing.T) *sql.DB {
	t.Helper()

	db := openUnmigrated(t)

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

// openUnmigrated opens a database connection without migrations applied.
func openUnmigrated(t *testing.T) *sql.DB {
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

	logger := logging.NewLogger(io.Discard)

	db := open(t)

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
		if got, want := err, quiz.ErrQuizNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
		if qz != nil {
			t.Errorf("quiz is not nil: %v", qz)
		}
	})
}

func TestSQLiteStore_GetQuizByID_ErrorHandling(t *testing.T) {
	t.Parallel()

	logger := logging.NewLogger(io.Discard)

	t.Run("context canceled", func(t *testing.T) {
		t.Parallel()

		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		// Create and cancel context to trigger an error
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := quizStore.GetQuizByID(ctx, 1)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "context canceled"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("scan error", func(t *testing.T) {
		t.Parallel()

		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

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
		if got, want := err.Error(), "error scanning row"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("question query error", func(t *testing.T) {
		t.Parallel()
	})

	t.Run("question scan error", func(t *testing.T) {
		t.Parallel()

		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

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

	logger := logging.NewLogger(io.Discard)

	db := open(t)

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

	logger := logging.NewLogger(io.Discard)

	t.Run("context canceled", func(t *testing.T) {
		t.Parallel()

		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

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

	t.Run("scan error", func(t *testing.T) {
		t.Parallel()

		db := open(t)

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
		if got, want := err.Error(), "error scanning row"; !strings.Contains(got, want) {
			t.Fatalf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("question scan error", func(t *testing.T) {
		t.Parallel()

		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		// Insert a quiz with a question with an invalid position value (string instead of int64) to trigger a scan error.
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

	logger := logging.NewLogger(io.Discard)

	db := open(t)

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
		if got, want := err, quiz.ErrQuestionNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
		if qs != nil {
			t.Errorf("question is not nil: %v", qs)
		}
	})
}

func TestSQLiteStore_GetQuestionByID_ErrorHandling(t *testing.T) {
	t.Parallel()

	logger := logging.NewLogger(io.Discard)

	t.Run("context canceled", func(t *testing.T) {
		t.Parallel()

		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := quizStore.GetQuestionByID(ctx, 1)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "context canceled"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("scan error", func(t *testing.T) {
		t.Parallel()

		db := open(t)

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
		if got, want := err.Error(), "error scanning row"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("option scan error", func(t *testing.T) {
		t.Parallel()

		db := open(t)

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

	db := open(t)

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

	t.Run("quiz insert error", func(t *testing.T) {
		t.Parallel()

		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		// Rename questions table to force an insert error
		_, err := db.ExecContext(t.Context(), "ALTER TABLE quizzes RENAME TO quizzes_backup")
		if err != nil {
			t.Fatalf("failed to rename table: %v", err)
		}

		testQuiz := &quiz.Quiz{
			Title:       "Quiz 1",
			Slug:        "quiz-1",
			Description: "Description",
		}

		err = quizStore.CreateQuiz(t.Context(), testQuiz)
		if err == nil {
			t.Fatal("got nil, want error")
		}

		if got, want := err.Error(), "error creating quiz"; !strings.Contains(err.Error(), want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("question insert error", func(t *testing.T) {
		t.Parallel()

		db := open(t)

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

		if got, want := err.Error(), "error handling questions"; !strings.Contains(err.Error(), want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("option insert error", func(t *testing.T) {
		t.Parallel()

		db := open(t)

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

		if got, want := err.Error(), "error handling questions"; !strings.Contains(err.Error(), want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestSQLiteStore_UpdateQuiz(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	db := open(t)

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
					{Text: "Option 1-2", Correct: true},
					{Text: "Option 1-3"},
					{Text: "Option 1-4"},
				},
			},
			{
				Text:     "Question 2",
				Position: 20,
				Options: []*quiz.Option{
					{Text: "Option 2-1"},
					{Text: "Option 2-2", Correct: true},
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

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	t.Run("bad quiz ID", func(t *testing.T) {
		t.Parallel()

		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

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
		db := open(t)

		// Rename questions table to force an insert error
		_, err := db.ExecContext(t.Context(), "ALTER TABLE quizzes RENAME TO quizzes_backup")
		if err != nil {
			t.Fatalf("failed to rename table: %v", err)
		}

		quizStore := quiz.NewSQLiteStore(db, logger)

		testQuiz := &quiz.Quiz{
			ID:          123456789,
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

		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		err := quizStore.UpdateQuiz(t.Context(), &qz)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err, quiz.ErrUpdatingQuizNoRowsAffected; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("error handling questions", func(t *testing.T) {
		t.Parallel()
		db := open(t)

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
		if got, want := err.Error(), "error handling questions"; !strings.Contains(got, want) {
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
		db := open(t)

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

	t.Run("ignore supplied option ID", func(t *testing.T) {
		t.Parallel()

		db := open(t)

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

func TestSQLiteStore_CreateQuestion_ErrorHandling(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	t.Run("fail on nonexisting quizID", func(t *testing.T) {
		t.Parallel()

		db := open(t)

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
		} else {
			t.Fatalf("got error %v, want sqlite.Error", err)
		}
	})

	t.Run("fail creating option with nonexisting questionID", func(t *testing.T) {
		t.Parallel()

		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		suppliedQuestionID := int64(1000)

		testQuestion := &quiz.Question{
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
	logger := logging.NewLogger(&buf)

	db := open(t)

	quizStore := quiz.NewSQLiteStore(db, logger)

	originalQuiz := &quiz.Quiz{
		Title:       "Quiz 1",
		Slug:        "quiz-1",
		Description: "Description",
		Questions: []*quiz.Question{
			{
				Text: "Question 1",
				Options: []*quiz.Option{
					{Text: "Option 1-1"},
					{Text: "Option 1-2"},
					{Text: "Option 1-3"},
				},
			},
		},
	}

	if err := quizStore.CreateQuiz(t.Context(), originalQuiz); err != nil {
		t.Fatalf("error creating quiz: %v", err)
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
		t.Fatalf("error updating question: %v", err)
	}

	qs, err := quizStore.GetQuestionByID(t.Context(), updatedQuestion.ID)
	if err != nil {
		t.Fatalf("error getting question by ID: %v", err)
	}

	if diff := cmp.Diff(qs, updatedQuestion, cmpopts.SortSlices(lessOptions)); diff != "" {
		t.Errorf("questions diff (-got +want):\n%s", diff)
	}
}

func TestSQLiteStore_UpdateQuestion_ErrorHandling(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	t.Run("bad question ID", func(t *testing.T) {
		t.Parallel()

		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

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

		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

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

		db := open(t)

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
		err := quizStore.CreateQuiz(t.Context(), testQuiz)
		if err != nil {
			t.Fatalf("error creating question: %v", err)
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
		if got, want := err.Error(), "error handling question"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("update option error", func(t *testing.T) {
		t.Parallel()

		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		testQuiz := &quiz.Quiz{
			Title:       "Quiz 1",
			Slug:        "quiz-1",
			Description: "Description",
			Questions: []*quiz.Question{
				{
					Text: "Question 1",
					Options: []*quiz.Option{
						{Text: "Option 1-1"},
						{Text: "Option 1-2"},
						{Text: "Option 1-3"},
					},
				},
			},
		}
		err := quizStore.CreateQuiz(t.Context(), testQuiz)
		if err != nil {
			t.Fatalf("error creating question: %v", err)
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
		if got, want := err.Error(), "error handling question"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("delete option error", func(t *testing.T) {
		t.Parallel()

		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		testQuiz := &quiz.Quiz{
			Title:       "Quiz 1",
			Slug:        "quiz-1",
			Description: "Description",
			Questions: []*quiz.Question{
				{
					Text: "Question 1",
					Options: []*quiz.Option{
						{Text: "Option 1-1"},
						{Text: "Option 1-2"},
					},
				},
			},
		}
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("error creating quiz: %v", err)
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
			t.Fatalf("error creating trigger: %v", err)
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
		if got, want := err.Error(), "error deleting options"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
		if got, want := err.Error(), "no deletes"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestSQLiteStore_WithTx(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	db := open(t)

	quizStore := quiz.NewSQLiteStore(db, logger)

	err := quizStore.WithTx(t.Context(), func(tx *sql.Tx) error {
		rows, qErr := tx.QueryContext(t.Context(), "SELECT 1")
		if qErr != nil {
			t.Fatalf("error querying database: %v", qErr)
		}
		defer func() {
			_ = rows.Close()
		}()
		if rows.Err() != nil {
			t.Fatalf("error reading rows: %v", rows.Err())
		}

		return nil
	})
	if err != nil {
		t.Fatalf("error executing query: %v", err)
	}
}

func TestSQLiteStore_WithTx_ErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("context canceled", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := quizStore.WithTx(ctx, func(tx *sql.Tx) error {
			rows, qErr := tx.QueryContext(ctx, "SELECT 1")
			if qErr != nil {
				t.Fatalf("error querying database: %v", qErr)
			}
			defer func() {
				_ = rows.Close()
			}()
			if rows.Err() != nil {
				t.Fatalf("error reading rows: %v", rows.Err())
			}

			return nil
		})
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "context canceled"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("fail triggers rollback", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		err := quizStore.WithTx(t.Context(), func(tx *sql.Tx) error {
			var err error

			_, err = tx.ExecContext(t.Context(), "CREATE TABLE test (id INTEGER PRIMARY KEY)")
			if err != nil {
				t.Fatalf("error creating table: %v", err)
			}
			_, err = tx.ExecContext(t.Context(), "INSERT INTO test (id) VALUES (1)")
			if err != nil {
				t.Fatalf("error inserting row: %v", err)
			}

			rows, err := tx.QueryContext(t.Context(), "SELECT foo FROM bar")
			if err != nil {
				return fmt.Errorf("error querying database: %w", err)
			}
			defer func() {
				_ = rows.Close()
			}()
			if rows.Err() != nil {
				t.Fatalf("error reading rows: %v", rows.Err())
			}

			return nil
		})
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "error executing transaction"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
		if got, want := buf.String(), "rollback transaction successful"; !strings.Contains(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("already triggered rollback", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		err := quizStore.WithTx(t.Context(), func(tx *sql.Tx) error {
			err := tx.Rollback()
			if err != nil {
				t.Fatalf("error rolling back transaction: %v", err)
			}

			return nil
		})
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(),
			"transaction has already been committed or rolled back"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestSQLiteStore_upsertQuestionInTx_ErrorHandling(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	t.Run("create error", func(t *testing.T) {
		t.Parallel()
		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		_, err := db.ExecContext(t.Context(), "ALTER TABLE questions RENAME TO questions_backup")
		if err != nil {
			t.Fatalf("failed to rename table: %v", err)
		}

		testQuestion := &quiz.Question{
			Text: "Question 1",
		}

		err = quizStore.WithTx(t.Context(), func(tx *sql.Tx) error {
			return quizStore.UpsertQuestionInTx(t.Context(), tx, testQuestion)
		})
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "error creating new question"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("update error", func(t *testing.T) {
		t.Parallel()
		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		_, err := db.ExecContext(t.Context(), "ALTER TABLE questions RENAME TO questions_backup")
		if err != nil {
			t.Fatalf("failed to rename table: %v", err)
		}

		testQuestion := &quiz.Question{
			ID:   1,
			Text: "Question 1",
		}

		err = quizStore.WithTx(t.Context(), func(tx *sql.Tx) error {
			return quizStore.UpsertQuestionInTx(t.Context(), tx, testQuestion)
		})
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "error updating question"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestSQLiteStore_deleteQuestionsInTx(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	db := open(t)

	quizStore := quiz.NewSQLiteStore(db, logger)

	_, err := db.ExecContext(t.Context(), "ALTER TABLE questions RENAME TO questions_backup")
	if err != nil {
		t.Fatalf("failed to rename table: %v", err)
	}
	err = quizStore.WithTx(t.Context(), func(tx *sql.Tx) error {
		return quizStore.DeleteQuestionsInTx(t.Context(), tx, []int64{1})
	})
	if err == nil {
		t.Fatal("got nil, want error")
	}
	if got, want := err.Error(), "error deleting question"; !strings.Contains(got, want) {
		t.Errorf("err.Error() = %q, should contain %q", got, want)
	}
}

func TestSQLiteStore_upsertOptionInTx_ErrorHandling(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	t.Run("create error", func(t *testing.T) {
		t.Parallel()
		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		_, err := db.ExecContext(t.Context(), "ALTER TABLE options RENAME TO options_backup")
		if err != nil {
			t.Fatalf("failed to rename table: %v", err)
		}

		testOption := &quiz.Option{
			Text: "Option 1",
		}

		err = quizStore.WithTx(t.Context(), func(tx *sql.Tx) error {
			return quizStore.UpsertOptionInTx(t.Context(), tx, testOption)
		})
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "error creating new option"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("update error", func(t *testing.T) {
		t.Parallel()
		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		_, err := db.ExecContext(t.Context(), "ALTER TABLE options RENAME TO options_backup")
		if err != nil {
			t.Fatalf("failed to rename table: %v", err)
		}

		testOption := &quiz.Option{
			ID:   1,
			Text: "Option 1",
		}

		err = quizStore.WithTx(t.Context(), func(tx *sql.Tx) error {
			return quizStore.UpsertOptionInTx(t.Context(), tx, testOption)
		})
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "error updating option"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestSQLiteStore_updateOptionInTx(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	option := quiz.Option{ID: 123456789}

	db := open(t)

	quizStore := quiz.NewSQLiteStore(db, logger)

	err := quizStore.WithTx(t.Context(), func(tx *sql.Tx) error {
		return quizStore.UpdateOptionInTx(t.Context(), tx, &option)
	})

	if err == nil {
		t.Fatal("got nil, want error")
	}
	if got, want := err, quiz.ErrUpdatingOptionNoRowsAffected; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestSQLiteStore_GetQuestionsByQuizID_ErrorHandling(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	t.Run("context canceled", func(t *testing.T) {
		t.Parallel()

		db := open(t)

		quizStore := quiz.NewSQLiteStore(db, logger)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := quizStore.GetQuestionsByQuizID(ctx, 1)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "context canceled"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("error getting options", func(t *testing.T) {
		t.Parallel()

		db := open(t)

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

		err := quizStore.CreateQuiz(t.Context(), testQuiz)
		if err != nil {
			t.Fatalf("error creating quiz: %v", err)
		}

		_, err = db.ExecContext(t.Context(), "ALTER TABLE options RENAME TO options_backup")
		if err != nil {
			t.Fatalf("failed to rename table: %v", err)
		}

		_, err = quizStore.GetQuestionsByQuizID(t.Context(), testQuiz.ID)
		if err == nil {
			t.Fatal("got nil, want error")
		}
		if got, want := err.Error(), "error getting options"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}
