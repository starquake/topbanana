package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// fullSuffix and thumbSuffix name the two files a stored image writes under
// the per-quiz directory. Both are jpeg; the thumb carries a distinct suffix
// so a single media id maps to two predictable filenames.
const (
	fullSuffix  = ".jpg"
	thumbSuffix = "-thumb.jpg"

	// dirPerm and filePerm are the permissions for the per-quiz directories and
	// the written files. The media root itself is created at startup; these
	// cover the per-quiz subdir created lazily on first upload.
	dirPerm  os.FileMode = 0o755
	filePerm os.FileMode = 0o644

	// decimalBase is the base used to render the quiz and media ids into the
	// per-quiz directory and filenames.
	decimalBase = 10
)

// Service processes an upload through the pipeline, persists the resulting webp
// files under a per-quiz directory below root, and records a media row. It is
// the single place that ties the pure pipeline to the filesystem and the store.
type Service struct {
	store  Store
	root   string
	logger *slog.Logger
}

// NewService returns a media Service writing files under root and recording
// rows through store. root is the configured media directory; the caller
// ensures it exists at startup.
func NewService(store Store, root string, logger *slog.Logger) *Service {
	return &Service{store: store, root: root, logger: logger}
}

// Store processes the upload into a normalised webp full image plus thumbnail,
// writes both under <root>/<quizID>/, inserts the media row with the relative
// paths and metadata, and returns the stored Media. createdBy is the uploading
// player.
//
// The row is inserted only after both files are on disk; if the insert fails
// the just-written files are removed so a failed upload leaves no orphans. The
// stored Path / ThumbPath are relative to root so a later root remount does not
// strand the references.
func (s *Service) Store(ctx context.Context, quizID, createdBy int64, r io.Reader) (*Media, error) {
	// Two ctx.Err checks around Process: the first skips Process if the
	// cancel already arrived (Process is the CPU-heavy decode + re-encode);
	// the second catches a cancel that arrived during Process, which is sync
	// and doesn't observe ctx itself.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("upload cancelled before processing: %w", ctxErr)
	}
	processed, err := Process(r)
	if err != nil {
		return nil, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("upload cancelled after processing: %w", ctxErr)
	}

	quizDir := strconv.FormatInt(quizID, decimalBase)
	absDir := filepath.Join(s.root, quizDir)
	if mkErr := os.MkdirAll(absDir, dirPerm); mkErr != nil {
		return nil, fmt.Errorf("creating quiz media directory: %w", mkErr)
	}

	row, err := s.store.CreateMedia(ctx, &Media{
		QuizID:            quizID,
		Type:              TypeImage,
		MIME:              processed.MIME,
		Width:             processed.Width,
		Height:            processed.Height,
		SizeBytes:         int64(processed.SizeBytes),
		SHA256:            processed.SHA256,
		CreatedByPlayerID: createdBy,
		// Path / ThumbPath are filled in below once the id assigns the
		// filenames; UpdateMediaPaths writes them so the names embed the row id.
		Path:      "",
		ThumbPath: "",
	})
	if err != nil {
		return nil, fmt.Errorf("creating media row: %w", err)
	}

	relFull := filepath.Join(quizDir, strconv.FormatInt(row.ID, decimalBase)+fullSuffix)
	relThumb := filepath.Join(quizDir, strconv.FormatInt(row.ID, decimalBase)+thumbSuffix)

	if err = s.writeFiles(processed, relFull, relThumb); err != nil {
		s.cleanupRow(context.WithoutCancel(ctx), row.ID)

		return nil, err
	}

	if err = s.store.UpdateMediaPaths(ctx, row.ID, relFull, relThumb); err != nil {
		// Use a cancel-immune context for cleanup so a cancelled upload's
		// row + files actually get removed instead of orphaning when ctx is
		// the cause of the failure (#951).
		cleanup := context.WithoutCancel(ctx)
		s.removeFile(relFull)
		s.removeFile(relThumb)
		s.cleanupRow(cleanup, row.ID)

		return nil, fmt.Errorf("recording media paths: %w", err)
	}

	row.Path = relFull
	row.ThumbPath = relThumb

	return row, nil
}

