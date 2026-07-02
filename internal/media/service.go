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
	"time"
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

	// StaleNotReadyThreshold is how long a not-ready media row may linger before
	// the sweep removes it and its files (#992). A row is not-ready only between
	// CreateMedia and the final MarkMediaReady, a sub-second window for a
	// completing upload; anything still not-ready past this threshold is a row
	// whose request was cancelled mid-upload. Generous enough that a slow but
	// live upload (large image, slow disk) is never swept out from under itself.
	StaleNotReadyThreshold = 5 * time.Minute
)

// Service processes an upload through the pipeline, persists the resulting jpeg
// files under a per-quiz directory below root, and records a media row. It is
// the single place that ties the pure pipeline to the filesystem and the store.
type Service struct {
	store         Store
	root          string
	imageMaxBytes int64
	audioMaxBytes int64
	logger        *slog.Logger
}

// NewService returns a media Service writing files under root and recording
// rows through store. root is the configured media directory; the caller
// ensures it exists at startup. imageMaxBytes caps a stored image upload's raw
// size and audioMaxBytes caps a stored audio upload's raw size; zero or negative
// means no cap.
func NewService(store Store, root string, imageMaxBytes, audioMaxBytes int64, logger *slog.Logger) *Service {
	return &Service{
		store:         store,
		root:          root,
		imageMaxBytes: imageMaxBytes,
		audioMaxBytes: audioMaxBytes,
		logger:        logger,
	}
}

// StoreImage processes the upload into a normalised jpeg full image plus
// thumbnail, writes both under <root>/<quizID>/, inserts the media row with the
// relative paths and metadata, and returns the stored Media. createdBy is the
// uploading player; filename is the client-supplied upload name, stored (base
// name, length-capped) as the row's OriginalFilename so the library can show it
// as a tooltip (#1137).
//
// The row is inserted not-ready, then the files are written, the paths
// recorded, and only then the row is flipped ready (a two-phase commit, #992).
// Until the final flip the library hides the row, so a cancel that arrives
// after the paths commit but before the flip leaves a hidden row the sweep
// later drops rather than a file visible in the host's library. A failure at
// any step removes the just-written files and the row so a failed upload leaves
// no orphans. The stored Path / ThumbPath are relative to root so a later root
// remount does not strand the references.
func (s *Service) StoreImage(
	ctx context.Context, quizID, createdBy int64, filename string, r io.Reader,
) (*Media, error) {
	// Two ctx.Err checks around Process: the first skips Process if the
	// cancel already arrived (Process is the CPU-heavy decode + re-encode);
	// the second catches a cancel that arrived during Process, which is sync
	// and doesn't observe ctx itself.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("upload cancelled before processing: %w", ctxErr)
	}
	processed, err := Process(r, s.imageMaxBytes)
	if err != nil {
		return nil, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("upload cancelled after processing: %w", ctxErr)
	}

	quizDir := strconv.FormatInt(quizID, decimalBase)

	// Path / ThumbPath are left empty here and filled in by UpdateMediaPaths
	// below, once the assigned id names the on-disk files (<quizID>/<id>.jpg).
	row, err := s.store.CreateMedia(ctx, &Media{
		QuizID:            quizID,
		Type:              TypeImage,
		MIME:              processed.MIME,
		Width:             processed.Width,
		Height:            processed.Height,
		SizeBytes:         int64(processed.SizeBytes),
		SHA256:            processed.SHA256,
		OriginalFilename:  sanitizeFilename(filename),
		CreatedByPlayerID: createdBy,
	})
	if err != nil {
		return nil, fmt.Errorf("creating media row: %w", err)
	}

	relFull := filepath.Join(quizDir, strconv.FormatInt(row.ID, decimalBase)+fullSuffix)
	relThumb := filepath.Join(quizDir, strconv.FormatInt(row.ID, decimalBase)+thumbSuffix)
	if err = s.writeAndCommit(ctx, row.ID, quizDir, []fileBlob{
		{relPath: relFull, data: processed.Full},
		{relPath: relThumb, data: processed.Thumb},
	}); err != nil {
		return nil, err
	}

	row.Path = relFull
	row.ThumbPath = relThumb

	return row, nil
}

