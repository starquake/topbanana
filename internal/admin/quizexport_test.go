package admin_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// exportManifest is the decoded shape of the archive's quiz.json. It restates
// the manifest fields the export test reads back; the production types are
// unexported, so the test decodes into its own mirror.
type exportManifest struct {
	FormatVersion    int              `json:"formatVersion"`
	Title            string           `json:"title"`
	Description      string           `json:"description"`
	TimeLimitSeconds *int             `json:"timeLimitSeconds"`
	Visibility       string           `json:"visibility"`
	Mode             string           `json:"mode"`
	Questions        []exportQuestion `json:"questions"`
	Rounds           []exportRound    `json:"rounds"`
}

type exportRound struct {
	Title                   string           `json:"title"`
	Summary                 string           `json:"summary"`
	BoundaryDurationSeconds *int             `json:"boundaryDurationSeconds"`
	Questions               []exportQuestion `json:"questions"`
}

type exportQuestion struct {
	Text             string          `json:"text"`
	TimeLimitSeconds *int            `json:"timeLimitSeconds"`
	Image            *exportImageRef `json:"image"`
	Audio            *exportAudioRef `json:"audio"`
	Options          []exportOption  `json:"options"`
}

type exportOption struct {
	Text    string `json:"text"`
	Correct bool   `json:"correct"`
}

type exportImageRef struct {
	File string `json:"file"`
	MIME string `json:"mime"`
}

type exportAudioRef struct {
	File        string `json:"file"`
	MIME        string `json:"mime"`
	Description string `json:"description"`
	DurationMs  *int   `json:"durationMs"`
	Repeat      bool   `json:"repeat"`
}

// tinyPNG returns a small valid 4x4 PNG, a valid upload StoreImage can process.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			img.Set(x, y, color.RGBA{R: uint8(x * 40), G: uint8(y * 40), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode err = %v, want nil", err)
	}

	return buf.Bytes()
}

// tinyMP3 returns a minimal payload that sniffs as MP3 via the ID3 magic, a
// valid upload StoreAudio accepts. The trailing bytes give the row a size.
func tinyMP3() []byte {
	return append([]byte("ID3"), bytes.Repeat([]byte{0x00}, 32)...)
}

// newMediaServiceOverTemp builds a real media Service writing under a fresh
// temp dir, sharing the env's DB so stored rows resolve through the same store
// the quiz reads.
func newMediaServiceOverTemp(t *testing.T, e *adminEnv) *media.Service {
	t.Helper()

	return media.NewService(
		store.NewMediaStore(e.db, slog.New(slog.DiscardHandler)),
		t.TempDir(),
		10<<20,
		20<<20,
		slog.New(slog.DiscardHandler),
	)
}

// attachMedia sets the question's image/audio ids (0 leaves a field unset) and
// persists via the real update path, so the exported quiz tree reflects it.
func attachMedia(t *testing.T, e *adminEnv, q *quiz.Question, imageID, audioID int64, repeat bool) {
	t.Helper()

	if imageID != 0 {
		q.ImageMediaID = &imageID
	}
	if audioID != 0 {
		q.AudioMediaID = &audioID
		q.AudioRepeat = repeat
	}
	if err := e.quizzes.UpdateQuestion(t.Context(), q); err != nil {
		t.Fatalf("UpdateQuestion err = %v, want nil", err)
	}
}

// roundedQuiz returns an owned quiz authored with two rounds, so the export
// exercises the rounds[] path. The first round's two questions hold the media
// attachments; the second round has a plain question.
func roundedQuiz() *quiz.Quiz {
	qz := ownedQuiz("Capitals", "capitals")
	qz.Description = "A tour of capitals."
	qz.TimeLimitSeconds = 12
	qz.Mode = quiz.ModeLive
	qz.Rounds = []*quiz.Round{
		{
			Title:   "Warm-up",
			Summary: "An easy start.",
			Questions: []*quiz.Question{
				{
					Text:     "Capital of France?",
					Position: 1,
					Options: []*quiz.Option{
						{Text: "Paris", Correct: true},
						{Text: "Lyon", Correct: false},
					},
				},
				{
					Text:     "Capital of Spain?",
					Position: 2,
					Options: []*quiz.Option{
						{Text: "Madrid", Correct: true},
						{Text: "Seville", Correct: false},
					},
				},
			},
		},
		{
			Title:                   "Finish",
			BoundaryDurationSeconds: ptr(15),
			Questions: []*quiz.Question{
				{
					Text:     "Capital of Italy?",
					Position: 3,
					Options: []*quiz.Option{
						{Text: "Rome", Correct: true},
						{Text: "Milan", Correct: false},
					},
				},
			},
		},
	}

	return qz
}

