package migrations_test

import (
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/starquake/topbanana/internal/dbtest"
)

// mediaDescriptionVersion is the #1072 migration adding media.description.
const mediaDescriptionVersion = 20260620120000

// TestMediaDescriptionMigration_Column pins the #1072 schema addition: media
// gains a description column.
func TestMediaDescriptionMigration_Column(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	if !tableColumns(t, db, "media")["description"] {
		t.Error("media is missing the description column")
	}
}

// TestMediaDescriptionMigration_DefaultAndRoundTrip pins that an existing row
// reads back the empty-string default (no backfill needed) and that an explicit
// description stores and reads back.
func TestMediaDescriptionMigration_DefaultAndRoundTrip(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Media description", "media-description-roundtrip")

	defaulted := seedMedia(t, db, quizID)
	var gotDefault string
	if err := db.QueryRowContext(
		t.Context(), "SELECT description FROM media WHERE id = ?", defaulted,
	).Scan(&gotDefault); err != nil {
		t.Fatalf("read default description err = %v, want nil", err)
	}
	if gotDefault != "" {
		t.Errorf("description = %q, want empty default", gotDefault)
	}

	if _, err := db.ExecContext(
		t.Context(), "UPDATE media SET description = ? WHERE id = ?", "Intro theme", defaulted,
	); err != nil {
		t.Fatalf("set description err = %v, want nil", err)
	}
	var got string
	if err := db.QueryRowContext(
		t.Context(), "SELECT description FROM media WHERE id = ?", defaulted,
	).Scan(&got); err != nil {
		t.Fatalf("read description err = %v, want nil", err)
	}
	if want := "Intro theme"; got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

// TestMediaDescriptionMigration_DownUpRoundTrips pins that the #1072 migration
// runs both directions against a populated DB: Down drops the column and Up
// re-adds it with the empty-string default.
func TestMediaDescriptionMigration_DownUpRoundTrips(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Media description down-up", "media-description-down-up")
	seedMedia(t, db, quizID)

	if err := goose.DownTo(db, ".", mediaDescriptionVersion-1); err != nil {
		t.Fatalf("goose.DownTo err = %v, want nil", err)
	}
	if tableColumns(t, db, "media")["description"] {
		t.Error("media still has description after Down, want it dropped")
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("goose.Up err = %v, want nil", err)
	}
	if !tableColumns(t, db, "media")["description"] {
		t.Error("media is missing description after re-Up")
	}

	var n int
	if err := db.QueryRowContext(
		t.Context(), "SELECT count(*) FROM media WHERE quiz_id = ? AND description = ''", quizID,
	).Scan(&n); err != nil {
		t.Fatalf("count default descriptions err = %v, want nil", err)
	}
	if n == 0 {
		t.Error("no rows read back the empty description default after re-Up")
	}
}
