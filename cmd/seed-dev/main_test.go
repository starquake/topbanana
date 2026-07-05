package main_test

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	. "github.com/starquake/topbanana/cmd/seed-dev"
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/demo"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/store"
)

func TestQuizFromFixtureFlat(t *testing.T) {
	t.Parallel()

	f := ExportQuizFixture{
		Title:       "Flat Quiz",
		Description: "A single-round quiz.",
		Questions: []ExportQuestionFixture{
			{Text: "Q1", Options: []ExportOptionFixture{{Text: "A", Correct: true}, {Text: "B"}}},
			{Text: "Q2", Options: []ExportOptionFixture{{Text: "C"}, {Text: "D", Correct: true}}},
		},
	}

	qz, err := ExportQuizFromFixture(&f)
	if err != nil {
		t.Fatalf("ExportQuizFromFixture() err = %v, want nil", err)
	}

	if got, want := len(qz.Rounds), 0; got != want {
		t.Errorf("len(Rounds) = %d, want %d", got, want)
	}
	if got, want := len(qz.Questions), 2; got != want {
		t.Fatalf("len(Questions) = %d, want %d", got, want)
	}
	for i, q := range qz.Questions {
		if got, want := q.Position, i+1; got != want {
			t.Errorf("Questions[%d].Position = %d, want %d", i, got, want)
		}
	}
	if got, want := qz.Questions[0].Text, "Q1"; got != want {
		t.Errorf("Questions[0].Text = %q, want %q", got, want)
	}
	if got, want := len(qz.Questions[0].Options), 2; got != want {
		t.Errorf("len(Questions[0].Options) = %d, want %d", got, want)
	}
}

func TestQuizFromFixtureRounds(t *testing.T) {
	t.Parallel()

	f := ExportQuizFixture{
		Title:       "Round Quiz",
		Description: "A multi-round quiz.",
		Rounds: []ExportRoundFixture{
			{
				Title:   "Warm-up",
				Summary: "An easy start.",
				Questions: []ExportQuestionFixture{
					{Text: "R1Q1", Options: []ExportOptionFixture{{Text: "A", Correct: true}, {Text: "B"}}},
					{Text: "R1Q2", Options: []ExportOptionFixture{{Text: "C", Correct: true}, {Text: "D"}}},
				},
			},
			{
				Title:   "Final stretch",
				Summary: "One harder round.",
				Questions: []ExportQuestionFixture{
					{Text: "R2Q1", Options: []ExportOptionFixture{{Text: "E", Correct: true}, {Text: "F"}}},
				},
			},
		},
	}

	qz, err := ExportQuizFromFixture(&f)
	if err != nil {
		t.Fatalf("ExportQuizFromFixture() err = %v, want nil", err)
	}

	if got, want := len(qz.Rounds), 2; got != want {
		t.Fatalf("len(Rounds) = %d, want %d", got, want)
	}

	if got, want := qz.Rounds[0].Title, "Warm-up"; got != want {
		t.Errorf("Rounds[0].Title = %q, want %q", got, want)
	}
	if got, want := qz.Rounds[0].Summary, "An easy start."; got != want {
		t.Errorf("Rounds[0].Summary = %q, want %q", got, want)
	}
	if got, want := qz.Rounds[0].Position, 0; got != want {
		t.Errorf("Rounds[0].Position = %d, want %d", got, want)
	}
	if got, want := qz.Rounds[1].Title, "Final stretch"; got != want {
		t.Errorf("Rounds[1].Title = %q, want %q", got, want)
	}
	if got, want := qz.Rounds[1].Position, 1; got != want {
		t.Errorf("Rounds[1].Position = %d, want %d", got, want)
	}

	if got, want := len(qz.Rounds[0].Questions), 2; got != want {
		t.Errorf("len(Rounds[0].Questions) = %d, want %d", got, want)
	}
	if got, want := len(qz.Rounds[1].Questions), 1; got != want {
		t.Errorf("len(Rounds[1].Questions) = %d, want %d", got, want)
	}

	// finishGame iterates qz.Questions, so the flat mirror must hold every
	// question with quiz-wide positions 1..N in document order across rounds.
	if got, want := len(qz.Questions), 3; got != want {
		t.Fatalf("len(Questions) = %d, want %d", got, want)
	}
	wantText := []string{"R1Q1", "R1Q2", "R2Q1"}
	for i, q := range qz.Questions {
		if got, want := q.Position, i+1; got != want {
			t.Errorf("Questions[%d].Position = %d, want %d", i, got, want)
		}
		if got, want := q.Text, wantText[i]; got != want {
			t.Errorf("Questions[%d].Text = %q, want %q", i, got, want)
		}
	}

	// The flat mirror and the per-round slices share the same question
	// pointers, so a position assigned once is visible from both views.
	if qz.Rounds[0].Questions[0] != qz.Questions[0] {
		t.Error("Rounds[0].Questions[0] is not the same pointer as Questions[0]")
	}
	if qz.Rounds[1].Questions[0] != qz.Questions[2] {
		t.Error("Rounds[1].Questions[0] is not the same pointer as Questions[2]")
	}
}

