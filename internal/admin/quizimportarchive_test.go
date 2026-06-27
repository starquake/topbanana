package admin_test

import (
	"archive/zip"
	"bytes"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/mediahttp"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// importTestImageMax, importTestAudioMax, and importTestTotalMax are generous
// per-entry / total caps for the importer under test: large enough that the tiny
// test media never trips a size guard, so a size-rejection test can drop them low
// deliberately.
const (
	importTestImageMax int64 = 10 << 20
	importTestAudioMax int64 = 20 << 20
	importTestTotalMax int64 = 64 << 20
)

// newImportHandler builds the archive-import handler over the env's real quiz
// store and a media service writing under a fresh temp dir, with a disabled
// budget (budget 0 admits every charge) and the generous test caps. A caller can
// pass overriding limits for a size-rejection test.
func newImportHandler(t *testing.T, e *adminEnv, mediaSvc *media.Service, limits ArchiveImportLimits) http.Handler {
	t.Helper()

	budget := mediahttp.NewUploadBudgetLimiter(0, 0)

	return HandleQuizImportArchive(e.logger, nil, e.quizzes, mediaSvc, budget, limits)
}

// importRequest builds a multipart POST to the archive-import route carrying the
// zip bytes under the "archive" field plus the given visibility / mode form
// overrides (empty means "use the archive's value"). The multipart form is
// pre-parsed (as the route's middleware would) and the seeded admin is attached
// to the context (as requireGameHost would).
func importRequest(
	t *testing.T, archiveBytes []byte, visibility, mode string, player *auth.Player,
) *http.Request {
	t.Helper()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("archive", "quiz.zip")
	if err != nil {
		t.Fatalf("CreateFormFile err = %v, want nil", err)
	}
	if _, err = part.Write(archiveBytes); err != nil {
		t.Fatalf("writing archive part err = %v, want nil", err)
	}
	if err = mw.WriteField("visibility", visibility); err != nil {
		t.Fatalf("WriteField visibility err = %v, want nil", err)
	}
	if err = mw.WriteField("mode", mode); err != nil {
		t.Fatalf("WriteField mode err = %v, want nil", err)
	}
	if err = mw.Close(); err != nil {
		t.Fatalf("multipart Close err = %v, want nil", err)
	}

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/admin/quizzes/import/archive", &body,
	)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if err = req.ParseMultipartForm(1 << 20); err != nil {
		t.Fatalf("ParseMultipartForm err = %v, want nil", err)
	}

	return req.WithContext(auth.WithPlayer(req.Context(), player))
}

// exportArchiveBytes seeds a quiz on a throwaway export env, attaches the given
// image to two questions (dedupe) and the audio to one (repeat on), and returns
// the exported .zip bytes plus the source quiz. The export env's DB is separate
// from the import env's, modelling an export from another instance.
func exportArchiveBytes(t *testing.T) []byte {
	t.Helper()

	env := newAdminEnv(t)
	mediaSvc := newMediaServiceOverTemp(t, env)
	qz := env.seedQuiz(t, roundedQuiz())

	img, err := mediaSvc.StoreImage(t.Context(), qz.ID, testExportPlayerID, bytes.NewReader(tinyPNG(t)))
	if err != nil {
		t.Fatalf("StoreImage err = %v, want nil", err)
	}
	aud, err := mediaSvc.StoreAudio(
		t.Context(), qz.ID, testExportPlayerID, 1234, "Theme", "theme.mp3", bytes.NewReader(tinyMP3()),
	)
	if err != nil {
		t.Fatalf("StoreAudio err = %v, want nil", err)
	}

	r0 := qz.Rounds[0]
	attachMedia(t, env, r0.Questions[0], img.ID, aud.ID, true)
	attachMedia(t, env, r0.Questions[1], img.ID, 0, false)

	var buf bytes.Buffer
	if err = WriteQuizArchive(t.Context(), &buf, env.quizzes, mediaSvc, qz.ID); err != nil {
		t.Fatalf("WriteQuizArchive err = %v, want nil", err)
	}

	return buf.Bytes()
}

