package admin

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strconv"

	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/quiz"
)

// archiveDecimalBase is the base used to render media and quiz ids into the
// archive's media filenames and the fallback download filename.
const archiveDecimalBase = 10

// MediaArchiver is the slice of the media service the exporter needs: resolve
// a media row's metadata and open its stored full file for reading. Defined
// consumer-side so the admin package depends only on the two reads it makes;
// the concrete *media.Service satisfies it. Open returns the concrete
// [os.File] the service hands back; the exporter only reads and closes it.
type MediaArchiver interface {
	Get(ctx context.Context, id int64) (*media.Media, error)
	Open(relPath string) (*os.File, error)
}

// archiveExtForMedia returns the archive filename extension for a stored media
// row, derived from its MIME with a fallback to the stored file's extension.
// The importer reads media/<id>.<ext> back, so the extension only has to be a
// stable, recognisable label for the bytes.
func archiveExtForMedia(m *media.Media) string {
	switch m.MIME {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "audio/mpeg":
		return ".mp3"
	case "audio/mp4":
		return ".m4a"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav":
		return ".wav"
	default:
		if ext := path.Ext(m.Path); ext != "" {
			return ext
		}

		return ".bin"
	}
}

// archiveMediaPath is the archive-relative path of a media file, keyed by the
// media id so the same id referenced by several questions maps to one file.
func archiveMediaPath(m *media.Media) string {
	return "media/" + strconv.FormatInt(m.ID, archiveDecimalBase) + archiveExtForMedia(m)
}

// quizSlugFilename returns the download filename for a quiz archive: its slug
// plus ".zip", falling back to the quiz id when the slug is somehow empty so
// the Content-Disposition header always names a file.
func quizSlugFilename(qz *quiz.Quiz) string {
	slug := qz.Slug
	if slug == "" {
		slug = "quiz-" + strconv.FormatInt(qz.ID, archiveDecimalBase)
	}

	return slug + ".zip"
}

// defaultExportRoundTitle is the title the store stamps on every quiz's
// auto-created round. A single round still carrying it marks an unauthored
// (flat) quiz. Invariant pinned by TestFlatQuizDetection.
const defaultExportRoundTitle = "Round 1"

// isFlatQuiz reports whether the quiz should export a flat top-level
// questions[] rather than rounds[]: true only for a single round still named
// the default "Round 1", which is exactly what importing a flat questions[]
// produces.
func isFlatQuiz(rounds []*quiz.Round) bool {
	return len(rounds) == 1 && rounds[0].Title == defaultExportRoundTitle
}

// timeLimitPtr returns a pointer to the quiz time limit, or nil for the zero
// value so the manifest omits it. The quiz default is always non-zero in
// practice (the store backfills it), but a zero stays omitted for cleanliness.
func timeLimitPtr(v int) *int {
	if v == 0 {
		return nil
	}

	return &v
}

// manifestBuilder collects the unique referenced media as it builds the
// manifest, deduping by media id so a row referenced by several questions is
// loaded once and written to the archive once. media holds the resolved rows
// keyed by id; the archive's media files are written from it after the
// manifest is assembled.
type manifestBuilder struct {
	svc   MediaArchiver
	media map[int64]*media.Media
}

func newManifestBuilder(svc MediaArchiver) *manifestBuilder {
	return &manifestBuilder{svc: svc, media: make(map[int64]*media.Media)}
}

// build assembles the full manifest for the quiz. It groups the quiz's
// questions under their rounds in position order and decides the XOR shape:
// a quiz with a single unauthored default round exports a flat top-level
// questions[]; anything else (a renamed round, or more than one round) exports
// rounds[]. This mirrors the importer, where a flat questions[] lands every
// question in the single default round.
func (b *manifestBuilder) build(
	ctx context.Context, qz *quiz.Quiz, rounds []*quiz.Round,
) (quizArchiveManifest, error) {
	manifest := quizArchiveManifest{
		FormatVersion:    archiveFormatVersion,
		Title:            qz.Title,
		Description:      qz.Description,
		TimeLimitSeconds: timeLimitPtr(qz.TimeLimitSeconds),
		Visibility:       qz.Visibility,
		Mode:             qz.Mode,
	}

	byRound := make(map[int64][]*quiz.Question, len(rounds))
	for _, q := range qz.Questions {
		byRound[q.RoundID] = append(byRound[q.RoundID], q)
	}

	if isFlatQuiz(rounds) {
		questions, err := b.questions(ctx, byRound[rounds[0].ID])
		if err != nil {
			return quizArchiveManifest{}, err
		}
		manifest.Questions = questions

		return manifest, nil
	}

	manifest.Rounds = make([]quizArchiveRound, 0, len(rounds))
	for _, rnd := range rounds {
		questions, err := b.questions(ctx, byRound[rnd.ID])
		if err != nil {
			return quizArchiveManifest{}, err
		}
		manifest.Rounds = append(manifest.Rounds, quizArchiveRound{
			Title:                   rnd.Title,
			Summary:                 rnd.Summary,
			BoundaryDurationSeconds: rnd.BoundaryDurationSeconds,
			Questions:               questions,
		})
	}

	return manifest, nil
}

