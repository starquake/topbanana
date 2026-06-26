package admin

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/mediahttp"
	"github.com/starquake/topbanana/internal/quiz"
)

// manifestFileName is the archive entry holding the quiz manifest, written by
// the exporter at the archive root.
const manifestFileName = "quiz.json"

// maxArchiveEntries caps how many entries a quiz archive may contain, a
// zip-bomb guard on entry count (the manifest plus one file per unique media
// row; a real quiz library is far below this). An archive over the cap is
// rejected before any entry is read.
const maxArchiveEntries = 1000

// ErrArchiveTooManyEntries is returned when an uploaded archive carries more
// than maxArchiveEntries entries (a zip-bomb guard).
var ErrArchiveTooManyEntries = errors.New("archive contains too many entries")

// ErrArchiveEntryTooLarge is returned when a single archive entry's declared or
// actual uncompressed size exceeds its per-type cap (a zip-bomb guard).
var ErrArchiveEntryTooLarge = errors.New("archive entry exceeds its maximum size")

// ErrArchiveTooLarge is returned when an archive's total uncompressed size
// exceeds the budget (a zip-bomb guard).
var ErrArchiveTooLarge = errors.New("archive total uncompressed size is too large")

// ErrArchiveMissingManifest is returned when the archive has no quiz.json entry.
var ErrArchiveMissingManifest = errors.New("archive is missing quiz.json")

// ErrArchiveUnsupportedVersion is returned when the manifest's formatVersion is
// newer than this build understands.
var ErrArchiveUnsupportedVersion = errors.New("archive format version is newer than supported")

// ErrArchiveMediaMissing is returned when a manifest references a media file the
// archive does not contain.
var ErrArchiveMediaMissing = errors.New("archive references a media file it does not contain")

// MediaImporter is the slice of the media service the archive importer needs:
// restore an image / audio file from bytes (the pipeline validates and
// re-encodes the untrusted bytes - that is the safety boundary), and remove a
// quiz's whole on-disk media directory for the rollback path. Defined
// consumer-side so the admin package depends only on what it calls; the concrete
// *media.Service satisfies it.
type MediaImporter interface {
	StoreImage(ctx context.Context, quizID, createdBy int64, r io.Reader) (*media.Media, error)
	StoreAudio(
		ctx context.Context, quizID, createdBy int64, durationMs int, description, filename string, r io.Reader,
	) (*media.Media, error)
	RemoveQuizDir(quizID int64) error
}

// ArchiveImportLimits bundles the size guards the importer applies to an
// untrusted archive: the per-entry image / audio uncompressed caps (reusing the
// media upload caps) and the total uncompressed budget across all entries.
type ArchiveImportLimits struct {
	imageMaxBytes int64
	audioMaxBytes int64
	totalMaxBytes int64
}

// NewArchiveImportLimits builds the zip-bomb size guards for the archive
// importer from the configured caps (#1113): the per-entry image and audio
// uncompressed caps reuse the media upload caps, and totalMaxBytes bounds the
// summed uncompressed size of every entry. A zero in any field disables that
// guard. Exported so the server wiring can build it from config.
func NewArchiveImportLimits(imageMaxBytes, audioMaxBytes, totalMaxBytes int64) ArchiveImportLimits {
	return ArchiveImportLimits{
		imageMaxBytes: imageMaxBytes,
		audioMaxBytes: audioMaxBytes,
		totalMaxBytes: totalMaxBytes,
	}
}

// HandleQuizImportArchive accepts a multipart .zip quiz archive on POST
// /admin/quizzes/import/archive and restores the quiz plus its images and sounds
// on this instance, the inverse of the export slice (#1113). It reuses the
// paste-import build + persist path (storeQuiz) but is media-aware: the manifest
// carries per-question image/audio references into the archive's media/ files,
// which are re-stored through the media pipeline (which validates and re-encodes
// the untrusted bytes) once the quiz id exists.
//
// The route is host/admin-gated upstream (requireGameHost); the created quiz is
// attributed to the session player so the owner-gated mutating routes match.
// Visibility and mode come from the form: the default option applies the
// manifest's value, an explicit choice overrides it.
//
// Safety: the body is capped by [http.MaxBytesReader] (MEDIA_IMPORT_MAX_BYTES)
// via the multipart middleware; zip-bomb expansion is bounded by a per-entry size
// cap, a total uncompressed cap, and an entry-count cap. ZIP-SLIP is structurally
// avoided - no archive entry path is ever written to disk; only entry bytes are
// read and handed to the media pipeline, which assigns its own confined paths.
//
// Rollback: any failure after the quiz row is created deletes the quiz (the
// cascade drops its media rows) and removes its on-disk media directory, so a
// failed import leaves nothing behind.
func HandleQuizImportArchive(
	logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store, mediaSvc MediaImporter,
	budget *mediahttp.UploadBudgetLimiter, limits ArchiveImportLimits,
) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizimport.gohtml")
	renderErr := func(w http.ResponseWriter, r *http.Request, status int, msg string) {
		renderer.Render(w, r, status, quizImportPageData{
			Title:             "Admin Dashboard - Import Quiz",
			Example:           quizImportExample,
			Error:             msg,
			ModeOptions:       quiz.ModeValues(),
			VisibilityOptions: quiz.VisibilityValues(),
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		player, ok := auth.PlayerFromContext(r.Context())
		if !ok {
			logger.ErrorContext(r.Context(), "archive import reached handler without a player on context")
			renderErr(w, r, http.StatusInternalServerError, "internal server error")

			return
		}

		manifest, archive, ok := readArchiveUpload(w, r, logger, renderErr, limits)
		if !ok {
			return
		}

		visibility, mode, ok := resolveImportOverrides(w, r, manifest, renderErr)
		if !ok {
			return
		}

		built, err := quizFromArchiveManifest(manifest, player.ID, visibility, mode)
		if err != nil {
			renderErr(w, r, http.StatusBadRequest, fmt.Sprintf("invalid archive: %v", err))

			return
		}

		// Run the same form-level validation the paste-import path runs (#1113):
		// the manifest is untrusted, so an out-of-range timeLimitSeconds /
		// boundaryDurationSeconds (which would otherwise hit the DB CHECK and
		// surface as a 500) or a structurally-invalid quiz (empty title/slug,
		// empty description, a question with no options) is rejected as a clear 400
		// before anything is persisted.
		if problems := (&quizForm{quiz: built.quiz}).Valid(r.Context()); len(problems) > 0 {
			renderErr(w, r, http.StatusBadRequest, fmt.Sprintf("the archive is not a valid quiz: %v", problems))

			return
		}

		// Per-host import budget, charged only once the archive is validated and
		// about to be restored: the expensive work (media re-encode + persist) is
		// what the budget protects, and a malformed-archive 400 should not spend
		// the host's rate budget (mirrors the upload route's "a clear denial does
		// not also spend the budget" rule, #988). Over budget is a clear "slow
		// down" rather than a content error.
		if allowed, _ := budget.Charge(player.ID, 1); !allowed {
			renderErr(w, r, http.StatusTooManyRequests, "import rate limit reached, slow down and try again shortly")

			return
		}

		if err = importQuizWithMedia(r.Context(), logger, quizStore, mediaSvc, archive, built, player.ID); err != nil {
			writeArchiveImportError(w, r, logger, renderErr, err)

			return
		}

		http.Redirect(w, r, fmt.Sprintf("/admin/quizzes/%d", built.quiz.ID), http.StatusSeeOther)
	})
}