func ptr(v int) *int { return &v }

// readArchive unzips the export bytes into a name->bytes map and decodes the
// manifest, failing the test on any structural problem.
func readArchive(t *testing.T, raw []byte) (map[string][]byte, exportManifest) {
	t.Helper()

	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("zip.NewReader err = %v, want nil", err)
	}
	files := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		rc, openErr := f.Open()
		if openErr != nil {
			t.Fatalf("open %q err = %v, want nil", f.Name, openErr)
		}
		data, readErr := io.ReadAll(rc)
		_ = rc.Close()
		if readErr != nil {
			t.Fatalf("read %q err = %v, want nil", f.Name, readErr)
		}
		files[f.Name] = data
	}

	manifestBytes, ok := files["quiz.json"]
	if !ok {
		t.Fatal("archive missing quiz.json")
	}
	var manifest exportManifest
	if err = json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("decode quiz.json err = %v, want nil", err)
	}

	return files, manifest
}

// TestWriteQuizArchive pins the export: the manifest carries the quiz fields
// and the rounds/questions/options, the image + audio refs resolve to bundled
// media files, and a media id attached to two questions yields exactly one
// archive file with both questions referencing it.
func TestWriteQuizArchive(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	mediaSvc := newMediaServiceOverTemp(t, env)

	qz := env.seedQuiz(t, roundedQuiz())

	// Store one image + one audio clip in the quiz's library.
	img, err := mediaSvc.StoreImage(t.Context(), qz.ID, testExportPlayerID, "pic.png", bytes.NewReader(tinyPNG(t)))
	if err != nil {
		t.Fatalf("StoreImage err = %v, want nil", err)
	}
	aud, err := mediaSvc.StoreAudio(
		t.Context(), qz.ID, testExportPlayerID, 1234, "Theme", "theme.mp3", bytes.NewReader(tinyMP3()),
	)
	if err != nil {
		t.Fatalf("StoreAudio err = %v, want nil", err)
	}

	// Attach the SAME image to both first-round questions (dedupe case) and the
	// audio to the first question with repeat on.
	r0 := qz.Rounds[0]
	attachMedia(t, env, r0.Questions[0], img.ID, aud.ID, true)
	attachMedia(t, env, r0.Questions[1], img.ID, 0, false)

	var buf bytes.Buffer
	if err = WriteQuizArchive(t.Context(), &buf, env.quizzes, mediaSvc, qz.ID); err != nil {
		t.Fatalf("WriteQuizArchive err = %v, want nil", err)
	}

	files, manifest := readArchive(t, buf.Bytes())

	assertManifestHeader(t, manifest)
	assertRounds(t, manifest, img, aud)
	assertMediaFiles(t, files, img, aud)
	assertImageDedupe(t, files, manifest, img)
}

// flatQuizWithQuestions returns an owned quiz with two top-level questions and
// no authored rounds, so the store drops them into its single auto-stamped
// "Round 1" default round - the shape isFlatQuiz must recognise as flat.
func flatQuizWithQuestions(title, slug string) *quiz.Quiz {
	qz := ownedQuiz(title, slug)
	qz.Questions = []*quiz.Question{
		{
			Text:     "Capital of France?",
			Position: 1,
			Options: []*quiz.Option{
				{Text: "Paris", Correct: true},
				{Text: "Lyon", Correct: false},
			},
		},
		{
			Text:     "Capital of Spain?",
			Position: 2,
			Options: []*quiz.Option{
				{Text: "Madrid", Correct: true},
				{Text: "Seville", Correct: false},
			},
		},
	}

	return qz
}