// maxOriginalFilenameLen bounds a stored original filename so a crafted upload
// cannot persist an unbounded string. Measured in runes so a multi-byte name is
// not cut mid-character. The library truncates visually anyway; this is the
// storage guard.
const maxOriginalFilenameLen = 255

// sanitizeFilename reduces a client-supplied upload filename to the value stored
// as a media row's OriginalFilename (#1137): its base name (stripping any
// directory components a crafted client may prepend), length-capped. A name that
// is empty or resolves to no real base ("." or a bare separator) yields the empty
// string so an unnamed upload stores no tooltip rather than a stray ".".
func sanitizeFilename(filename string) string {
	base := filepath.Base(strings.TrimSpace(filename))
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	runes := []rune(base)
	if len(runes) > maxOriginalFilenameLen {
		base = strings.TrimSpace(string(runes[:maxOriginalFilenameLen]))
	}

	return base
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

// RemoveQuizDir removes a quiz's entire on-disk media directory
// (<root>/<quizID>/) best-effort. It is the filesystem half of dropping a
// quiz's library: the quiz-delete cascade removes the media rows, but nothing
// unlinks their files, so a failed import's rollback calls this to leave no
// orphaned files behind (#1113). A missing directory is not an error. The path
// is the same id-keyed subdir StoreImage / StoreAudio write into, joined to the
// confined root, so a caller cannot reach outside the media tree.
func (s *Service) RemoveQuizDir(quizID int64) error {
	dir := filepath.Join(s.root, strconv.FormatInt(quizID, decimalBase))
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("removing quiz media directory: %w", err)
	}

	return nil
}

