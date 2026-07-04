package migrations_test

import (
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/starquake/topbanana/internal/dbtest"
)

// quizPublishedVersion is the #1192 ADD COLUMN + backfill migration adding quizzes.published.
const quizPublishedVersion = 20260704120000

// TestQuizPublishedMigration_Column pins the #1192 schema addition: quizzes gains a published column.
func TestQuizPublishedMigration_Column(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	if !tableColumns(t, db, "quizzes")["published"] {
		t.Error("quizzes is missing the published column")
	}
}

// TestQuizPublishedMigration_DefaultAndRoundTrip pins the draft default (0) and a round-trip of published=1.
func TestQuizPublishedMigration_DefaultAndRoundTrip(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Publish default", "publish-default-roundtrip")

	var gotDefault int64
	if err := db.QueryRowContext(
		t.Context(), "SELECT published FROM quizzes WHERE id = ?", quizID,
	).Scan(&gotDefault); err != nil {
		t.Fatalf("read default published err = %v, want nil", err)
	}
	if want := int64(0); gotDefault != want {
		t.Errorf("published = %d, want %d (draft default)", gotDefault, want)
	}

	if _, err := db.ExecContext(
		t.Context(), "UPDATE quizzes SET published = 1 WHERE id = ?", quizID,
	); err != nil {
		t.Fatalf("publish quiz err = %v, want nil", err)
	}
	var got int64
	if err := db.QueryRowContext(
		t.Context(), "SELECT published FROM quizzes WHERE id = ?", quizID,
	).Scan(&got); err != nil {
		t.Fatalf("read published err = %v, want nil", err)
	}
	if want := int64(1); got != want {
		t.Errorf("published = %d, want %d", got, want)
	}
}

// TestQuizPublishedMigration_BackfillsExistingQuizzes pins that a pre-existing quiz lands at published=1 (#1192); it seeds below the version via DownTo then re-Ups to run the backfill against populated data.
func TestQuizPublishedMigration_BackfillsExistingQuizzes(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	if err := goose.DownTo(db, ".", quizPublishedVersion-1); err != nil {
		t.Fatalf("goose.DownTo err = %v, want nil", err)
	}

	existingID := seedQuiz(t, db, "Pre-existing", "pre-existing-publish-backfill")

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("goose.Up err = %v, want nil", err)
	}

	var got int64
	if err := db.QueryRowContext(
		t.Context(), "SELECT published FROM quizzes WHERE id = ?", existingID,
	).Scan(&got); err != nil {
		t.Fatalf("read published err = %v, want nil", err)
	}
	if want := int64(1); got != want {
		t.Errorf("pre-existing quiz published = %d, want %d (backfilled)", got, want)
	}
}

// TestQuizPublishedMigration_DownUpRoundTrips pins Down/Up against a populated DB: Down drops the column, Up re-adds and backfills it to published.
func TestQuizPublishedMigration_DownUpRoundTrips(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Publish down-up", "publish-down-up")

	if err := goose.DownTo(db, ".", quizPublishedVersion-1); err != nil {
		t.Fatalf("goose.DownTo err = %v, want nil", err)
	}
	if tableColumns(t, db, "quizzes")["published"] {
		t.Error("quizzes still has published after Down, want it dropped")
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("goose.Up err = %v, want nil", err)
	}
	if !tableColumns(t, db, "quizzes")["published"] {
		t.Error("quizzes is missing published after re-Up")
	}

	var got int64
	if err := db.QueryRowContext(
		t.Context(), "SELECT published FROM quizzes WHERE id = ?", quizID,
	).Scan(&got); err != nil {
		t.Fatalf("read published err = %v, want nil", err)
	}
	if want := int64(1); got != want {
		t.Errorf("published = %d after re-Up, want %d (backfilled)", got, want)
	}
}

// TestQuizPublishedMigration_CheckConstraintRejectsInvalid pins the CHECK on published: it refuses a value outside {0, 1}.
func TestQuizPublishedMigration_CheckConstraintRejectsInvalid(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Publish check", "publish-check")

	if _, err := db.ExecContext(
		t.Context(), "UPDATE quizzes SET published = 2 WHERE id = ?", quizID,
	); err == nil {
		t.Error("UPDATE to 2 err = nil, want a CHECK violation")
	}
}