// questions maps a round's questions onto their manifest entries.
func (b *manifestBuilder) questions(
	ctx context.Context, questions []*quiz.Question,
) ([]quizArchiveQuestion, error) {
	out := make([]quizArchiveQuestion, 0, len(questions))
	for _, q := range questions {
		entry, err := b.question(ctx, q)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}

	return out, nil
}

// question builds the manifest entry for one question, resolving its image and
// audio references and copying its options.
func (b *manifestBuilder) question(ctx context.Context, q *quiz.Question) (quizArchiveQuestion, error) {
	imageRef, err := b.imageRef(ctx, q.ImageMediaID)
	if err != nil {
		return quizArchiveQuestion{}, err
	}
	audioRef, err := b.audioRef(ctx, q.AudioMediaID, q.AudioRepeat)
	if err != nil {
		return quizArchiveQuestion{}, err
	}

	options := make([]quizArchiveOption, 0, len(q.Options))
	for _, o := range q.Options {
		options = append(options, quizArchiveOption{Text: o.Text, Correct: o.Correct})
	}

	return quizArchiveQuestion{
		Text:             q.Text,
		TimeLimitSeconds: q.TimeLimitSeconds,
		Image:            imageRef,
		Audio:            audioRef,
		Options:          options,
	}, nil
}

// imageRef resolves the image media id into a manifest ref, recording the
// referenced media for later file writing. A nil id yields a nil ref.
func (b *manifestBuilder) imageRef(ctx context.Context, id *int64) (*quizArchiveImageRef, error) {
	if id == nil {
		return nil, nil //nolint:nilnil // nil ref means "no image attached", the natural sentinel here.
	}
	m, err := b.resolve(ctx, *id)
	if err != nil {
		return nil, err
	}

	return &quizArchiveImageRef{File: archiveMediaPath(m), MIME: m.MIME}, nil
}

// audioRef resolves the audio media id into a manifest ref, carrying the clip's
// advisory metadata and the repeat flag. A nil id yields a nil ref.
func (b *manifestBuilder) audioRef(ctx context.Context, id *int64, repeat bool) (*quizArchiveAudioRef, error) {
	if id == nil {
		return nil, nil //nolint:nilnil // nil ref means "no audio attached", the natural sentinel here.
	}
	m, err := b.resolve(ctx, *id)
	if err != nil {
		return nil, err
	}

	return &quizArchiveAudioRef{
		File:        archiveMediaPath(m),
		MIME:        m.MIME,
		Description: m.Description,
		DurationMs:  m.DurationMs,
		Repeat:      repeat,
	}, nil
}

// resolve loads a media row once and caches it for the export, so a media id
// referenced by several questions is read from the store a single time.
func (b *manifestBuilder) resolve(ctx context.Context, id int64) (*media.Media, error) {
	if m, ok := b.media[id]; ok {
		return m, nil
	}
	m, err := b.svc.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("loading media %d for export: %w", id, err)
	}
	b.media[id] = m

	return m, nil
}

