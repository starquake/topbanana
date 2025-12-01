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

	db := must.Any(sql.Open("sqlite", ":memory:"))
	db.SetMaxOpenConns(1)
	goose.SetBaseFS(migrations.FS)
	must.OK(goose.SetDialect("sqlite3"))
	must.OK(goose.Up(db, "."))

	quizStore := quiz.NewSQLiteStore(db, logger)

	err := quizStore.CreateQuiz(t.Context(), &quiz.Quiz{
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
	})
	if err != nil {
		t.Fatalf("error creating quiz: %v", err)
	}

	t.Run("valid quiz ID", func(t *testing.T) {
		t.Parallel()
		qz, err := quizStore.GetQuizByID(t.Context(), 1)
		if err != nil {
			t.Errorf("error getting qz by ID: %v", err)
		}
		if qz.ID != 1 {
			t.Errorf("quizID is not 1: %d", qz.ID)
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
	t.Run("context cancelled", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := quizStore.GetQuizByID(ctx, 1)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "error iterating quizRow") {
			t.Errorf("expected error to contain 'error iterating quizRow', got %v", err)
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
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "error scanning quizRow") {
			t.Errorf("expected error to contain 'error scanning quizRow', got %v", err)
		}
	})
}
