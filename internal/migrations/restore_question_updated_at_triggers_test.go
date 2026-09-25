package migrations_test

import (
	"maps"
	"testing"

	"github.com/starquake/topbanana/internal/dbtest"
)

// TestRestoreQuestionUpdatedAtTriggers_AllSixExist pins that every trigger
// bumping quizzes.updated_at survives the migrations, so a later table rebuild
// cannot drop one silently (#1346).
func TestRestoreQuestionUpdatedAtTriggers_AllSixExist(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	rows, err := db.QueryContext(t.Context(), `
		SELECT name, tbl_name
		FROM sqlite_schema
		WHERE type = 'trigger' AND name LIKE 'quizzes\_updated\_at\_on\_%' ESCAPE '\'`)
	if err != nil {
		t.Fatalf("query triggers err = %v", err)
	}
	t.Cleanup(func() {
		if cerr := rows.Close(); cerr != nil {
			t.Errorf("rows.Close err = %v", cerr)
		}
	})

	got := map[string]string{}
	for rows.Next() {
		var name, table string
		if err = rows.Scan(&name, &table); err != nil {
			t.Fatalf("scan err = %v", err)
		}
		got[name] = table
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("rows err = %v", err)
	}

	want := map[string]string{
		"quizzes_updated_at_on_question_insert": "questions",
		"quizzes_updated_at_on_question_update": "questions",
		"quizzes_updated_at_on_question_delete": "questions",
		"quizzes_updated_at_on_option_insert":   "options",
		"quizzes_updated_at_on_option_update":   "options",
		"quizzes_updated_at_on_option_delete":   "options",
	}
	if !maps.Equal(got, want) {
		t.Errorf("quizzes_updated_at_on_* triggers (name -> table) = %v, want %v", got, want)
	}
}