// readArchiveUpload reads the uploaded archive part, opens it as a zip under the
// zip-bomb guards, and decodes + version-checks its manifest. On any failure it
// renders the error response and returns ok=false.
func readArchiveUpload(
	w http.ResponseWriter, r *http.Request, logger *slog.Logger,
	renderErr func(http.ResponseWriter, *http.Request, int, string), limits ArchiveImportLimits,
) (quizArchiveManifest, *zip.Reader, bool) {
	raw, ok := readArchivePart(w, r, logger, renderErr)
	if !ok {
		return quizArchiveManifest{}, nil, false
	}

	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		renderErr(w, r, http.StatusBadRequest, "the uploaded file is not a valid .zip archive")

		return quizArchiveManifest{}, nil, false
	}
	if err = checkArchiveLimits(zr, limits); err != nil {
		renderErr(w, r, http.StatusBadRequest, archiveLimitMessage(err))

		return quizArchiveManifest{}, nil, false
	}

	manifest, err := decodeArchiveManifest(zr, limits)
	if err != nil {
		// Every manifest-decode failure (missing quiz.json, unsupported version,
		// invalid JSON) is a malformed client upload, so all map to 400.
		renderErr(w, r, http.StatusBadRequest, archiveManifestMessage(err))

		return quizArchiveManifest{}, nil, false
	}

	return manifest, zr, true
}

// resolveImportOverrides reads the form's visibility and mode selectors and maps
// them onto the values to persist: the sentinel "use the archive's value" falls
// back to the manifest, any other value is validated and used. An unrecognised
// explicit value renders a 400 and returns ok=false.
func resolveImportOverrides(
	w http.ResponseWriter, r *http.Request, manifest quizArchiveManifest,
	renderErr func(http.ResponseWriter, *http.Request, int, string),
) (visibility, mode string, ok bool) {
	visibility, ok = resolveVisibilityOverride(r.PostFormValue("visibility"), manifest.Visibility)
	if !ok {
		renderErr(w, r, http.StatusBadRequest, "choose a valid visibility (or use the archive's visibility)")

		return "", "", false
	}
	mode, ok = resolveModeOverride(r.PostFormValue("mode"), manifest.Mode)
	if !ok {
		renderErr(w, r, http.StatusBadRequest, "choose a valid play mode (or use the archive's mode)")

		return "", "", false
	}

	return visibility, mode, true
}

// useArchiveValue is the form selector value meaning "apply the archive's own
// visibility / mode" rather than an explicit override. The default selected
// option on both selectors posts this.
const useArchiveValue = ""

// resolveVisibilityOverride maps the form selector onto the visibility to
// persist. The empty sentinel uses the manifest's value (validated, falling back
// to the store default when absent); any other value must be a recognised
// visibility.
func resolveVisibilityOverride(formValue, manifestValue string) (string, bool) {
	if formValue == useArchiveValue {
		if manifestValue == "" || quiz.IsValidVisibility(manifestValue) {
			return manifestValue, true
		}

		return "", false
	}
	if quiz.IsValidVisibility(formValue) {
		return formValue, true
	}

	return "", false
}

// resolveModeOverride maps the form selector onto the mode to persist. The empty
// sentinel uses the manifest's value (validated, falling back to the store
// default when absent); any other value must be a recognised mode.
func resolveModeOverride(formValue, manifestValue string) (string, bool) {
	if formValue == useArchiveValue {
		if manifestValue == "" || quiz.IsValidMode(manifestValue) {
			return manifestValue, true
		}

		return "", false
	}
	if quiz.IsValidMode(formValue) {
		return formValue, true
	}

	return "", false
}