func importAdmin() *auth.Player {
	return &auth.Player{ID: testAdminID, Role: auth.RoleAdmin}
}

func defaultImportLimits() ArchiveImportLimits {
	return NewArchiveImportLimits(importTestImageMax, importTestAudioMax, importTestTotalMax)
}

// TestHandleQuizImportArchive_RoundTrip exports a media-bearing quiz, imports it
// on a clean DB, and asserts the imported quiz equals the original (title,
// description, visibility, mode, rounds/questions/options) and that the restored
// image/audio are real media rows referenced by the right questions with the
// expected metadata - and that a reused image yields exactly one media row.
func TestHandleQuizImportArchive_RoundTrip(t *testing.T) {
	t.Parallel()

	archiveBytes := exportArchiveBytes(t)

	env := newAdminEnv(t)
	mediaSvc := newMediaServiceOverTemp(t, env)
	handler := newImportHandler(t, env, mediaSvc, defaultImportLimits())

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, importRequest(t, archiveBytes, "", "", importAdmin()))

	if got, want := rr.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d (body: %s)", got, want, rr.Body.String())
	}

	imported := onlyQuiz(t, env)
	assertImportedQuizMeta(t, imported)
	assertImportedRounds(t, env, imported)
	assertImportedMedia(t, env, imported)
}

// onlyQuiz returns the single quiz the import created, failing if there is not
// exactly one.
func onlyQuiz(t *testing.T, env *adminEnv) *quiz.Quiz {
	t.Helper()

	quizzes, err := env.quizzes.ListQuizzes(t.Context())
	if err != nil {
		t.Fatalf("ListQuizzes err = %v, want nil", err)
	}
	if got, want := len(quizzes), 1; got != want {
		t.Fatalf("imported quiz count = %d, want %d", got, want)
	}

	full, err := env.quizzes.GetQuiz(t.Context(), quizzes[0].ID)
	if err != nil {
		t.Fatalf("GetQuiz err = %v, want nil", err)
	}

	return full
}

func assertImportedQuizMeta(t *testing.T, imported *quiz.Quiz) {
	t.Helper()

	if got, want := imported.Title, "Capitals"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := imported.Slug, "capitals"; got != want {
		t.Errorf("Slug = %q, want %q", got, want)
	}
	if got, want := imported.Description, "A tour of capitals."; got != want {
		t.Errorf("Description = %q, want %q", got, want)
	}
	if got, want := imported.TimeLimitSeconds, 12; got != want {
		t.Errorf("TimeLimitSeconds = %d, want %d", got, want)
	}
	// No form override, so the manifest's values (live, public) apply.
	if got, want := imported.Mode, quiz.ModeLive; got != want {
		t.Errorf("Mode = %q, want %q", got, want)
	}
	if got, want := imported.Visibility, quiz.VisibilityPublic; got != want {
		t.Errorf("Visibility = %q, want %q", got, want)
	}
	if got, want := imported.CreatedByPlayerID, testAdminID; got != want {
		t.Errorf("CreatedByPlayerID = %d, want %d", got, want)
	}
}