func TestQuizFromFixtureRoundsAndQuestionsRejected(t *testing.T) {
	t.Parallel()

	f := ExportQuizFixture{
		Title:     "Both",
		Questions: []ExportQuestionFixture{{Text: "Q1"}},
		Rounds:    []ExportRoundFixture{{Title: "R", Questions: []ExportQuestionFixture{{Text: "R1Q1"}}}},
	}

	if _, err := ExportQuizFromFixture(&f); !errors.Is(err, ErrExportFixtureQuestionsOrRounds) {
		t.Errorf("err = %v, want %v", err, ErrExportFixtureQuestionsOrRounds)
	}
}

func TestQuizFromFixtureNeitherRejected(t *testing.T) {
	t.Parallel()

	f := ExportQuizFixture{Title: "Empty"}

	if _, err := ExportQuizFromFixture(&f); !errors.Is(err, ErrExportFixtureQuestionsOrRounds) {
		t.Errorf("err = %v, want %v", err, ErrExportFixtureQuestionsOrRounds)
	}
}

func TestQuizFromFixtureRoundTitleRequired(t *testing.T) {
	t.Parallel()

	f := ExportQuizFixture{
		Title: "No Round Title",
		Rounds: []ExportRoundFixture{
			{Title: "", Questions: []ExportQuestionFixture{{Text: "R1Q1"}}},
		},
	}

	if _, err := ExportQuizFromFixture(&f); !errors.Is(err, ErrExportFixtureRoundTitleRequired) {
		t.Errorf("err = %v, want %v", err, ErrExportFixtureRoundTitleRequired)
	}
}

func TestQuizFromFixtureRoundNoQuestions(t *testing.T) {
	t.Parallel()

	f := ExportQuizFixture{
		Title: "Empty Round",
		Rounds: []ExportRoundFixture{
			{Title: "Lonely", Questions: nil},
		},
	}

	if _, err := ExportQuizFromFixture(&f); !errors.Is(err, ErrExportFixtureRoundNoQuestions) {
		t.Errorf("err = %v, want %v", err, ErrExportFixtureRoundNoQuestions)
	}
}

// audioSeedHarness bundles the real DB-backed stores, the real media service,
// the media root, and a logger the audio-seeding tests share. The service
// writes audio files under mediaDir (a t.TempDir), so a stored clip is a real
// file on disk a test can read back.
type audioSeedHarness struct {
	logger   *slog.Logger
	stores   *store.Stores
	mediaSvc *media.Service
	mediaDir string
}

func newAudioSeedHarness(t *testing.T) audioSeedHarness {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	stores := store.New(dbtest.Open(t), logger)
	mediaDir := t.TempDir()
	mediaSvc := media.NewService(
		stores.Media, mediaDir,
		config.MediaImageMaxBytesDefault, config.MediaAudioMaxBytesDefault, logger,
	)

	return audioSeedHarness{logger: logger, stores: stores, mediaSvc: mediaSvc, mediaDir: mediaDir}
}

