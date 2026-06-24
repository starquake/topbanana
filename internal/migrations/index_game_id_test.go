package migrations_test

import (
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/starquake/topbanana/internal/dbtest"
)

// indexGameIDVersion is the migration that adds indexes on game_id for
// game_questions and game_participants.
const indexGameIDVersion = 20260624130000

// TestIndexGameID_UpCreatesIndexes pins that the Up migration creates both
// indexes. Each is verified by querying sqlite_master for the index name.
func TestIndexGameID_UpCreatesIndexes(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v, want nil", cerr)
		}
	})

	for _, idx := range []string{
		"game_questions_game_id_idx",
		"game_participants_game_id_idx",
	} {
		if !indexExists(t, db, idx) {
			t.Errorf("index %q does not exist after migration, want it to", idx)
		}
	}
}

// TestIndexGameID_DownDropsIndexes pins that the Down migration removes both
// indexes so the schema returns to its pre-migration shape.
func TestIndexGameID_DownDropsIndexes(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v, want nil", cerr)
		}
	})

	if err := goose.DownTo(db, ".", indexGameIDVersion-1); err != nil {
		t.Fatalf("goose.DownTo err = %v, want nil", err)
	}

	for _, idx := range []string{
		"game_questions_game_id_idx",
		"game_participants_game_id_idx",
	} {
		if indexExists(t, db, idx) {
			t.Errorf("index %q still exists after Down, want it dropped", idx)
		}
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("goose.Up err = %v, want nil", err)
	}
}
