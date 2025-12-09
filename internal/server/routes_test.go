package server_test

import (
	"bytes"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/starquake/topbanana/internal/logging"
	"github.com/starquake/topbanana/internal/migrations"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/server"
	"github.com/starquake/topbanana/internal/store"
	_ "modernc.org/sqlite"
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
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	return db
}

func TestAddRoutes(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := logging.NewLogger(&buf)

	db := setupTestDBWithMigrations(t)

	quizStore := quiz.NewSQLiteStore(db, logger)

	err := quizStore.CreateQuiz(t.Context(), &quiz.Quiz{
		Title:       "Quiz 1",
		Slug:        "quiz-1",
		Description: "Quiz 1 Description",
		Questions: []*quiz.Question{
			{
				Text:     "Question 1",
				Position: 10,
				Options: []*quiz.Option{
					{Text: "Option 1"},
					{Text: "Option 2"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("error creating quiz: %v", err)
	}

	stores := &store.Stores{
		Quizzes: quizStore,
	}
	mux := http.NewServeMux()
	server.AddRoutes(mux, logger, stores)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{
			name:       "Admin Index",
			method:     http.MethodGet,
			path:       "/admin",
			wantStatus: http.StatusOK,
		},
		{
			name:       "Admin Quiz List",
			method:     http.MethodGet,
			path:       "/admin/quizzes",
			wantStatus: http.StatusOK,
		},
		{
			name:       "Admin Quiz View",
			method:     http.MethodGet,
			path:       "/admin/quizzes/1",
			wantStatus: http.StatusOK,
		},
		{
			name:       "Admin Quiz Create",
			method:     http.MethodGet,
			path:       "/admin/quizzes/new",
			wantStatus: http.StatusOK,
		},
		{
			name:       "Admin Quiz Save",
			method:     http.MethodPost,
			path:       "/admin/quizzes/save",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Admin Quiz Edit",
			method:     http.MethodGet,
			path:       "/admin/quizzes/1/edit",
			wantStatus: http.StatusOK,
		},
		{
			name:       "Question Create",
			method:     http.MethodGet,
			path:       "/admin/quizzes/1/questions/new",
			wantStatus: http.StatusOK,
		},
		{
			name:       "Question Save",
			method:     http.MethodPost,
			path:       "/admin/quizzes/1/questions/save",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Question Edit",
			method:     http.MethodGet,
			path:       "/admin/quizzes/1/questions/1/edit",
			wantStatus: http.StatusOK,
		},
		{
			name:       "Question Save With Question ID",
			method:     http.MethodPost,
			path:       "/admin/quizzes/1/questions/1/save",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Not Found Route",
			method:     http.MethodGet,
			path:       "/unknown/path",
			wantStatus: http.StatusNotFound,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("unexpected status code: got %v, want %v", rec.Code, tc.wantStatus)
				t.Logf("logstr: %s", buf.String())
			}
		})
	}
}
