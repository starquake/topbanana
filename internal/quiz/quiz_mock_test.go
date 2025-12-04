package quiz_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/starquake/topbanana/internal/logging"
	"github.com/starquake/topbanana/internal/quiz"
)

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

		testError := errors.New("quizRows error")

		// Set up a mock that will return an error when the GetQuestionsByQuizIDSQL query is executed.
		quizRows := mock.NewRows([]string{"id", "title", "slug", "description", "created_at"}).
			AddRow(1, "Test Quiz 1", "test-quiz-1", "Test Description 1", 1234).
			AddRow(2, "Test Quiz 2", "test-quiz-2", "Test Description 2", 1234)
		quizRows.RowError(1, testError)
		mock.ExpectQuery(quiz.ListQuizzesSQL).WillReturnRows(quizRows)
		mock.ExpectClose()

		quizStore := quiz.NewSQLiteStore(db, logger)
		_, err = quizStore.ListQuizzes(t.Context())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, testError) {
			t.Fatalf("expected error to be %v, got %v", testError, err)
		}
	})
}
