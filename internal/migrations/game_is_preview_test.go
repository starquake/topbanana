package migrations_test

import (
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/starquake/topbanana/internal/dbtest"
)

// gameIsPreviewVersion is the #1192 ADD COLUMN migration adding games.is_preview.
const gameIsPreviewVersion = 20260704130000

// TestGameIsPreviewMigration_Column pins the #1192 schema addition: games gains
// an is_preview column.
func TestGameIsPreviewMigration_Column(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	if !tableColumns(t, db, "games")["is_preview"] {
		t.Error("games is missing the is_preview column")
	}
}

// TestGameIsPreviewMigration_DefaultAndRoundTrip pins that a freshly inserted
// game reads back the non-preview default (0) and that flagging a game as a
// preview stores and reads back as 1.
func TestGameIsPreviewMigration_DefaultAndRoundTrip(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Preview default", "preview-default-roundtrip")
	if _, err := db.ExecContext(
		t.Context(), "INSERT INTO games (id, quiz_id) VALUES ('g-preview-default', ?)", quizID,
	); err != nil {
		t.Fatalf("seed game err = %v, want nil", err)
	}

	var gotDefault int64
	if err := db.QueryRowContext(
		t.Context(), "SELECT is_preview FROM games WHERE id = 'g-preview-default'",
	).Scan(&gotDefault); err != nil {
		t.Fatalf("read default is_preview err = %v, want nil", err)
	}
	if want := int64(0); gotDefault != want {
		t.Errorf("is_preview = %d, want %d (non-preview default)", gotDefault, want)
	}

	if _, err := db.ExecContext(
		t.Context(), "UPDATE games SET is_preview = 1 WHERE id = 'g-preview-default'",
	); err != nil {
		t.Fatalf("flag preview err = %v, want nil", err)
	}
	var got int64
	if err := db.QueryRowContext(
		t.Context(), "SELECT is_preview FROM games WHERE id = 'g-preview-default'",
	).Scan(&got); err != nil {
		t.Fatalf("read is_preview err = %v, want nil", err)
	}
	if want := int64(1); got != want {
		t.Errorf("is_preview = %d, want %d", got, want)
	}
}

// TestGameIsPreviewMigration_DownUpRoundTrips pins that the #1192 migration runs
// both directions against a populated DB: Down drops the column and Up re-adds
// it with the non-preview default.
func TestGameIsPreviewMigration_DownUpRoundTrips(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Preview down-up", "preview-down-up")
	if _, err := db.ExecContext(
		t.Context(), "INSERT INTO games (id, quiz_id) VALUES ('g-preview-down-up', ?)", quizID,
	); err != nil {
		t.Fatalf("seed game err = %v, want nil", err)
	}

	if err := goose.DownTo(db, ".", gameIsPreviewVersion-1); err != nil {
		t.Fatalf("goose.DownTo err = %v, want nil", err)
	}
	if tableColumns(t, db, "games")["is_preview"] {
		t.Error("games still has is_preview after Down, want it dropped")
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("goose.Up err = %v, want nil", err)
	}
	if !tableColumns(t, db, "games")["is_preview"] {
		t.Error("games is missing is_preview after re-Up")
	}

	var got int64
	if err := db.QueryRowContext(
		t.Context(), "SELECT is_preview FROM games WHERE id = 'g-preview-down-up'",
	).Scan(&got); err != nil {
		t.Fatalf("read is_preview err = %v, want nil", err)
	}
	if want := int64(0); got != want {
		t.Errorf("is_preview = %d after re-Up, want %d (default)", got, want)
	}
}

// TestGameIsPreviewMigration_CheckConstraintRejectsInvalid pins the CHECK on
// is_preview: the column refuses a value outside {0, 1}.
func TestGameIsPreviewMigration_CheckConstraintRejectsInvalid(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Preview check", "preview-check")
	if _, err := db.ExecContext(
		t.Context(), "INSERT INTO games (id, quiz_id) VALUES ('g-preview-check', ?)", quizID,
	); err != nil {
		t.Fatalf("seed game err = %v, want nil", err)
	}

	if _, err := db.ExecContext(
		t.Context(), "UPDATE games SET is_preview = 2 WHERE id = 'g-preview-check'",
	); err == nil {
		t.Error("UPDATE to 2 err = nil, want a CHECK violation")
	}
}