// TestFlatQuizDetection pins isFlatQuiz against the store's default-round
// behaviour (the auto-stamped "Round 1"): a quiz that has never had a round
// authored - whether it has zero questions or several - exports a flat
// top-level questions[] with an empty rounds[]. A regression that wrongly
// emitted rounds[] for such a quiz fails here. The defaultExportRoundTitle
// constant's doc comment points at this test.
func TestFlatQuizDetection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		quiz          *quiz.Quiz
		wantQuestions int
	}{
		{
			name:          "zero-question flat quiz",
			quiz:          ownedQuiz("Empty", "empty-flat"),
			wantQuestions: 0,
		},
		{
			name:          "populated flat quiz",
			quiz:          flatQuizWithQuestions("Capitals", "capitals-flat"),
			wantQuestions: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := newAdminEnv(t)
			mediaSvc := newMediaServiceOverTemp(t, env)
			qz := env.seedQuiz(t, tt.quiz)

			var buf bytes.Buffer
			if err := WriteQuizArchive(t.Context(), &buf, env.quizzes, mediaSvc, qz.ID); err != nil {
				t.Fatalf("WriteQuizArchive err = %v, want nil", err)
			}

			_, manifest := readArchive(t, buf.Bytes())

			// Flat quiz: rounds[] is empty and the questions live at the top level.
			if got, want := len(manifest.Rounds), 0; got != want {
				t.Errorf("len(Rounds) = %d, want %d (flat quiz emits no rounds[])", got, want)
			}
			if got, want := len(manifest.Questions), tt.wantQuestions; got != want {
				t.Errorf("len(Questions) = %d, want %d", got, want)
			}
		})
	}
}

// TestArchiveExtForMedia pins the MIME-to-extension mapping for every branch,
// including the fallback to the stored file's extension and the final .bin.
func TestArchiveExtForMedia(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mime string
		path string
		want string
	}{
		{name: "jpeg", mime: "image/jpeg", path: "1/1.jpg", want: ".jpg"},
		{name: "png", mime: "image/png", path: "1/1.png", want: ".png"},
		{name: "webp", mime: "image/webp", path: "1/1.webp", want: ".webp"},
		{name: "gif", mime: "image/gif", path: "1/1.gif", want: ".gif"},
		{name: "mp3", mime: "audio/mpeg", path: "1/1.mp3", want: ".mp3"},
		{name: "m4a", mime: "audio/mp4", path: "1/1.m4a", want: ".m4a"},
		{name: "ogg", mime: "audio/ogg", path: "1/1.ogg", want: ".ogg"},
		{name: "wav", mime: "audio/wav", path: "1/1.wav", want: ".wav"},
		{name: "unknown mime uses path ext", mime: "application/octet-stream", path: "1/1.dat", want: ".dat"},
		{name: "unknown mime no ext is bin", mime: "application/octet-stream", path: "1/1", want: ".bin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got, want := ArchiveExtForMedia(&media.Media{MIME: tt.mime, Path: tt.path}), tt.want; got != want {
				t.Errorf("ArchiveExtForMedia(MIME=%q, Path=%q) = %q, want %q", tt.mime, tt.path, got, want)
			}
		})
	}
}

// exportRequest builds a GET export request for the given quiz id with the
// supplied player on its context, the way the route's auth middleware would.
func exportRequest(t *testing.T, quizID int64, player *auth.Player) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/admin/quizzes/"+strconv.FormatInt(quizID, 10)+"/export", nil,
	)
	req.SetPathValue("quizID", strconv.FormatInt(quizID, 10))

	return req.WithContext(auth.WithPlayer(req.Context(), player))
}