// Delete removes the media row, then unlinks its two files best-effort. A
// missing file is not an error: a desync between row and file is reconciled by
// the cleanup tooling, so a half-deleted upload still fully deletes here.
// Returns ErrMediaNotFound when the id does not name a row.
func (s *Service) Delete(ctx context.Context, id int64) error {
	m, err := s.store.GetMedia(ctx, id)
	if err != nil {
		return fmt.Errorf("loading media for delete: %w", err)
	}

	if err = s.store.DeleteMedia(ctx, id); err != nil {
		return fmt.Errorf("deleting media row: %w", err)
	}

	s.removeFile(m.Path)
	if m.ThumbPath != "" {
		s.removeFile(m.ThumbPath)
	}

	return nil
}

// Get returns the media row for id, or ErrMediaNotFound. The serving slice uses
// it to resolve a path and ETag before streaming the file.
func (s *Service) Get(ctx context.Context, id int64) (*Media, error) {
	m, err := s.store.GetMedia(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting media: %w", err)
	}

	return m, nil
}

// ListByQuiz returns every media row for quizID, newest first.
func (s *Service) ListByQuiz(ctx context.Context, quizID int64) ([]*Media, error) {
	items, err := s.store.ListMediaByQuiz(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("listing media by quiz: %w", err)
	}

	return items, nil
}

// Open opens a stored media file for reading by its root-relative path. The
// caller closes the returned file. The path is confined to root: a relPath that
// would escape the media root (a traversal via "..") is rejected with
// ErrPathEscapesRoot rather than opened, so a corrupt or hostile DB value
// cannot read an arbitrary file.
func (s *Service) Open(relPath string) (*os.File, error) {
	resolved, err := s.resolve(relPath)
	if err != nil {
		return nil, err
	}
	// resolve confines the path to root via filepath.Rel, so the variable open
	// cannot reach outside the media directory.
	f, err := os.Open(resolved) //nolint:gosec // path confined to root by resolve
	if err != nil {
		return nil, fmt.Errorf("opening media file: %w", err)
	}

	return f, nil
}

// writeFiles writes the full and thumb jpeg bytes to the given root-relative
// paths. On a failure after the first write succeeds, the first file is removed
// so a partial write leaves nothing behind.
func (s *Service) writeFiles(processed *Processed, relFull, relThumb string) error {
	if err := os.WriteFile(filepath.Join(s.root, relFull), processed.Full, filePerm); err != nil {
		return fmt.Errorf("writing full image: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.root, relThumb), processed.Thumb, filePerm); err != nil {
		s.removeFile(relFull)

		return fmt.Errorf("writing thumbnail: %w", err)
	}

	return nil
}

// cleanupRow deletes a just-created media row after a filesystem failure so a
// failed upload leaves no orphan row. A failure to clean up is logged, not
// returned: the original error is what the caller acts on.
func (s *Service) cleanupRow(ctx context.Context, id int64) {
	if err := s.store.DeleteMedia(ctx, id); err != nil && !errors.Is(err, ErrMediaNotFound) {
		s.logger.ErrorContext(ctx, "failed to clean up media row after write failure",
			slog.Int64("media_id", id), slog.Any("err", err))
	}
}

// resolve joins a root-relative path to root and confirms it stays under root.
// A path that climbs out via ".." yields ErrPathEscapesRoot. Returns the
// absolute on-disk path on success.
func (s *Service) resolve(relPath string) (string, error) {
	joined := filepath.Join(s.root, relPath)
	rel, err := filepath.Rel(s.root, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrPathEscapesRoot, relPath)
	}

	return joined, nil
}

// removeFile unlinks a root-relative path best-effort. A missing file is not an
// error (the cleanup tooling reconciles); any other failure is logged.
func (s *Service) removeFile(relPath string) {
	if relPath == "" {
		return
	}
	if err := os.Remove(filepath.Join(s.root, relPath)); err != nil && !os.IsNotExist(err) {
		s.logger.Error("failed to remove media file",
			slog.String("path", relPath), slog.Any("err", err))
	}
}