// assertImportedRounds pins that the two authored rounds round-tripped with
// their titles, the first round's summary, and the second round's boundary
// override, and that the quiz carries all three questions with their options.
func assertImportedRounds(t *testing.T, env *adminEnv, imported *quiz.Quiz) {
	t.Helper()

	if got, want := len(imported.Questions), 3; got != want {
		t.Fatalf("imported question count = %d, want %d", got, want)
	}

	rounds, err := env.quizzes.ListRoundsByQuiz(t.Context(), imported.ID)
	if err != nil {
		t.Fatalf("ListRoundsByQuiz err = %v, want nil", err)
	}
	if got, want := len(rounds), 2; got != want {
		t.Fatalf("imported round count = %d, want %d", got, want)
	}
	if got, want := rounds[0].Title, "Warm-up"; got != want {
		t.Errorf("rounds[0].Title = %q, want %q", got, want)
	}
	if got, want := rounds[0].Summary, "An easy start."; got != want {
		t.Errorf("rounds[0].Summary = %q, want %q", got, want)
	}
	if got, want := rounds[1].Title, "Finish"; got != want {
		t.Errorf("rounds[1].Title = %q, want %q", got, want)
	}
	if rounds[1].BoundaryDurationSeconds == nil {
		t.Fatal("rounds[1].BoundaryDurationSeconds = nil, want 15")
	}
	if got, want := *rounds[1].BoundaryDurationSeconds, 15; got != want {
		t.Errorf("rounds[1].BoundaryDurationSeconds = %d, want %d", got, want)
	}

	assertImportedOptions(t, imported)
}

// assertImportedOptions pins that the first question's options round-tripped
// with their text and correct flags.
func assertImportedOptions(t *testing.T, imported *quiz.Quiz) {
	t.Helper()

	var first *quiz.Question
	for _, q := range imported.Questions {
		if q.Position == 1 {
			first = q

			break
		}
	}
	if first == nil {
		t.Fatal("no question at position 1")
	}
	if got, want := first.Text, "Capital of France?"; got != want {
		t.Errorf("question[0].Text = %q, want %q", got, want)
	}
	if got, want := len(first.Options), 2; got != want {
		t.Fatalf("question[0] options = %d, want %d", got, want)
	}

	var paris *quiz.Option
	for _, o := range first.Options {
		if o.Text == "Paris" {
			paris = o

			break
		}
	}
	if paris == nil {
		t.Fatal("question[0] has no Paris option")
	}
	if !paris.Correct {
		t.Error("Paris option Correct = false, want true")
	}
}

// assertImportedMedia pins the restored media: one image row reused by two
// questions and one audio row on one question, with the audio's description,
// duration, and repeat flag round-tripped. The image and audio rows are real,
// ready, quiz-scoped rows the questions reference by id.
func assertImportedMedia(t *testing.T, env *adminEnv, imported *quiz.Quiz) {
	t.Helper()

	rows, err := env.media.ListMediaByQuiz(t.Context(), imported.ID)
	if err != nil {
		t.Fatalf("ListMediaByQuiz err = %v, want nil", err)
	}

	images, audios := splitByType(rows)
	if got, want := len(images), 1; got != want {
		t.Fatalf("imported image rows = %d, want %d (reused image deduped to one)", got, want)
	}
	if got, want := len(audios), 1; got != want {
		t.Fatalf("imported audio rows = %d, want %d", got, want)
	}

	imageID := images[0].ID
	audio := audios[0]

	withImage, withAudio := questionsByMedia(imported, imageID, audio.ID)
	if got, want := withImage, 2; got != want {
		t.Errorf("questions referencing the restored image = %d, want %d", got, want)
	}
	if got, want := withAudio, 1; got != want {
		t.Errorf("questions referencing the restored audio = %d, want %d", got, want)
	}

	assertAudioMeta(t, imported, audio)
}

func splitByType(rows []*media.Media) (images, audios []*media.Media) {
	for _, m := range rows {
		if m.Type == media.TypeAudio {
			audios = append(audios, m)

			continue
		}
		images = append(images, m)
	}

	return images, audios
}

// questionsByMedia counts how many questions reference the given image and audio
// ids.
func questionsByMedia(imported *quiz.Quiz, imageID, audioID int64) (withImage, withAudio int) {
	for _, q := range imported.Questions {
		if q.ImageMediaID != nil && *q.ImageMediaID == imageID {
			withImage++
		}
		if q.AudioMediaID != nil && *q.AudioMediaID == audioID {
			withAudio++
		}
	}

	return withImage, withAudio
}