// writeQuizArchive builds the quiz's manifest and writes the archive (a .zip)
// to w: quiz.json plus one media/<id>.<ext> file per unique referenced media.
// It reads only via the quiz store and the media service; it persists nothing.
func writeQuizArchive(
	ctx context.Context, w io.Writer, quizStore quiz.Store, mediaSvc MediaArchiver, quizID int64,
) error {
	qz, err := quizStore.GetQuiz(ctx, quizID)
	if err != nil {
		return fmt.Errorf("loading quiz %d for export: %w", quizID, err)
	}
	rounds, err := quizStore.ListRoundsByQuiz(ctx, quizID)
	if err != nil {
		return fmt.Errorf("loading rounds for quiz %d export: %w", quizID, err)
	}

	builder := newManifestBuilder(mediaSvc)
	manifest, err := builder.build(ctx, qz, rounds)
	if err != nil {
		return err
	}

	zw := zip.NewWriter(w)
	if err = writeManifestEntry(zw, manifest); err != nil {
		return err
	}
	if err = writeMediaEntries(zw, mediaSvc, builder.media); err != nil {
		return err
	}
	if err = zw.Close(); err != nil {
		return fmt.Errorf("closing quiz archive: %w", err)
	}

	return nil
}

// writeManifestEntry writes the indented quiz.json into the archive.
func writeManifestEntry(zw *zip.Writer, manifest quizArchiveManifest) error {
	out, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding quiz manifest: %w", err)
	}
	entry, err := zw.Create("quiz.json")
	if err != nil {
		return fmt.Errorf("creating manifest entry: %w", err)
	}
	if _, err = entry.Write(out); err != nil {
		return fmt.Errorf("writing manifest entry: %w", err)
	}

	return nil
}

// writeMediaEntries copies each unique referenced media's full file into the
// archive. Export the full file (m.Path), not the thumbnail: import
// regenerates the thumbnail from the original.
func writeMediaEntries(zw *zip.Writer, mediaSvc MediaArchiver, items map[int64]*media.Media) error {
	for _, m := range items {
		if err := writeMediaEntry(zw, mediaSvc, m); err != nil {
			return err
		}
	}

	return nil
}

// writeMediaEntry copies one media file's bytes into the archive at its
// id-keyed path.
func writeMediaEntry(zw *zip.Writer, mediaSvc MediaArchiver, m *media.Media) error {
	src, err := mediaSvc.Open(m.Path)
	if err != nil {
		return fmt.Errorf("opening media %d file for export: %w", m.ID, err)
	}
	defer func() { _ = src.Close() }()

	entry, err := zw.Create(archiveMediaPath(m))
	if err != nil {
		return fmt.Errorf("creating media entry for %d: %w", m.ID, err)
	}
	if _, err = io.Copy(entry, src); err != nil {
		return fmt.Errorf("writing media entry for %d: %w", m.ID, err)
	}

	return nil
}

// HandleQuizExport returns the per-quiz export handler. It loads the quiz,
// enforces the same creator-or-admin edit gate the quiz view applies, then
// streams a .zip bundling the quiz manifest and its referenced media. The
// archive is buffered before any header is written so a build failure returns
// a clean 500 rather than a truncated body; quiz archives are admin-only and
// size-bounded, so buffering in memory is acceptable.
func HandleQuizExport(logger *slog.Logger, quizStore quiz.Store, mediaSvc MediaArchiver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}

		qz, err := quizStore.GetQuiz(r.Context(), quizID)
		if err != nil {
			if errors.Is(err, quiz.ErrQuizNotFound) || errors.Is(err, quiz.ErrQuestionNotFound) {
				logger.InfoContext(r.Context(), "quiz not found for export", slog.Any("err", err))
				http.NotFound(w, r)

				return
			}
			logger.ErrorContext(r.Context(), "error loading quiz for export", slog.Any("err", err))
			http.Error(w, "internal server error", http.StatusInternalServerError)

			return
		}

		// Same gate the quiz view's edit affordances use (#281/#538): the
		// session player must be the quiz creator or an admin.
		if !canEditQuiz(r, qz.CreatedByPlayerID) {
			http.Error(w, "forbidden", http.StatusForbidden)

			return
		}

		var buf bytes.Buffer
		if err = writeQuizArchive(r.Context(), &buf, quizStore, mediaSvc, quizID); err != nil {
			logger.ErrorContext(r.Context(), "error building quiz archive", slog.Any("err", err))
			http.Error(w, "internal server error", http.StatusInternalServerError)

			return
		}

		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+quizSlugFilename(qz)+"\"")
		w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
		if _, err = w.Write(buf.Bytes()); err != nil {
			logger.ErrorContext(r.Context(), "error writing quiz archive response", slog.Any("err", err))
		}
	})
}