// TestSeedQuizzesAudioFlat exercises the audio path end to end against a real
// DB and a real media service: a flat fixture whose first question opts into
// audio must yield a ready audio media row (whose file exists on disk) and the
// question's audio_media_id set, while a no-audio question stays untouched.
func TestSeedQuizzesAudioFlat(t *testing.T) {
	t.Parallel()

	h := newAudioSeedHarness(t)

	fixtures := []ExportQuizFixture{{
		Title:       "Audio Flat",
		Description: "First question has audio.",
		Questions: []ExportQuestionFixture{
			{
				Text:        "With audio (repeat)",
				Audio:       true,
				AudioRepeat: true,
				Options:     []ExportOptionFixture{{Text: "A", Correct: true}, {Text: "B"}},
			},
			{
				Text:    "No audio",
				Options: []ExportOptionFixture{{Text: "C", Correct: true}, {Text: "D"}},
			},
		},
	}}

	created, err := ExportSeedQuizzes(t.Context(), h.logger, h.stores.Quizzes, h.mediaSvc, fixtures)
	if err != nil {
		t.Fatalf("ExportSeedQuizzes() err = %v, want nil", err)
	}
	if got, want := len(created), 1; got != want {
		t.Fatalf("len(created) = %d, want %d", got, want)
	}
	qz := created[0]

	// Exactly one ready audio media row exists for the quiz: ListByQuiz returns
	// only ready rows, so a non-ready (failed two-phase) upload would not appear.
	rows, err := h.mediaSvc.ListByQuiz(t.Context(), qz.ID)
	if err != nil {
		t.Fatalf("ListByQuiz() err = %v, want nil", err)
	}
	if got, want := len(rows), 1; got != want {
		t.Fatalf("len(ready media rows) = %d, want %d", got, want)
	}
	row := rows[0]
	if got, want := row.Type, media.TypeAudio; got != want {
		t.Errorf("media Type = %q, want %q", got, want)
	}
	if got, want := row.MIME, "audio/mpeg"; got != want {
		t.Errorf("media MIME = %q, want %q", got, want)
	}

	// The file the row points at exists under the media dir and holds the bundled
	// clip's bytes.
	onDisk, err := os.ReadFile(filepath.Join(h.mediaDir, row.Path))
	if err != nil {
		t.Fatalf("read stored audio file: %v", err)
	}
	if got, want := onDisk, ExportSampleAudio; !bytes.Equal(got, want) {
		t.Errorf("stored audio bytes len = %d, want %d", len(got), len(want))
	}

	// The first question now references the stored row; the second is untouched.
	q1, err := h.stores.Quizzes.GetQuestion(t.Context(), qz.Questions[0].ID)
	if err != nil {
		t.Fatalf("GetQuestion(q1) err = %v, want nil", err)
	}
	if q1.AudioMediaID == nil {
		t.Fatal("q1.AudioMediaID = nil, want set")
	}
	if got, want := *q1.AudioMediaID, row.ID; got != want {
		t.Errorf("q1.AudioMediaID = %d, want %d", got, want)
	}
	if got, want := q1.AudioRepeat, true; got != want {
		t.Errorf("q1.AudioRepeat = %t, want %t", got, want)
	}

	q2, err := h.stores.Quizzes.GetQuestion(t.Context(), qz.Questions[1].ID)
	if err != nil {
		t.Fatalf("GetQuestion(q2) err = %v, want nil", err)
	}
	if q2.AudioMediaID != nil {
		t.Errorf("q2.AudioMediaID = %d, want nil", *q2.AudioMediaID)
	}
}

// TestSeedQuizzesAudioRounds confirms the audio path lines questions up by
// quiz-wide document order across rounds: a rounds fixture whose audio flag
// sits on the second round's question attaches the clip to that question, not
// the first.
func TestSeedQuizzesAudioRounds(t *testing.T) {
	t.Parallel()

	h := newAudioSeedHarness(t)

	fixtures := []ExportQuizFixture{{
		Title:       "Audio Rounds",
		Description: "Audio on the second round's question.",
		Rounds: []ExportRoundFixture{
			{
				Title: "Round one",
				Questions: []ExportQuestionFixture{
					{Text: "R1Q1 no audio", Options: []ExportOptionFixture{{Text: "A", Correct: true}, {Text: "B"}}},
				},
			},
			{
				Title: "Round two",
				Questions: []ExportQuestionFixture{
					{
						Text:    "R2Q1 audio",
						Audio:   true,
						Options: []ExportOptionFixture{{Text: "C", Correct: true}, {Text: "D"}},
					},
				},
			},
		},
	}}

	created, err := ExportSeedQuizzes(t.Context(), h.logger, h.stores.Quizzes, h.mediaSvc, fixtures)
	if err != nil {
		t.Fatalf("ExportSeedQuizzes() err = %v, want nil", err)
	}
	qz := created[0]

	// qz.Questions is the flat mirror in document order: index 0 is the first
	// round's question (no audio), index 1 the second round's (audio).
	q1, err := h.stores.Quizzes.GetQuestion(t.Context(), qz.Questions[0].ID)
	if err != nil {
		t.Fatalf("GetQuestion(q1) err = %v, want nil", err)
	}
	if q1.AudioMediaID != nil {
		t.Errorf("q1.AudioMediaID = %d, want nil", *q1.AudioMediaID)
	}

	q2, err := h.stores.Quizzes.GetQuestion(t.Context(), qz.Questions[1].ID)
	if err != nil {
		t.Fatalf("GetQuestion(q2) err = %v, want nil", err)
	}
	if q2.AudioMediaID == nil {
		t.Fatal("q2.AudioMediaID = nil, want set")
	}

	rows, err := h.mediaSvc.ListByQuiz(t.Context(), qz.ID)
	if err != nil {
		t.Fatalf("ListByQuiz() err = %v, want nil", err)
	}
	if got, want := len(rows), 1; got != want {
		t.Fatalf("len(ready media rows) = %d, want %d", got, want)
	}
	if got, want := *q2.AudioMediaID, rows[0].ID; got != want {
		t.Errorf("q2.AudioMediaID = %d, want %d", got, want)
	}
}