func assertAudioMeta(t *testing.T, imported *quiz.Quiz, audio *media.Media) {
	t.Helper()

	if got, want := audio.Description, "Theme"; got != want {
		t.Errorf("audio Description = %q, want %q", got, want)
	}
	if audio.DurationMs == nil {
		t.Fatal("audio DurationMs = nil, want 1234")
	}
	if got, want := *audio.DurationMs, 1234; got != want {
		t.Errorf("audio DurationMs = %d, want %d", got, want)
	}

	// The repeat flag rides on the question, not the media row.
	for _, q := range imported.Questions {
		if q.AudioMediaID != nil && *q.AudioMediaID == audio.ID {
			if !q.AudioRepeat {
				t.Error("imported question AudioRepeat = false, want true")
			}
		}
	}
}

// TestHandleQuizImportArchive_VisibilityOverride pins that an explicit form
// visibility/mode override wins over the manifest's values.
func TestHandleQuizImportArchive_VisibilityOverride(t *testing.T) {
	t.Parallel()

	archiveBytes := exportArchiveBytes(t)

	env := newAdminEnv(t)
	mediaSvc := newMediaServiceOverTemp(t, env)
	handler := newImportHandler(t, env, mediaSvc, defaultImportLimits())

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, importRequest(t, archiveBytes, quiz.VisibilityPrivate, quiz.ModeSolo, importAdmin()))

	if got, want := rr.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d (body: %s)", got, want, rr.Body.String())
	}

	imported := onlyQuiz(t, env)
	if got, want := imported.Visibility, quiz.VisibilityPrivate; got != want {
		t.Errorf("Visibility = %q, want %q (form override should win)", got, want)
	}
	if got, want := imported.Mode, quiz.ModeSolo; got != want {
		t.Errorf("Mode = %q, want %q (form override should win)", got, want)
	}
}

// TestHandleQuizImportArchive_SlugCollision pins the 409: importing a quiz whose
// title yields a slug already in use returns 409 and leaves no second quiz or
// orphan media behind.
func TestHandleQuizImportArchive_SlugCollision(t *testing.T) {
	t.Parallel()

	archiveBytes := exportArchiveBytes(t)

	env := newAdminEnv(t)
	mediaSvc := newMediaServiceOverTemp(t, env)
	handler := newImportHandler(t, env, mediaSvc, defaultImportLimits())

	// First import succeeds.
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, importRequest(t, archiveBytes, "", "", importAdmin()))
	if got, want := rr.Code, http.StatusSeeOther; got != want {
		t.Fatalf("first import status = %d, want %d", got, want)
	}

	// Second import of the same archive collides on slug.
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, importRequest(t, archiveBytes, "", "", importAdmin()))
	if got, want := rr.Code, http.StatusConflict; got != want {
		t.Fatalf("second import status = %d, want %d", got, want)
	}

	quizzes, err := env.quizzes.ListQuizzes(t.Context())
	if err != nil {
		t.Fatalf("ListQuizzes err = %v, want nil", err)
	}
	if got, want := len(quizzes), 1; got != want {
		t.Errorf("quiz count after collision = %d, want %d (no partial second quiz)", got, want)
	}
}

// TestHandleQuizImportArchive_Rejections pins the malformed / oversized archive
// guards: corrupt zip, missing quiz.json, and a too-new format version each
// return 400, and none creates a quiz.
func TestHandleQuizImportArchive_Rejections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		archive func(t *testing.T) []byte
		want    int
	}{
		{
			name:    "corrupt zip",
			archive: func(*testing.T) []byte { return []byte("this is not a zip file at all") },
			want:    http.StatusBadRequest,
		},
		{
			name:    "missing manifest",
			archive: zipWithoutManifest,
			want:    http.StatusBadRequest,
		},
		{
			name:    "newer format version",
			archive: zipWithNewerVersion,
			want:    http.StatusBadRequest,
		},
		{
			name:    "too many entries",
			archive: zipWithTooManyEntries,
			want:    http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := newAdminEnv(t)
			mediaSvc := newMediaServiceOverTemp(t, env)
			handler := newImportHandler(t, env, mediaSvc, defaultImportLimits())

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, importRequest(t, tt.archive(t), "", "", importAdmin()))

			if got, want := rr.Code, tt.want; got != want {
				t.Fatalf("status = %d, want %d (body: %s)", got, want, rr.Body.String())
			}
			assertNoQuiz(t, env)
		})
	}
}

