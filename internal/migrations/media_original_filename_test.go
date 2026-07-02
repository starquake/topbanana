package migrations_test

import (
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/starquake/topbanana/internal/dbtest"
)

// mediaOriginalFilenameVersion is the #1137 migration adding
// media.original_filename.
const mediaOriginalFilenameVersion = 20260702120000

// TestMediaOriginalFilenameMigration_Column pins the #1137 schema addition:
// media gains an original_filename column.
func TestMediaOriginalFilenameMigration_Column(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	if !tableColumns(t, db, "media")["original_filename"] {
		t.Error("media is missing the original_filename column")
	}
}

// TestMediaOriginalFilenameMigration_DefaultAndRoundTrip pins that an existing
// row reads back the empty-string default (no backfill needed) and that an
// explicit filename stores and reads back.
func TestMediaOriginalFilenameMigration_DefaultAndRoundTrip(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Media original filename", "media-original-filename-roundtrip")

	defaulted := seedMedia(t, db, quizID)
	var gotDefault string
	if err := db.QueryRowContext(
		t.Context(), "SELECT original_filename FROM media WHERE id = ?", defaulted,
	).Scan(&gotDefault); err != nil {
		t.Fatalf("read default original_filename err = %v, want nil", err)
	}
	if gotDefault != "" {
		t.Errorf("original_filename = %q, want empty default", gotDefault)
	}

	if _, err := db.ExecContext(
		t.Context(), "UPDATE media SET original_filename = ? WHERE id = ?", "sunset.png", defaulted,
	); err != nil {
		t.Fatalf("set original_filename err = %v, want nil", err)
	}
	var got string
	if err := db.QueryRowContext(
		t.Context(), "SELECT original_filename FROM media WHERE id = ?", defaulted,
	).Scan(&got); err != nil {
		t.Fatalf("read original_filename err = %v, want nil", err)
	}
	if want := "sunset.png"; got != want {
		t.Errorf("original_filename = %q, want %q", got, want)
	}
}

// TestMediaOriginalFilenameMigration_DownUpRoundTrips pins that the #1137
// migration runs both directions against a populated DB: Down drops the column
// and Up re-adds it with the empty-string default.
func TestMediaOriginalFilenameMigration_DownUpRoundTrips(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Media original filename down-up", "media-original-filename-down-up")
	seedMedia(t, db, quizID)

	if err := goose.DownTo(db, ".", mediaOriginalFilenameVersion-1); err != nil {
		t.Fatalf("goose.DownTo err = %v, want nil", err)
	}
	if tableColumns(t, db, "media")["original_filename"] {
		t.Error("media still has original_filename after Down, want it dropped")
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("goose.Up err = %v, want nil", err)
	}
	if !tableColumns(t, db, "media")["original_filename"] {
		t.Error("media is missing original_filename after re-Up")
	}

	var n int
	if err := db.QueryRowContext(
		t.Context(), "SELECT count(*) FROM media WHERE quiz_id = ? AND original_filename = ''", quizID,
	).Scan(&n); err != nil {
		t.Fatalf("count default original filenames err = %v, want nil", err)
	}
	if n == 0 {
		t.Error("no rows read back the empty original_filename default after re-Up")
	}
}
