package migrations_test

import (
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/starquake/topbanana/internal/dbtest"
)

// questionAudioRepeatVersion is the #1073 migration adding
// questions.audio_repeat.
const questionAudioRepeatVersion = 20260620130000

// TestQuestionAudioRepeatMigration_Column pins the #1073 schema addition:
// questions gains an audio_repeat column.
func TestQuestionAudioRepeatMigration_Column(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	if !tableColumns(t, db, "questions")["audio_repeat"] {
		t.Error("questions is missing the audio_repeat column")
	}
}

// TestQuestionAudioRepeatMigration_DownUpRoundTrips pins that the #1073 migration
// runs both directions cleanly against a populated DB: Down drops audio_repeat,
// Up re-adds it.
func TestQuestionAudioRepeatMigration_DownUpRoundTrips(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	// Seed a quiz + question so Down/Up runs against populated tables, not an
	// empty schema.
	quizID := seedQuiz(t, db, "Audio repeat roundtrip", "audio-repeat-down-up")
	roundID := seedRound(t, db, quizID)
	seedQuestion(t, db, quizID, roundID, 1)

	if err := goose.DownTo(db, ".", questionAudioRepeatVersion-1); err != nil {
		t.Fatalf("goose.DownTo err = %v, want nil", err)
	}
	if tableColumns(t, db, "questions")["audio_repeat"] {
		t.Error("questions still has audio_repeat after Down, want it dropped")
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("goose.Up err = %v, want nil", err)
	}
	if !tableColumns(t, db, "questions")["audio_repeat"] {
		t.Error("questions is missing audio_repeat after re-Up")
	}
}

// TestQuestionAudioRepeatMigration_ValueRoundTrips pins that audio_repeat
// defaults to 0 and stores and reads back an explicit value.
func TestQuestionAudioRepeatMigration_ValueRoundTrips(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Audio repeat value", "audio-repeat-value")
	roundID := seedRound(t, db, quizID)
	questionID := seedQuestion(t, db, quizID, roundID, 1)

	var got int64
	if err := db.QueryRowContext(
		t.Context(), "SELECT audio_repeat FROM questions WHERE id = ?", questionID,
	).Scan(&got); err != nil {
		t.Fatalf("read default audio_repeat err = %v, want nil", err)
	}
	if want := int64(0); got != want {
		t.Errorf("default audio_repeat = %d, want %d", got, want)
	}

	if _, err := db.ExecContext(
		t.Context(), "UPDATE questions SET audio_repeat = 1 WHERE id = ?", questionID,
	); err != nil {
		t.Fatalf("set audio_repeat err = %v, want nil", err)
	}
	if err := db.QueryRowContext(
		t.Context(), "SELECT audio_repeat FROM questions WHERE id = ?", questionID,
	).Scan(&got); err != nil {
		t.Fatalf("read audio_repeat err = %v, want nil", err)
	}
	if want := int64(1); got != want {
		t.Errorf("audio_repeat = %d, want %d", got, want)
	}
}
