package quiz_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/starquake/topbanana/internal/logging"
	"github.com/starquake/topbanana/internal/quiz"
)

type failLastInsertIDResult struct{}

func (failLastInsertIDResult) LastInsertId() (int64, error) {
	return 0, errors.New("forced last insert ID error")
}

func (failLastInsertIDResult) RowsAffected() (int64, error) {
	return 1, nil
}

func TestSQLiteStore_ListQuizzes_MockTesting(t *testing.T) {
	t.Parallel()

	t.Run("quizRows has error", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
		}
		defer func() {
			if err = db.Close(); err != nil {
				t.Fatalf("error closing db: %v", err)
			}
			if err = mock.ExpectationsWereMet(); err != nil {
				t.Errorf("there were unfulfilled expectations: %s", err)
			}
		}()

		quizStore := quiz.NewSQLiteStore(db, logger)

		testError := errors.New("quizRows error")

		// Set up a mock that will return an error when the GetQuestionsByQuizIDSQL query is executed.
		quizRows := mock.NewRows([]string{"id", "title", "slug", "description", "created_at"}).
			AddRow(1, "Test Quiz 1", "test-quiz-1", "Test Description 1", 1234).
			AddRow(2, "Test Quiz 2", "test-quiz-2", "Test Description 2", 1234)
		quizRows.RowError(1, testError)
		mock.ExpectQuery(quiz.ListQuizzesSQL).WillReturnRows(quizRows)
		mock.ExpectClose()

		_, err = quizStore.ListQuizzes(t.Context())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, testError) {
			t.Fatalf("expected error to be %v, got %v", testError, err)
		}
	})
}

func TestSQLiteStore_CreateQuiz_MockTesting(t *testing.T) {
	t.Parallel()

	t.Run("lastInsertId fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
		}
		defer func() {
			if err = db.Close(); err != nil {
				t.Fatalf("error closing db: %v", err)
			}
			if err = mock.ExpectationsWereMet(); err != nil {
				t.Errorf("there were unfulfilled expectations: %s", err)
			}
		}()

		quizStore := quiz.NewSQLiteStore(db, logger)

		qz := &quiz.Quiz{
			Title:       "Test Quiz",
			Slug:        "test-qz",
			Description: "A description",
		}

		mock.ExpectExec("INSERT INTO quizzes").
			WithArgs(qz.Title, qz.Slug, qz.Description, sqlmock.AnyArg()).
			WillReturnResult(failLastInsertIDResult{})

		mock.ExpectClose()

		err = quizStore.CreateQuiz(context.Background(), qz)

		if err == nil {
			t.Fatal("expected an error, but got nil")
		}

		if got, want := err.Error(), "error getting last insert ID for quiz"; !strings.Contains(got, want) {
			t.Errorf("got %q, should contain %q", got, want)
		}
	})

	t.Run("question lastInsertId fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
		}
		defer func() {
			if err = db.Close(); err != nil {
				t.Fatalf("error closing db: %v", err)
			}
			if err = mock.ExpectationsWereMet(); err != nil {
				t.Errorf("there were unfulfilled expectations: %s", err)
			}
		}()

		quizStore := quiz.NewSQLiteStore(db, logger)

		qz := &quiz.Quiz{
			Title:       "Test Quiz",
			Slug:        "test-quiz",
			Description: "A description",
			Questions: []*quiz.Question{
				{
					Text:     "Question 1",
					Position: 10,
				},
			},
		}

		mock.ExpectExec("INSERT INTO quizzes").
			WithArgs(qz.Title, qz.Slug, qz.Description, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))

		mock.ExpectExec("INSERT INTO questions").
			WithArgs(1, qz.Questions[0].Text, qz.Questions[0].ImageURL, qz.Questions[0].Position).
			WillReturnResult(failLastInsertIDResult{})

		mock.ExpectClose()

		err = quizStore.CreateQuiz(context.Background(), qz)

		if err == nil {
			t.Fatal("expected an error, but got nil")
		}

		if got, want := err.Error(), "error getting last insert ID for question"; !strings.Contains(got, want) {
			t.Errorf("got %q, should contain %q", got, want)
		}
	})

	t.Run("option lastInsertId fails", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := logging.NewLogger(&buf)

		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
		}
		defer func() {
			if err = db.Close(); err != nil {
				t.Fatalf("error closing db: %v", err)
			}
			if err = mock.ExpectationsWereMet(); err != nil {
				t.Errorf("there were unfulfilled expectations: %s", err)
			}
		}()

		quizStore := quiz.NewSQLiteStore(db, logger)

		qz := &quiz.Quiz{
			Title:       "Test Quiz",
			Slug:        "test-quiz",
			Description: "A description",
			Questions: []*quiz.Question{
				{
					Text:     "Question 1",
					Position: 10,
					Options: []*quiz.Option{
						{Text: "Option 1"},
					},
				},
			},
		}

		mock.ExpectExec("INSERT INTO quizzes").
			WithArgs(qz.Title, qz.Slug, qz.Description, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))

		mock.ExpectExec("INSERT INTO questions").
			WithArgs(1, qz.Questions[0].Text, qz.Questions[0].ImageURL, qz.Questions[0].Position).
			WillReturnResult(sqlmock.NewResult(1, 1))

		mock.ExpectExec("INSERT INTO options").
			WithArgs(1, qz.Questions[0].Options[0].Text, qz.Questions[0].Options[0].Correct).
			WillReturnResult(failLastInsertIDResult{})

		mock.ExpectClose()

		err = quizStore.CreateQuiz(context.Background(), qz)

		if err == nil {
			t.Fatal("expected an error, but got nil")
		}

		if got, want := err.Error(), "error getting last insert ID for option"; !strings.Contains(got, want) {
			t.Errorf("got %q, should contain %q", got, want)
		}
	})
}
