package quiz_test

import (
	"bytes"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/starquake/topbanana/internal/quiz"
)

type failLastInsertIDResult struct{}

type failRowsAffectedResult struct{}

var errFailLastInsertID = errors.New("forced last insert ID error")

var errFailRowsAffected = errors.New("forced rows affected error")

func (failLastInsertIDResult) LastInsertId() (int64, error) {
	return 0, errFailLastInsertID
}

func (failLastInsertIDResult) RowsAffected() (int64, error) {
	return 1, nil
}

func (failRowsAffectedResult) LastInsertId() (int64, error) {
	return 1, nil
}

func (failRowsAffectedResult) RowsAffected() (int64, error) {
	return 0, errFailRowsAffected
}

func TestSQLiteStore_ListQuizzes_MockTesting_RowError(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer func() {
		if cErr := db.Close(); cErr != nil {
			t.Fatalf("error closing db: %v", cErr)
		}
		if mErr := mock.ExpectationsWereMet(); mErr != nil {
			t.Errorf("there were unfulfilled expectations: %s", mErr)
		}
	}()

	quizStore := quiz.NewSQLiteStore(db, logger)

	testError := errors.New("quizRows error")

	// Set up a mock that will return an error when the GetQuestionsByQuizIDSQL query is executed.
	quizRows := sqlmock.NewRows([]string{"id", "title", "slug", "description", "created_at"}).
		AddRow(1, "Test Quiz 1", "test-quiz-1", "Test Description 1", 1234).
		AddRow(2, "Test Quiz 2", "test-quiz-2", "Test Description 2", 1234)
	quizRows.RowError(1, testError)
	mock.ExpectQuery("SELECT (.*) FROM quizzes").WillReturnRows(quizRows)
	mock.ExpectClose()

	_, err = quizStore.ListQuizzes(t.Context())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, testError) {
		t.Fatalf("expected error to be %v, got %v", testError, err)
	}
}