// demoArchiveDir is the committed directory of demo quiz archives the seeder
// restores, from the seed-dev package directory. demoArchivePath is one archive
// in it. The TestSeedDemoQuiz test doubles as a rot-guard: if the committed
// archive is removed or its shape drifts, this test fails.
const (
	demoArchiveDir  = "../../dev/fixtures/demo"
	demoArchivePath = demoArchiveDir + "/demo-quiz.zip"
)

// TestSeedDemoQuiz restores the real committed demo archive through the seeder's
// HTTP-free import path against a real DB and a real media service writing into a
// temp dir. It asserts the showcase quiz lands with 15 questions across 3 rounds
// and that all 9 media files (4 audio + 5 image) are stored as files on disk,
// then asserts a second restore is the idempotent no-op (nil quiz, nil error).
func TestSeedDemoQuiz(t *testing.T) {
	t.Parallel()

	h := newAudioSeedHarness(t)

	archive, err := ExportOpenDemoArchive(demoArchivePath)
	if err != nil {
		t.Fatalf("ExportOpenDemoArchive() err = %v, want nil", err)
	}

	qz, err := ExportSeedDemoQuiz(t.Context(), h.logger, h.stores, h.mediaSvc, archive)
	if err != nil {
		t.Fatalf("ExportSeedDemoQuiz() err = %v, want nil", err)
	}
	if qz == nil {
		t.Fatal("created quiz = nil, want a quiz")
	}
	if got, want := len(qz.Questions), 15; got != want {
		t.Errorf("len(Questions) = %d, want %d", got, want)
	}

	rounds, err := h.stores.Quizzes.ListRoundsByQuiz(t.Context(), qz.ID)
	if err != nil {
		t.Fatalf("ListRoundsByQuiz() err = %v, want nil", err)
	}
	if got, want := len(rounds), 3; got != want {
		t.Errorf("len(rounds) = %d, want %d", got, want)
	}

	assertDemoMediaOnDisk(t, h, qz.ID)

	// A second restore reuses the same reader, collides on the slug, and is the
	// idempotent no-op.
	again, err := ExportSeedDemoQuiz(t.Context(), h.logger, h.stores, h.mediaSvc, archive)
	if err != nil {
		t.Fatalf("second ExportSeedDemoQuiz() err = %v, want nil", err)
	}
	if again != nil {
		t.Errorf("second ExportSeedDemoQuiz() quiz = %v, want nil (idempotent)", again)
	}
}

// TestSeedDemoArchiveSet restores every committed archive in the demo directory
// through the seeder's HTTP-free import path and asserts all of them land as
// distinct, published quizzes. It pins the demo seed set to more than one quiz
// so the showcase is a set rather than a single quiz (#1136).
func TestSeedDemoArchiveSet(t *testing.T) {
	t.Parallel()

	h := newAudioSeedHarness(t)

	readers, err := ExportOpenDemoArchives(demoArchiveDir)
	if err != nil {
		t.Fatalf("ExportOpenDemoArchives() err = %v, want nil", err)
	}
	if got := len(readers); got < 2 {
		t.Fatalf("demo archive count = %d, want at least 2 (multi-quiz set)", got)
	}

	slugs := make(map[string]struct{}, len(readers))
	for _, zr := range readers {
		qz, seedErr := ExportSeedDemoQuiz(t.Context(), h.logger, h.stores, h.mediaSvc, zr)
		if seedErr != nil {
			t.Fatalf("ExportSeedDemoQuiz() err = %v, want nil", seedErr)
		}
		if qz == nil {
			t.Fatal("created quiz = nil, want a quiz per archive")
		}
		if got, want := qz.Published, true; got != want {
			t.Errorf("demo quiz %q Published = %v, want %v", qz.Title, got, want)
		}
		if _, dup := slugs[qz.Slug]; dup {
			t.Errorf("duplicate slug %q across demo archives", qz.Slug)
		}
		slugs[qz.Slug] = struct{}{}
	}

	quizzes, err := h.stores.Quizzes.ListQuizzes(t.Context())
	if err != nil {
		t.Fatalf("ListQuizzes() err = %v, want nil", err)
	}
	if got, want := len(quizzes), len(readers); got != want {
		t.Errorf("seeded quiz count = %d, want %d (one per archive)", got, want)
	}
}