// TestHandleQuizExport pins the handler: a missing quiz is a 404, a non-owner
// non-admin is a 403, and the owner gets a 200 zip with the slug filename.
func TestHandleQuizExport(t *testing.T) {
	t.Parallel()

	t.Run("owner downloads zip", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		mediaSvc := newMediaServiceOverTemp(t, env)
		qz := env.seedQuiz(t, ownedQuiz("Solo Quiz", "solo-quiz"))

		rr := httptest.NewRecorder()
		HandleQuizExport(env.logger, env.quizzes, mediaSvc).ServeHTTP(
			rr, exportRequest(t, qz.ID, &auth.Player{ID: testAdminID, Role: auth.RoleAdmin}),
		)

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if got, want := rr.Header().Get("Content-Type"), "application/zip"; got != want {
			t.Errorf("Content-Type = %q, want %q", got, want)
		}
		if got, want := rr.Header().Get("Content-Disposition"), `attachment; filename="solo-quiz.zip"`; got != want {
			t.Errorf("Content-Disposition = %q, want %q", got, want)
		}
		// The body is a real zip carrying the manifest.
		_, manifest := readArchive(t, rr.Body.Bytes())
		if got, want := manifest.Title, "Solo Quiz"; got != want {
			t.Errorf("manifest Title = %q, want %q", got, want)
		}
	})

	t.Run("missing quiz is 404", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		mediaSvc := newMediaServiceOverTemp(t, env)

		rr := httptest.NewRecorder()
		HandleQuizExport(env.logger, env.quizzes, mediaSvc).ServeHTTP(
			rr, exportRequest(t, 9999, &auth.Player{ID: testAdminID, Role: auth.RoleAdmin}),
		)

		if got, want := rr.Code, http.StatusNotFound; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
	})

	t.Run("non-owner is 403", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		mediaSvc := newMediaServiceOverTemp(t, env)
		qz := env.seedQuiz(t, ownedQuiz("Owned", "owned-quiz"))

		rr := httptest.NewRecorder()
		// A signed-in player who is neither the creator nor an admin.
		HandleQuizExport(env.logger, env.quizzes, mediaSvc).ServeHTTP(
			rr, exportRequest(t, qz.ID, &auth.Player{ID: nonOwnerID, Role: auth.RolePlayer}),
		)

		if got, want := rr.Code, http.StatusForbidden; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
	})
}

func assertManifestHeader(t *testing.T, manifest exportManifest) {
	t.Helper()

	if got, want := manifest.FormatVersion, ArchiveFormatVersion; got != want {
		t.Errorf("FormatVersion = %d, want %d", got, want)
	}
	if got, want := manifest.Title, "Capitals"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := manifest.Description, "A tour of capitals."; got != want {
		t.Errorf("Description = %q, want %q", got, want)
	}
	if manifest.TimeLimitSeconds == nil {
		t.Fatal("TimeLimitSeconds = nil, want 12")
	}
	if got, want := *manifest.TimeLimitSeconds, 12; got != want {
		t.Errorf("TimeLimitSeconds = %d, want %d", got, want)
	}
	if got, want := manifest.Visibility, quiz.VisibilityPublic; got != want {
		t.Errorf("Visibility = %q, want %q", got, want)
	}
	if got, want := manifest.Mode, quiz.ModeLive; got != want {
		t.Errorf("Mode = %q, want %q", got, want)
	}
	// A multi-round quiz exports rounds[], not a flat questions[].
	if len(manifest.Questions) != 0 {
		t.Errorf("top-level Questions = %d entries, want 0 (rounds[] path)", len(manifest.Questions))
	}
	if got, want := len(manifest.Rounds), 2; got != want {
		t.Fatalf("len(Rounds) = %d, want %d", got, want)
	}
}

func assertRounds(t *testing.T, manifest exportManifest, img, aud *media.Media) {
	t.Helper()

	first := manifest.Rounds[0]
	if got, want := first.Title, "Warm-up"; got != want {
		t.Errorf("Rounds[0].Title = %q, want %q", got, want)
	}
	if got, want := first.Summary, "An easy start."; got != want {
		t.Errorf("Rounds[0].Summary = %q, want %q", got, want)
	}
	if got, want := len(first.Questions), 2; got != want {
		t.Fatalf("Rounds[0] questions = %d, want %d", got, want)
	}

	q0 := first.Questions[0]
	if got, want := q0.Text, "Capital of France?"; got != want {
		t.Errorf("Rounds[0].Questions[0].Text = %q, want %q", got, want)
	}
	if got, want := len(q0.Options), 2; got != want {
		t.Fatalf("Rounds[0].Questions[0] options = %d, want %d", got, want)
	}
	if got, want := q0.Options[0].Text, "Paris"; got != want {
		t.Errorf("option[0].Text = %q, want %q", got, want)
	}
	if !q0.Options[0].Correct {
		t.Error("option[0].Correct = false, want true")
	}

	assertImageRef(t, q0.Image, img)
	assertAudioRef(t, q0.Audio, aud)

	second := manifest.Rounds[1]
	if got, want := second.Title, "Finish"; got != want {
		t.Errorf("Rounds[1].Title = %q, want %q", got, want)
	}
	if second.BoundaryDurationSeconds == nil {
		t.Fatal("Rounds[1].BoundaryDurationSeconds = nil, want 15")
	}
	if got, want := *second.BoundaryDurationSeconds, 15; got != want {
		t.Errorf("Rounds[1].BoundaryDurationSeconds = %d, want %d", got, want)
	}
}