// TestHandleQuizImportArchive_OversizedEntry pins the zip-bomb per-entry guard:
// an archive whose media entry exceeds the (deliberately tiny) per-entry cap is
// rejected 400 before any media is stored, and no quiz is created.
func TestHandleQuizImportArchive_OversizedEntry(t *testing.T) {
	t.Parallel()

	archiveBytes := exportArchiveBytes(t)

	env := newAdminEnv(t)
	mediaSvc := newMediaServiceOverTemp(t, env)
	// Image cap of 1 byte: the real archive's image entry is larger, so the
	// per-entry guard trips.
	limits := NewArchiveImportLimits(1, importTestAudioMax, importTestTotalMax)
	handler := newImportHandler(t, env, mediaSvc, limits)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, importRequest(t, archiveBytes, "", "", importAdmin()))

	if got, want := rr.Code, http.StatusBadRequest; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	assertNoQuiz(t, env)
}

// TestHandleQuizImportArchive_InvalidQuizRejected pins that the archive path runs
// the same quizForm.Valid gate the paste path runs (#1113): an untrusted manifest
// that is structurally invalid or carries an out-of-range time limit is rejected
// with a 400 (NOT a 500 from a DB CHECK), and no quiz is created.
func TestHandleQuizImportArchive_InvalidQuizRejected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		manifest []byte
	}{
		{
			name: "time limit out of range",
			manifest: []byte(`{
				"formatVersion": 1, "title": "Over Limit", "description": "d",
				"timeLimitSeconds": 9999,
				"questions": [{"text": "Q1", "options": [{"text": "a", "correct": true}]}]
			}`),
		},
		{
			name: "empty title",
			manifest: []byte(`{
				"formatVersion": 1, "title": "", "description": "d",
				"questions": [{"text": "Q1", "options": [{"text": "a", "correct": true}]}]
			}`),
		},
		{
			name: "question with no options",
			manifest: []byte(`{
				"formatVersion": 1, "title": "No Options", "description": "d",
				"questions": [{"text": "Q1", "options": []}]
			}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := newAdminEnv(t)
			mediaSvc := newMediaServiceOverTemp(t, env)
			handler := newImportHandler(t, env, mediaSvc, defaultImportLimits())

			archiveBytes := buildZip(t, map[string][]byte{"quiz.json": tt.manifest})

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, importRequest(t, archiveBytes, "", "", importAdmin()))

			if got, want := rr.Code, http.StatusBadRequest; got != want {
				t.Fatalf("status = %d, want %d (body: %s)", got, want, rr.Body.String())
			}
			assertNoQuiz(t, env)
		})
	}
}

// TestHandleQuizImportArchive_TotalTooLarge pins the total-uncompressed guard.
func TestHandleQuizImportArchive_TotalTooLarge(t *testing.T) {
	t.Parallel()

	archiveBytes := exportArchiveBytes(t)

	env := newAdminEnv(t)
	mediaSvc := newMediaServiceOverTemp(t, env)
	// Total cap of 1 byte: the summed uncompressed size exceeds it.
	limits := NewArchiveImportLimits(importTestImageMax, importTestAudioMax, 1)
	handler := newImportHandler(t, env, mediaSvc, limits)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, importRequest(t, archiveBytes, "", "", importAdmin()))

	if got, want := rr.Code, http.StatusBadRequest; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	assertNoQuiz(t, env)
}

// TestHandleQuizImportArchive_RollbackOnMediaFailure pins the rollback (#1113):
// an archive whose manifest references a media file the archive does not contain
// fails DURING restore, AFTER the quiz row is created. The handler must roll the
// import back - delete the quiz (cascading its media rows) and remove the
// quiz's on-disk media directory - so a failed import leaves no quiz row and no
// orphan files behind, and surfaces a 400 (a malformed client archive, not a
// server fault).
func TestHandleQuizImportArchive_RollbackOnMediaFailure(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	root := t.TempDir()
	mediaSvc := media.NewService(
		store.NewMediaStore(env.db, slog.New(slog.DiscardHandler)),
		root,
		importTestImageMax,
		importTestAudioMax,
		slog.New(slog.DiscardHandler),
	)
	handler := newImportHandler(t, env, mediaSvc, defaultImportLimits())

	// A valid manifest with one question whose image references media/1.jpg, but
	// the archive carries an image at media/1.jpg AND a second question whose
	// image references media/missing.jpg, which is absent. The first image stores
	// (creating the quiz dir), then the missing reference fails the restore and
	// triggers the rollback.
	archiveBytes := buildZip(t, map[string][]byte{
		"quiz.json":   rollbackManifest(),
		"media/1.jpg": tinyPNG(t),
	})

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, importRequest(t, archiveBytes, "", "", importAdmin()))

	if got, want := rr.Code, http.StatusBadRequest; got != want {
		t.Fatalf("status = %d, want %d (body: %s)", got, want, rr.Body.String())
	}
	assertNoQuiz(t, env)

	// No media rows survived the cascade, and no files were left on disk.
	rows := allMediaRows(t, env)
	if got, want := len(rows), 0; got != want {
		t.Errorf("media rows after rollback = %d, want %d", got, want)
	}
	assertMediaRootEmpty(t, root)
}

// rollbackManifest returns a manifest with two flat questions: the first
// references media/1.jpg (present in the test archive), the second references
// media/missing.jpg (absent), so the restore stores the first image then fails
// on the missing second, exercising the post-create rollback.
func rollbackManifest() []byte {
	return []byte(`{
		"formatVersion": 1,
		"title": "Rollback Quiz",
		"description": "d",
		"questions": [
			{
				"text": "Q1",
				"image": {"file": "media/1.jpg", "mime": "image/png"},
				"options": [{"text": "a", "correct": true}]
			},
			{
				"text": "Q2",
				"image": {"file": "media/missing.jpg", "mime": "image/png"},
				"options": [{"text": "b", "correct": true}]
			}
		]
	}`)
}

// allMediaRows returns every media row across every quiz, so a rollback test can
// assert the cascade dropped them all. It probes a small id range since the test
// DB starts empty.
func allMediaRows(t *testing.T, env *adminEnv) []*media.Media {
	t.Helper()

	var rows []*media.Media
	for quizID := int64(1); quizID <= 10; quizID++ {
		items, err := env.media.ListMediaByQuiz(t.Context(), quizID)
		if err != nil {
			t.Fatalf("ListMediaByQuiz(%d) err = %v, want nil", quizID, err)
		}
		rows = append(rows, items...)
	}

	return rows
}

// assertMediaRootEmpty fails if the media root holds any per-quiz directory with
// files, so a rolled-back import is proven to leave no orphan media on disk.
func assertMediaRootEmpty(t *testing.T, root string) {
	t.Helper()

	var leftovers []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			leftovers = append(leftovers, path)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walking media root err = %v, want nil", err)
	}
	if len(leftovers) != 0 {
		t.Errorf("media root holds %d leftover file(s) after rollback: %v", len(leftovers), leftovers)
	}
}

// TestHandleQuizImportArchive_BudgetExhausted pins the per-host import budget:
// a second import within the window is rejected with 429, and a malformed-archive
// 400 does NOT spend the budget (the charge happens only once the archive is
// validated and about to be restored, mirroring the upload route's "a clear
// denial does not also spend the budget" rule).
func TestHandleQuizImportArchive_BudgetExhausted(t *testing.T) {
	t.Parallel()

	archiveBytes := exportArchiveBytes(t)

	env := newAdminEnv(t)
	mediaSvc := newMediaServiceOverTemp(t, env)
	// Budget of one import per (long) window so the second real import is denied.
	budget := mediahttp.NewUploadBudgetLimiter(1, time.Hour)
	handler := HandleQuizImportArchive(env.logger, nil, env.quizzes, mediaSvc, budget, defaultImportLimits())

	// A malformed archive (corrupt zip) is rejected 400 and must NOT spend the
	// single budget unit.
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, importRequest(t, []byte("not a zip"), "", "", importAdmin()))
	if got, want := rr.Code, http.StatusBadRequest; got != want {
		t.Fatalf("malformed import status = %d, want %d", got, want)
	}

	// The first valid import succeeds (proving the malformed attempt did not
	// consume the budget) ...
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, importRequest(t, archiveBytes, "", "", importAdmin()))
	if got, want := rr.Code, http.StatusSeeOther; got != want {
		t.Fatalf("first import status = %d, want %d (malformed attempt must not spend budget)", got, want)
	}

	// ... and the second valid import is over budget (429). Use a fresh title so a
	// slug collision cannot mask the rate-limit result.
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, importRequest(t, retitledArchive(t), "", "", importAdmin()))
	if got, want := rr.Code, http.StatusTooManyRequests; got != want {
		t.Fatalf("second import status = %d, want %d", got, want)
	}
}

// retitledArchive returns a second exported archive with a different title (and
// so a different slug), so a budget test's second import is denied by the budget
// rather than a slug collision.
func retitledArchive(t *testing.T) []byte {
	t.Helper()

	env := newAdminEnv(t)
	mediaSvc := newMediaServiceOverTemp(t, env)
	qz := roundedQuiz()
	qz.Title = "Other Capitals"
	qz.Slug = "other-capitals"
	seeded := env.seedQuiz(t, qz)

	var buf bytes.Buffer
	if err := WriteQuizArchive(t.Context(), &buf, env.quizzes, mediaSvc, seeded.ID); err != nil {
		t.Fatalf("WriteQuizArchive err = %v, want nil", err)
	}

	return buf.Bytes()
}

func assertNoQuiz(t *testing.T, env *adminEnv) {
	t.Helper()

	quizzes, err := env.quizzes.ListQuizzes(t.Context())
	if err != nil {
		t.Fatalf("ListQuizzes err = %v, want nil", err)
	}
	if got, want := len(quizzes), 0; got != want {
		t.Errorf("quiz count = %d, want %d (rejection must leave nothing behind)", got, want)
	}
}

// zipWithoutManifest returns a valid zip that has a media file but no quiz.json.
func zipWithoutManifest(t *testing.T) []byte {
	t.Helper()

	return buildZip(t, map[string][]byte{"media/1.jpg": tinyPNG(t)})
}

// zipWithNewerVersion returns a valid zip whose quiz.json declares a
// formatVersion newer than this build supports.
func zipWithNewerVersion(t *testing.T) []byte {
	t.Helper()

	manifest := []byte(`{"formatVersion": 9999, "title": "Too New", "questions": []}`)

	return buildZip(t, map[string][]byte{"quiz.json": manifest})
}

// zipWithTooManyEntries returns a valid zip carrying more than the entry-count
// cap, so checkArchiveLimits rejects it before any entry is read.
func zipWithTooManyEntries(t *testing.T) []byte {
	t.Helper()

	files := make(map[string][]byte, 1100)
	files["quiz.json"] = []byte(`{"formatVersion":1,"title":"x","questions":[]}`)
	for i := range 1100 {
		files["media/"+strconv.Itoa(i)+".jpg"] = []byte{0x00}
	}

	return buildZip(t, files)
}

// buildZip writes the given name->bytes map into an in-memory zip and returns
// the bytes.
func buildZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip Create %q err = %v, want nil", name, err)
		}
		if _, err = w.Write(data); err != nil {
			t.Fatalf("zip Write %q err = %v, want nil", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip Close err = %v, want nil", err)
	}

	return buf.Bytes()
}
