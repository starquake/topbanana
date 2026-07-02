package admin

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/quiz"
)

// importQuizWithMedia persists the built quiz, then restores its media and wires
// each referencing question to the new media ids. Any failure AFTER the quiz row
// is created rolls the whole import back: the quiz is deleted (the cascade drops
// its media rows) and its on-disk media directory removed, so a failed import
// leaves nothing behind. The slug-collision case is surfaced before any media is
// touched, so it needs no rollback.
func importQuizWithMedia(
	ctx context.Context, logger *slog.Logger, quizStore quiz.Store, mediaSvc MediaImporter,
	archive *zip.Reader, built builtArchiveQuiz, importerID int64,
) error {
	if err := storeQuiz(ctx, quizStore, built.quiz); err != nil {
		// storeQuiz wraps quiz.ErrSlugTaken; surface it unwrapped-by-Is to the
		// caller. Nothing was created on a slug collision, so no rollback.
		return err
	}

	if err := restoreArchiveMedia(ctx, quizStore, mediaSvc, archive, built, importerID); err != nil {
		rollbackImport(ctx, logger, quizStore, mediaSvc, built.quiz.ID)

		return err
	}

	return nil
}

// restoreArchiveMedia re-stores each UNIQUE archive media file referenced by the
// plan through the media pipeline (which validates and re-encodes the untrusted
// bytes), maps each archive file key to its new media id, then patches every
// referencing question's media references. Reusing a file key across questions
// yields a single media row.
func restoreArchiveMedia(
	ctx context.Context, quizStore quiz.Store, mediaSvc MediaImporter,
	archive *zip.Reader, built builtArchiveQuiz, importerID int64,
) error {
	r := &mediaRestorer{
		mediaSvc:   mediaSvc,
		archive:    archive,
		quizID:     built.quiz.ID,
		importerID: importerID,
		imageIDs:   map[string]int64{},
		audioIDs:   map[string]int64{},
	}

	for _, entry := range built.plan {
		imageID, err := r.resolveImage(ctx, entry.image)
		if err != nil {
			return err
		}
		audioID, err := r.resolveAudio(ctx, entry.audio)
		if err != nil {
			return err
		}
		if err = quizStore.SetQuestionMedia(
			ctx, entry.question.ID, imageID, audioID, audioRepeatFor(entry.audio),
		); err != nil {
			return fmt.Errorf("wiring restored media to question %d: %w", entry.question.ID, err)
		}
	}

	return nil
}

// audioRepeatFor reports the repeat flag for an audio ref, false when there is
// no audio.
func audioRepeatFor(ref *quizArchiveAudioRef) bool {
	return ref != nil && ref.Repeat
}

// mediaRestorer carries the per-import dependencies and dedupe state for
// restoring archive media: the maps keyed by archive file path mean a file
// referenced by several questions is stored exactly once.
type mediaRestorer struct {
	mediaSvc   MediaImporter
	archive    *zip.Reader
	quizID     int64
	importerID int64
	imageIDs   map[string]int64
	audioIDs   map[string]int64
}

// resolveImage returns the new media id for an image ref, storing the archive
// file the first time it is seen and returning the cached id on reuse. A nil ref
// yields a nil id (no image attached).
func (r *mediaRestorer) resolveImage(ctx context.Context, ref *quizArchiveImageRef) (*int64, error) {
	if ref == nil {
		return nil, nil //nolint:nilnil // nil id means "no image attached", the natural sentinel here.
	}
	if id, ok := r.imageIDs[ref.File]; ok {
		return &id, nil
	}

	rc, err := openArchiveFile(r.archive, ref.File)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()

	stored, err := r.mediaSvc.StoreImage(ctx, r.quizID, r.importerID, ref.File, rc)
	if err != nil {
		return nil, fmt.Errorf("restoring image %q: %w", ref.File, err)
	}
	r.imageIDs[ref.File] = stored.ID

	return &stored.ID, nil
}