// TestOpenDemoArchivesEmptyDir pins the fail-fast guard: an archive directory
// with no .zip files errors rather than silently seeding nothing.
func TestOpenDemoArchivesEmptyDir(t *testing.T) {
	t.Parallel()

	_, err := ExportOpenDemoArchives(t.TempDir())
	if got, want := err, demo.ErrNoArchives; !errors.Is(got, want) {
		t.Errorf("ExportOpenDemoArchives() err = %v, want %v", got, want)
	}
}

// assertDemoMediaOnDisk checks the restored demo quiz has the expected 4 audio +
// 5 image media rows and that every one is backed by a real file under the
// harness's temp media dir.
func assertDemoMediaOnDisk(t *testing.T, h audioSeedHarness, quizID int64) {
	t.Helper()

	rows, err := h.mediaSvc.ListByQuiz(t.Context(), quizID)
	if err != nil {
		t.Fatalf("ListByQuiz() err = %v, want nil", err)
	}
	if got, want := len(rows), 9; got != want {
		t.Fatalf("len(media rows) = %d, want %d (4 audio + 5 image)", got, want)
	}

	var audio, image int
	for _, m := range rows {
		if m.Type == media.TypeAudio {
			audio++
		} else {
			image++
		}
		if _, statErr := os.Stat(filepath.Join(h.mediaDir, m.Path)); statErr != nil {
			t.Errorf("stored media file %q missing on disk: %v", m.Path, statErr)
		}
	}
	if got, want := audio, 4; got != want {
		t.Errorf("audio media rows = %d, want %d", got, want)
	}
	if got, want := image, 5; got != want {
		t.Errorf("image media rows = %d, want %d", got, want)
	}
}

// TestSeedPlayerName checks the seeded-player naming: the first pass over the
// pool yields distinct, non-empty names, and the index one past the pool wraps
// back to the first name with a " 2" lap suffix so larger -players values stay
// collision-free within a run.
func TestSeedPlayerName(t *testing.T) {
	t.Parallel()

	poolSize := len(ExportSeedPlayerNames)

	seen := make(map[string]bool, poolSize)
	for i := range poolSize {
		name := ExportSeedPlayerName(i)
		if name == "" {
			t.Errorf("ExportSeedPlayerName(%d) = empty, want non-empty", i)
		}
		if seen[name] {
			t.Errorf("ExportSeedPlayerName(%d) = %q, already seen (want distinct)", i, name)
		}
		seen[name] = true
	}

	if got, want := ExportSeedPlayerName(poolSize), ExportSeedPlayerName(0)+" 2"; got != want {
		t.Errorf("ExportSeedPlayerName(%d) = %q, want %q", poolSize, got, want)
	}
}

// TestSeedPlaysToleratesDuplicateName confirms seedPlays skips a player whose
// name a prior run already claimed (display_name is UNIQUE) rather than failing
// the whole seed: the non-colliding player still gets a finished game.
func TestSeedPlaysToleratesDuplicateName(t *testing.T) {
	t.Parallel()

	h := newAudioSeedHarness(t)

	// Pre-claim the first seeded name so seedPlays collides on player 0.
	if _, err := h.stores.Players.CreateAnonymousPlayer(t.Context(), ExportSeedPlayerName(0)); err != nil {
		t.Fatalf("pre-create player err = %v, want nil", err)
	}

	fixtures := []ExportQuizFixture{{
		Title:       "Solo Quiz",
		Description: "A one-question quiz.",
		Questions: []ExportQuestionFixture{
			{Text: "Q1", Options: []ExportOptionFixture{{Text: "A", Correct: true}, {Text: "B"}}},
		},
	}}
	created, err := ExportSeedQuizzes(t.Context(), h.logger, h.stores.Quizzes, h.mediaSvc, fixtures)
	if err != nil {
		t.Fatalf("ExportSeedQuizzes() err = %v, want nil", err)
	}

	plays, err := ExportSeedPlays(t.Context(), h.logger, h.stores, created, 2, 1)
	if err != nil {
		t.Fatalf("ExportSeedPlays() err = %v, want nil", err)
	}
	if plays <= 0 {
		t.Errorf("plays = %d, want > 0 (non-colliding player still finishes a game)", plays)
	}
}