// SweepStaleNotReady removes media rows still not-ready past
// StaleNotReadyThreshold and unlinks their files (#992). A row stays not-ready
// only between CreateMedia and the final MarkMediaReady; one lingering past the
// threshold is the residue of an upload whose request was cancelled mid-flight,
// committed but hidden from the library. Each file unlink and row delete is
// best-effort and logged on failure so one stuck row does not abort the rest of
// the pass; the row is retried on the next sweep. Returns the number of rows
// deleted.
func (s *Service) SweepStaleNotReady(ctx context.Context) (int, error) {
	stale, err := s.store.ListStaleNotReadyMedia(ctx, StaleNotReadyThreshold)
	if err != nil {
		return 0, fmt.Errorf("listing stale not-ready media: %w", err)
	}

	deleted := 0
	for _, m := range stale {
		s.removeFile(m.Path)
		if m.ThumbPath != "" {
			s.removeFile(m.ThumbPath)
		}
		switch derr := s.store.DeleteMedia(ctx, m.ID); {
		case derr == nil:
			deleted++
		case errors.Is(derr, ErrMediaNotFound):
			// Already gone (a concurrent cancel cleanup or a prior pass): the
			// row is dropped either way, so this is not an error, but it is not
			// a deletion this pass made.
		default:
			s.logger.ErrorContext(ctx, "failed to delete stale not-ready media row",
				slog.Int64("media_id", m.ID), slog.Any("err", derr))
		}
	}

	return deleted, nil
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

// UpdateDescription sets the host-supplied description label of a media row,
// normalized (trimmed and length-capped) the same way an upload default is
// (#1072). Returns ErrMediaNotFound when the id does not name a row.
func (s *Service) UpdateDescription(ctx context.Context, id int64, description string) error {
	if err := s.store.UpdateMediaDescription(ctx, id, normalizeDescription(description)); err != nil {
		return fmt.Errorf("updating media description: %w", err)
	}

	return nil
}

// ListByQuiz returns every media row for quizID, newest first.
func (s *Service) ListByQuiz(ctx context.Context, quizID int64) ([]*Media, error) {
	items, err := s.store.ListMediaByQuiz(ctx, quizID)
	if err != nil {
		return nil, fmt.Errorf("listing media by quiz: %w", err)
	}

	return items, nil
}

// CountByQuizAndType returns how many ready media rows of mediaType a quiz has.
// Each upload route uses it to enforce a per-type library ceiling before storing
// a new batch: an image upload counts only images and an audio upload counts only
// audio, so the two kinds never draw down each other's cap (#988, #1059).
func (s *Service) CountByQuizAndType(ctx context.Context, quizID int64, mediaType string) (int64, error) {
	n, err := s.store.CountMediaByQuizAndType(ctx, quizID, mediaType)
	if err != nil {
		return 0, fmt.Errorf("counting media by quiz and type: %w", err)
	}

	return n, nil
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

// fileBlob is one root-relative file the two-phase commit writes: an image
// passes a full + thumb pair, audio passes a single file.
type fileBlob struct {
	relPath string
	data    []byte
}

// writeAndCommit creates the per-quiz directory, writes every file in blobs,
// records the resulting paths on the row, and flips the row ready -- the
// two-phase commit's file + DB work after the not-ready insert. blobs[0] is the
// primary file (recorded as Path); blobs[1], when present, is the thumbnail
// (recorded as ThumbPath). A failure at any step tears the just-written files
// and the still-hidden row back down so a failed upload leaves nothing behind
// (#992, #998).
func (s *Service) writeAndCommit(ctx context.Context, id int64, quizDir string, blobs []fileBlob) error {
	// Create the per-quiz directory only after the insert succeeds, so a quiz
	// whose first upload fails at the insert is not left with a stray empty dir
	// (#998).
	if mkErr := os.MkdirAll(filepath.Join(s.root, quizDir), dirPerm); mkErr != nil {
		s.cleanupRow(context.WithoutCancel(ctx), id)

		return fmt.Errorf("creating quiz media directory: %w", mkErr)
	}

	if err := s.writeFiles(blobs); err != nil {
		s.cleanupRow(context.WithoutCancel(ctx), id)

		return err
	}

	relPath := blobs[0].relPath
	relThumb := ""
	if len(blobs) > 1 {
		relThumb = blobs[1].relPath
	}

	// Cleanup on the post-write failures runs under a cancel-immune context so a
	// cancelled upload's row + files actually get removed instead of orphaning
	// when ctx is the cause of the failure (#951).
	if err := s.store.UpdateMediaPaths(ctx, id, relPath, relThumb); err != nil {
		s.cleanupRowAndFiles(ctx, id, blobs)

		return fmt.Errorf("recording media paths: %w", err)
	}

	// Flip the row ready last, so a cancel arriving after the paths commit but
	// before this flip leaves a hidden (not-ready) row the sweep drops rather
	// than a file the host sees in their library (#992).
	if err := s.store.MarkMediaReady(ctx, id); err != nil {
		s.cleanupRowAndFiles(ctx, id, blobs)

		return fmt.Errorf("marking media ready: %w", err)
	}

	return nil
}

// writeFiles writes every blob to its root-relative path using a
// temp-then-rename pattern so a SIGKILL or crash mid-write can't leave a row
// pointing at a half-written or missing file. Each blob is written under
// <name>.tmp, then atomically renamed into place. A failure at any step removes
// every file written so far so the upload's invariant ("all files exist or
// none do") holds and a failed upload leaves nothing behind.
func (s *Service) writeFiles(blobs []fileBlob) error {
	written := make([]string, 0, len(blobs))

	for _, b := range blobs {
		tmp := b.relPath + ".tmp"
		if err := os.WriteFile(filepath.Join(s.root, tmp), b.data, filePerm); err != nil {
			s.removeFiles(written)
			s.removeFile(tmp)

			return fmt.Errorf("writing media file %q: %w", b.relPath, err)
		}
		if err := os.Rename(filepath.Join(s.root, tmp), filepath.Join(s.root, b.relPath)); err != nil {
			s.removeFiles(written)
			s.removeFile(tmp)

			return fmt.Errorf("publishing media file %q: %w", b.relPath, err)
		}
		written = append(written, b.relPath)
	}

	return nil
}

// removeFiles unlinks every root-relative path best-effort.
func (s *Service) removeFiles(relPaths []string) {
	for _, p := range relPaths {
		s.removeFile(p)
	}
}

// cleanupRowAndFiles removes every written file and the media row after a
// post-write failure, under a cancel-immune context derived from ctx so a
// cancelled upload (the common cause of the failure) still gets fully torn down
// rather than orphaning a row or files (#951).
func (s *Service) cleanupRowAndFiles(ctx context.Context, id int64, blobs []fileBlob) {
	cleanup := context.WithoutCancel(ctx)
	for _, b := range blobs {
		s.removeFile(b.relPath)
	}
	s.cleanupRow(cleanup, id)
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