func assertImageRef(t *testing.T, ref *exportImageRef, img *media.Media) {
	t.Helper()

	if ref == nil {
		t.Fatal("question image ref = nil, want set")
	}
	if got, want := ref.MIME, img.MIME; got != want {
		t.Errorf("image ref MIME = %q, want %q", got, want)
	}
	if got, want := ref.File, "media/"+strconv.FormatInt(img.ID, 10)+".jpg"; got != want {
		t.Errorf("image ref File = %q, want %q", got, want)
	}
}

func assertAudioRef(t *testing.T, ref *exportAudioRef, aud *media.Media) {
	t.Helper()

	if ref == nil {
		t.Fatal("question audio ref = nil, want set")
	}
	if got, want := ref.MIME, aud.MIME; got != want {
		t.Errorf("audio ref MIME = %q, want %q", got, want)
	}
	if got, want := ref.File, "media/"+strconv.FormatInt(aud.ID, 10)+".mp3"; got != want {
		t.Errorf("audio ref File = %q, want %q", got, want)
	}
	if got, want := ref.Description, "Theme"; got != want {
		t.Errorf("audio ref Description = %q, want %q", got, want)
	}
	if ref.DurationMs == nil {
		t.Fatal("audio ref DurationMs = nil, want 1234")
	}
	if got, want := *ref.DurationMs, 1234; got != want {
		t.Errorf("audio ref DurationMs = %d, want %d", got, want)
	}
	if !ref.Repeat {
		t.Error("audio ref Repeat = false, want true")
	}
}

func assertMediaFiles(t *testing.T, files map[string][]byte, img, aud *media.Media) {
	t.Helper()

	imagePath := "media/" + strconv.FormatInt(img.ID, 10) + ".jpg"
	if got, ok := files[imagePath]; !ok || len(got) == 0 {
		t.Errorf("archive missing non-empty %q (ok=%t, len=%d)", imagePath, ok, len(got))
	}
	audioPath := "media/" + strconv.FormatInt(aud.ID, 10) + ".mp3"
	if got, ok := files[audioPath]; !ok || len(got) == 0 {
		t.Errorf("archive missing non-empty %q (ok=%t, len=%d)", audioPath, ok, len(got))
	}
}

func assertImageDedupe(t *testing.T, files map[string][]byte, manifest exportManifest, img *media.Media) {
	t.Helper()

	imagePath := "media/" + strconv.FormatInt(img.ID, 10) + ".jpg"

	// The shared image is present once (a map key is unique), and only two
	// media files total exist - the image and the audio - so the image
	// referenced by both first-round questions was not duplicated.
	if _, ok := files[imagePath]; !ok {
		t.Errorf("archive missing shared image %q", imagePath)
	}
	mediaFiles := 0
	for name := range files {
		if strings.HasPrefix(name, "media/") {
			mediaFiles++
		}
	}
	if got, want := mediaFiles, 2; got != want {
		t.Errorf("media files in archive = %d, want %d (one image + one audio, deduped)", got, want)
	}

	// Both first-round questions reference that same file.
	refs := 0
	for _, q := range manifest.Rounds[0].Questions {
		if q.Image != nil && q.Image.File == imagePath {
			refs++
		}
	}
	if got, want := refs, 2; got != want {
		t.Errorf("questions referencing %q = %d, want %d", imagePath, got, want)
	}
}

// testExportPlayerID is the uploading player for the export fixtures; the
// seeded admin (id 1) owns the quiz, so attribute uploads to it.
const testExportPlayerID int64 = 1