// resolveAudio returns the new media id for an audio ref, storing the archive
// file the first time it is seen and returning the cached id on reuse. A nil ref
// yields a nil id (no audio attached). The clip's advisory description and
// duration round-trip from the manifest.
func (r *mediaRestorer) resolveAudio(ctx context.Context, ref *quizArchiveAudioRef) (*int64, error) {
	if ref == nil {
		return nil, nil //nolint:nilnil // nil id means "no audio attached", the natural sentinel here.
	}
	if id, ok := r.audioIDs[ref.File]; ok {
		return &id, nil
	}

	rc, err := openArchiveFile(r.archive, ref.File)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()

	stored, err := r.mediaSvc.StoreAudio(
		ctx, r.quizID, r.importerID, durationMsFor(ref.DurationMs), ref.Description, ref.File, rc,
	)
	if err != nil {
		return nil, fmt.Errorf("restoring audio %q: %w", ref.File, err)
	}
	r.audioIDs[ref.File] = stored.ID

	return &stored.ID, nil
}

// durationMsFor unwraps the manifest's optional duration into the int the media
// service takes; a nil (unknown) duration becomes 0, which the service stores as
// NULL.
func durationMsFor(ms *int) int {
	if ms == nil {
		return 0
	}

	return *ms
}

// openArchiveFile opens a media entry by its manifest-relative path. ZIP-SLIP is
// structurally avoided: the path names an entry inside the archive opened
// read-only and the bytes are handed straight to the media pipeline (which
// assigns its own confined on-disk paths) - this code never writes the entry's
// path to disk. A path the archive does not contain is a malformed archive.
func openArchiveFile(archive *zip.Reader, name string) (io.ReadCloser, error) {
	rc, err := archive.Open(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrArchiveMediaMissing, name)
	}

	return rc, nil
}

// rollbackImport tears down a partially-imported quiz after a media-restore
// failure: it deletes the quiz row (the cascade drops its media rows) and
// removes its on-disk media directory so a failed import leaves nothing behind.
// Both steps are best-effort and run under a cancel-immune context so a
// cancelled import still cleans up. A cleanup failure cannot be returned (the
// caller already holds the original restore error), so each is logged here at
// Warn with the quizID - otherwise a failed rollback would leave a partial quiz
// with no log line at all.
func rollbackImport(
	ctx context.Context, logger *slog.Logger, quizStore quiz.Store, mediaSvc MediaImporter, quizID int64,
) {
	cleanup := context.WithoutCancel(ctx)
	// Delete the quiz first so the DB rows are gone even if the file removal
	// fails; the media rows cascade off the quiz row.
	if err := quizStore.DeleteQuiz(cleanup, quizID); err != nil {
		logger.WarnContext(cleanup, "import rollback failed to delete quiz",
			slog.Int64("quiz_id", quizID), slog.Any("err", err))
	}
	if err := mediaSvc.RemoveQuizDir(quizID); err != nil {
		logger.WarnContext(cleanup, "import rollback failed to remove quiz media directory",
			slog.Int64("quiz_id", quizID), slog.Any("err", err))
	}
}

// writeArchiveImportError maps an import failure to the right response. A slug
// collision is a clear 409 with the rename guidance. An archive whose manifest
// references a media file the archive does not contain is a malformed client
// upload (400), not a server fault - the rollback has already removed the
// partial quiz. Everything else is logged and rendered as a 500 (the import
// already rolled back, so nothing partial remains either way).
func writeArchiveImportError(
	w http.ResponseWriter, r *http.Request, logger *slog.Logger,
	renderErr func(http.ResponseWriter, *http.Request, int, string), err error,
) {
	switch {
	case errors.Is(err, quiz.ErrSlugTaken):
		renderErr(
			w, r, http.StatusConflict,
			"a quiz with this title already exists - rename it on the source instance "+
				"or delete the existing one here, then import again",
		)
	case errors.Is(err, ErrArchiveMediaMissing):
		renderErr(
			w, r, http.StatusBadRequest,
			"the archive's manifest references a media file the archive does not contain",
		)
	default:
		logger.ErrorContext(r.Context(), "error importing quiz archive", slog.Any("err", err))
		renderErr(w, r, http.StatusInternalServerError, "the import failed and was rolled back; please try again")
	}
}