func TestSQLiteStore_CreateQuiz_MockTesting(t *testing.T) {
	t.Parallel()

	t.Run("lastInsertId error", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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

		mock.ExpectBegin()

		mock.ExpectExec("INSERT INTO quizzes").
			WithArgs(qz.Title, qz.Slug, qz.Description, sqlmock.AnyArg()).
			WillReturnResult(failLastInsertIDResult{})

		mock.ExpectRollback()

		mock.ExpectClose()

		err = quizStore.CreateQuiz(t.Context(), qz)

		if err == nil {
			t.Fatal("expected an error, but got nil")
		}

		if got, want := err, errFailLastInsertID; !errors.Is(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("question lastInsertId error", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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

		mock.ExpectBegin()

		mock.ExpectExec("INSERT INTO quizzes").
			WithArgs(qz.Title, qz.Slug, qz.Description, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))

		mock.ExpectQuery("SELECT id FROM questions WHERE quiz_id").
			WithArgs(sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"id"}))

		mock.ExpectExec("INSERT INTO questions").
			WithArgs(1, qz.Questions[0].Text, qz.Questions[0].ImageURL, qz.Questions[0].Position).
			WillReturnResult(failLastInsertIDResult{})

		mock.ExpectRollback()

		mock.ExpectClose()

		err = quizStore.CreateQuiz(t.Context(), qz)

		if err == nil {
			t.Fatal("expected an error, but got nil")
		}

		if got, want := err, errFailLastInsertID; !errors.Is(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("option lastInsertId error", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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

		mock.ExpectBegin()

		mock.ExpectExec("INSERT INTO quizzes").
			WithArgs(qz.Title, qz.Slug, qz.Description, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))

		mock.ExpectQuery("SELECT id FROM questions WHERE quiz_id").
			WithArgs(sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"id"}))

		mock.ExpectExec("INSERT INTO questions").
			WithArgs(1, qz.Questions[0].Text, qz.Questions[0].ImageURL, qz.Questions[0].Position).
			WillReturnResult(sqlmock.NewResult(1, 1))

		mock.ExpectQuery("SELECT id FROM options WHERE question_id").
			WithArgs(sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"id"}))

		mock.ExpectExec("INSERT INTO options").
			WithArgs(1, qz.Questions[0].Options[0].Text, qz.Questions[0].Options[0].Correct).
			WillReturnResult(failLastInsertIDResult{})

		mock.ExpectRollback()

		mock.ExpectClose()

		err = quizStore.CreateQuiz(t.Context(), qz)

		if err == nil {
			t.Fatal("expected an error, but got nil")
		}

		if got, want := err, errFailLastInsertID; !errors.Is(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("get question id scan error", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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

		badQuestionRows := sqlmock.NewRows([]string{"id"}).AddRow("bad id")

		mock.ExpectBegin()

		mock.ExpectExec("INSERT INTO quizzes").
			WithArgs(qz.Title, qz.Slug, qz.Description, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))

		mock.ExpectQuery("SELECT id FROM questions WHERE quiz_id").
			WithArgs(sqlmock.AnyArg()).
			WillReturnRows(badQuestionRows)

		mock.ExpectClose()

		err = quizStore.CreateQuiz(t.Context(), qz)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got, want := err.Error(), "error scanning questionIDs"; !strings.Contains(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("get question id row error", func(t *testing.T) {
		t.Parallel()

		buf := bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(&buf, nil))

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
		rowError := errors.New("row error")
		questionRows := sqlmock.NewRows([]string{"id"}).AddRow(1).RowError(0, rowError)

		mock.ExpectBegin()

		mock.ExpectExec("INSERT INTO quizzes").
			WithArgs(qz.Title, qz.Slug, qz.Description, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))

		mock.ExpectQuery("SELECT id FROM questions WHERE quiz_id").
			WithArgs(sqlmock.AnyArg()).
			WillReturnRows(questionRows)

		mock.ExpectClose()

		err = quizStore.CreateQuiz(t.Context(), qz)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got, want := err, rowError; !errors.Is(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
		if got, want := err.Error(), "error iterating questionIDs"; !strings.Contains(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestSQLiteStore_UpdateQuiz_MockTesting_FailRowsAffected(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer func() {
		if cErr := db.Close(); cErr != nil {
			t.Fatalf("error closing db: %v", cErr)
		}
		if mErr := mock.ExpectationsWereMet(); mErr != nil {
			t.Errorf("there were unfulfilled expectations: %s", mErr)
		}
	}()

	quizStore := quiz.NewSQLiteStore(db, logger)

	testQuiz := &quiz.Quiz{
		ID: 1,
		Questions: []*quiz.Question{
			{
				ID:   1,
				Text: "Test Question",
			},
		},
	}

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE quizzes").
		WithArgs(testQuiz.Title, testQuiz.Slug, testQuiz.Description, testQuiz.ID).
		WillReturnResult(failRowsAffectedResult{})
	mock.ExpectClose()

	err = quizStore.UpdateQuiz(t.Context(), testQuiz)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got, want := err, errFailRowsAffected; !errors.Is(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSQLiteStore_UpdateQuestion_MockTesting_FailRowsAffected(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer func() {
		if cErr := db.Close(); cErr != nil {
			t.Fatalf("error closing db: %v", cErr)
		}
		if mErr := mock.ExpectationsWereMet(); mErr != nil {
			t.Errorf("there were unfulfilled expectations: %s", mErr)
		}
	}()

	quizStore := quiz.NewSQLiteStore(db, logger)

	testQuestion := &quiz.Question{
		ID:       1,
		Text:     "Test Question",
		ImageURL: "http://example.com/image.png",
		Position: 10,
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id FROM questions WHERE quiz_id").
		WithArgs(testQuestion.QuizID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testQuestion.ID))
	mock.ExpectExec("UPDATE questions").
		WithArgs(testQuestion.Text, testQuestion.ImageURL, testQuestion.Position, testQuestion.ID).
		WillReturnResult(failRowsAffectedResult{})
	mock.ExpectClose()

	err = quizStore.UpdateQuestion(t.Context(), testQuestion)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got, want := err, errFailRowsAffected; !errors.Is(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSQLiteStore_updateOptionInTx_MockTesting_FailRowsAffected(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer func() {
		if cErr := db.Close(); cErr != nil {
			t.Fatalf("error closing db: %v", cErr)
		}
		if mErr := mock.ExpectationsWereMet(); mErr != nil {
			t.Errorf("there were unfulfilled expectations: %s", mErr)
		}
	}()

	quizStore := quiz.NewSQLiteStore(db, logger)

	testOption := &quiz.Option{
		ID:         1,
		QuestionID: 1,
		Text:       "Test Option",
		Correct:    true,
	}

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE options").
		WithArgs(testOption.Text, testOption.Correct, testOption.ID).
		WillReturnResult(failRowsAffectedResult{})
	mock.ExpectClose()

	err = quizStore.WithTx(t.Context(), func(tx *sql.Tx) error {
		return quizStore.UpdateOptionInTx(t.Context(), tx, testOption)
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got, want := err, errFailRowsAffected; !errors.Is(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSQLiteStore_withTx_MockTesting_FailRollback(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}

	quizStore := quiz.NewSQLiteStore(db, logger)

	defer func() {
		if err = mock.ExpectationsWereMet(); err != nil {
			t.Errorf("there were unfulfilled expectations: %s", err)
		}
	}()

	queryError := errors.New("query error")
	rollbackError := errors.New("rollback error")

	mock.ExpectBegin()
	mock.ExpectExec("SELECT foo FROM bar").WillReturnError(queryError)
	mock.ExpectRollback().WillReturnError(rollbackError)

	err = quizStore.WithTx(t.Context(), func(tx *sql.Tx) error {
		_, err2 := tx.ExecContext(t.Context(), "SELECT foo FROM bar")

		return err2
	})
	if err == nil {
		t.Fatal("got nil, want error")
	}
	if got, want := err, queryError; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
	if got, want := buf.String(), "error rolling back transaction"; !strings.Contains(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSQliteStore_getOptionIDsInTx_MockTesting(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	t.Run("scan error", func(t *testing.T) {
		t.Parallel()

		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("unexpected error creating sqlmock: %v", err)
		}
		defer func() {
			if cErr := db.Close(); cErr != nil {
				t.Fatalf("error closing db: %v", cErr)
			}
			if mErr := mock.ExpectationsWereMet(); mErr != nil {
				t.Errorf("there were unfulfilled expectations: %s", mErr)
			}
		}()

		quizStore := quiz.NewSQLiteStore(db, logger)

		id := int64(42)
		badQuestionRows := sqlmock.NewRows([]string{"id"}).AddRow("bad id")

		mock.ExpectBegin()

		mock.ExpectQuery("SELECT id FROM options WHERE question_id").
			WithArgs(id).
			WillReturnRows(badQuestionRows)

		mock.ExpectClose()

		err = quizStore.WithTx(t.Context(), func(tx *sql.Tx) error { // use exported test helper
			_, err2 := quizStore.GetOptionIDsInTx(t.Context(), tx, id)

			return err2
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got, want := err.Error(), "error scanning optionIDs"; !strings.Contains(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("row error", func(t *testing.T) {
		t.Parallel()

		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("unexpected error creating sqlmock: %v", err)
		}
		defer func() {
			if cErr := db.Close(); cErr != nil {
				t.Fatalf("error closing db: %v", cErr)
			}
			if mErr := mock.ExpectationsWereMet(); mErr != nil {
				t.Errorf("there were unfulfilled expectations: %s", mErr)
			}
		}()

		quizStore := quiz.NewSQLiteStore(db, logger)

		id := int64(42)
		rowError := errors.New("row error")

		mock.ExpectBegin()

		mock.ExpectQuery("SELECT id FROM options WHERE question_id").
			WithArgs(id).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).RowError(0, rowError))

		mock.ExpectClose()

		err = quizStore.WithTx(t.Context(), func(tx *sql.Tx) error { // use exported test helper
			_, err2 := quizStore.GetOptionIDsInTx(t.Context(), tx, id)

			return err2
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got, want := err, rowError; !errors.Is(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestSQLiteStore_GetQuestionIDsInTx_MockTesting(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(&buf, nil))

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

	quizID := int64(1)

	rowError := errors.New("row error")

	questionRows := sqlmock.NewRows([]string{"id", "quiz_id", "text", "image_url", "position"}).
		AddRow(1, quizID, "Test Question", "http://example.com/image.png", 10).
		AddRow(2, quizID, "Test Question 2", "http://example.com/image2.png", 20).
		RowError(1, rowError)

	mock.ExpectQuery("SELECT id, quiz_id, text, image_url, position FROM questions").
		WithArgs(quizID).WillReturnRows(questionRows)
	mock.ExpectClose()

	quizStore := quiz.NewSQLiteStore(db, logger)

	_, err = quizStore.GetQuestionsByQuizID(t.Context(), quizID)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got, want := err.Error(), "error iterating rows"; !strings.Contains(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSQLiteStore_GetOptionsByQuestionID_MockTesting(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(&buf, nil))

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

	questionID := int64(1)

	rowError := errors.New("row error")

	optionRows := sqlmock.NewRows([]string{"id", "question_id", "text", "is_correct"}).
		AddRow(1, questionID, "Test Option", true).
		AddRow(2, questionID, "Test Option 2", false).
		RowError(1, rowError)

	mock.ExpectQuery("SELECT id, question_id, text, is_correct FROM options").
		WithArgs(questionID).WillReturnRows(optionRows)
	mock.ExpectClose()

	quizStore := quiz.NewSQLiteStore(db, logger)

	_, err = quizStore.GetOptionsByQuestionID(t.Context(), questionID)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got, want := err.Error(), "error iterating rows"; !strings.Contains(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
