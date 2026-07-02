package mediahttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/quiz"
)

const (
	// uploadFormField is the multipart field images arrive under.
	uploadFormField = "images"

	// maxUploadFilesPerRequest caps how many files a single upload request may
	// carry. Defense in depth on top of the form's own size + count limits.
	// Sized so a host can submit a folder of thumbnails in one action without
	// pinning the parser on a malicious flood.
	maxUploadFilesPerRequest = 10

	// maxUploadRequestBytes caps the whole multipart request body. It sits
	// above N x media.MaxUploadBytes (the ~10 MB image cap the pipeline
	// enforces on each file part) so the multipart envelope - boundaries, the
	// csrf_token field, headers - fits without tripping the body cap before
	// the pipeline can return the cleaner ErrUploadTooLarge for an oversized
	// image. The pipeline still rejects each image part over MaxUploadBytes,
	// so this is only a coarse outer guard.
	maxUploadRequestBytes = maxUploadFilesPerRequest*media.MaxUploadBytes + multipartEnvelopeHeadroom

	// maxSingleUploadBytes is the largest a single uploaded file can be under the
	// request-body cap: the body is capped at maxUploadRequestBytes and one file
	// must leave room for the multipart envelope, so subtract the headroom. A
	// host-facing per-file cap (e.g. the audio clip cap, whose configured value
	// can exceed this) is clamped to it via ClampSingleUploadBytes so the host is
	// never shown a size the body cap would reject (#1139).
	maxSingleUploadBytes = maxUploadRequestBytes - multipartEnvelopeHeadroom

	// multipartEnvelopeHeadroom is the slack added over the image cap to cover
	// the multipart envelope (boundaries, the csrf_token field, part headers).
	multipartEnvelopeHeadroom = 2 << 20

	// multipartMemoryBytes is how much of the parsed multipart form is buffered
	// in memory before parts spill to temp files. Kept modest so a flood of
	// concurrent uploads does not pin large buffers; each file part is
	// streamed to the pipeline either way.
	multipartMemoryBytes = 1 << 20

	// uploadReadDeadline bounds how long an upload's body may take to arrive
	// over the wire. The server-wide ReadTimeout is 10 s so an ordinary
	// request that hangs gets killed quickly; that limit kills slow uploads
	// of legitimately large images. The middleware bumps the per-connection
	// read deadline to this longer cap before parsing the multipart body so a
	// 10 MB image still lands over a slow phone connection (~50 KB/s).
	uploadReadDeadline = 5 * time.Minute

	// internalErrorMessage is the generic 500 body shared across this package.
	internalErrorMessage = "internal error"

	// retryAfterBase is the numeric base for rendering the Retry-After seconds
	// value (decimal, per RFC 9110).
	retryAfterBase = 10

	// httpStatusClientClosedRequest is the nginx convention for "client
	// closed the connection before the server could write a response" - not
	// in the IANA registry but widely used so cancelled uploads don't paint
	// the access log as 5xx server faults.
	httpStatusClientClosedRequest = 499
)

// ClampSingleUploadBytes bounds a configured per-file size cap to the largest a
// single file can actually be under the multipart request-body cap, so a
// host-facing cap (e.g. an audio clip cap raised above the body cap) is never
// advertised or guarded higher than the server will accept (#1139). A zero
// input (cap disabled) passes through unchanged.
func ClampSingleUploadBytes(n int64) int64 {
	if n > maxSingleUploadBytes {
		return maxSingleUploadBytes
	}

	return n
}

// QuizEditLookup is the slice of the quiz store the upload handler uses to
// enforce the per-quiz edit gate: a host may upload only to a quiz they
// created, an admin to any (mirrors admin.canEditQuiz / requireQuizOwner).
type QuizEditLookup interface {
	// GetQuiz returns a quiz (including CreatedByPlayerID). Returns
	// quiz.ErrQuizNotFound when the quiz does not exist.
	GetQuiz(ctx context.Context, id int64) (*quiz.Quiz, error)
}

// HandleMediaUpload accepts a multipart image upload for POST
// /admin/quizzes/{quizID}/media and stores each file through the media service.
// Multiple files in one submission are allowed: each is processed
// independently and a partial success (some files stored, some skipped because
// the pipeline rejected them) lands the successful files and surfaces the
// counts via the redirect's query string so the quiz view can render a banner.
// Pipeline rejections on a file map to a skip, not a 4xx for the whole
// request, so the rest of the batch still gets through.
//
// The route is host/admin-gated upstream (requireGameHost); this handler adds
// the per-quiz edit gate so a host may upload only to a quiz they created and
// an admin to any (the same creator-or-admin rule the admin question/quiz
// mutations apply via requireQuizOwner). A non-owner host gets a 403; a
// missing quiz a 404. A request with no files at all, or one exceeding the
// per-request count cap, returns 400. A real server failure on store returns
// 500.
//
// Two server-side backstops bound a single host's upload volume (#988), since
// the form JS fires one request per picked file and so cannot be trusted to
// honour the per-request count cap on its own:
//   - quizImageLimit is the per-quiz library ceiling. A batch that would push
//     the quiz over it is rejected 409 BEFORE any budget is charged, so a clear
//     admin denial does not also consume the host's rate budget.
//   - budget is a per-host file allowance over a rolling window, charging the
//     file COUNT per request so many single-file POSTs draw down the same
//     budget one big batch would. Over budget returns 429 with Retry-After.
//
// The order is: per-request count cap (400), then library-size cap (409), then
// budget charge (429), then store - so a 409 never leaves a charge behind.
//
// On success it redirects 303 back to the quiz view's images section so the
// page does not jump to the top.
//
// The caller is expected to front this handler with MaxMultipartFormMiddleware
// so the body is capped and the multipart form is parsed before the CSRF
// middleware validates the token, which for a multipart form lives in the
// parsed PostForm.
func HandleMediaUpload(
	logger *slog.Logger, svc MediaService, quizzes QuizEditLookup,
	budget *UploadBudgetLimiter, quizImageLimit int,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}

		player, ok := auth.PlayerFromContext(r.Context())
		if !ok {
			// The host gate upstream guarantees a player on the context; a
			// missing one is a wiring bug, not a client error.
			logger.ErrorContext(r.Context(), "media upload reached handler without a player on context")
			http.Error(w, internalErrorMessage, http.StatusInternalServerError)

			return
		}

		if !authorizeQuizEdit(w, r, logger, quizzes, quizID, player) {
			return
		}

		files := collectUploadFiles(r)
		if len(files) == 0 {
			http.Error(w, "missing image file", http.StatusBadRequest)

			return
		}
		if len(files) > maxUploadFilesPerRequest {
			http.Error(w, fmt.Sprintf("too many files in one upload (max %d)", maxUploadFilesPerRequest),
				http.StatusBadRequest)

			return
		}

		// Library-size cap before the budget charge: a quiz that has hit its
		// media ceiling is a clear admin denial (409), not abuse, so it must
		// not draw down the host's rate budget. A zero limit disables the cap.
		if !checkQuizMediaLimit(w, r, logger, svc, quizID, len(files), quizImageLimit, media.TypeImage) {
			return
		}

		// Per-host file budget over a rolling window: charges len(files) so the
		// one-request-per-file form JS cannot bypass the per-request count cap.
		if allowed, retryAfter := budget.Charge(player.ID, len(files)); !allowed {
			writeRateLimited(w, retryAfter)

			return
		}

		results := storeUploadFiles(r.Context(), logger, svc, quizID, player.ID, files)

		if wantsJSON(r) {
			writeUploadJSON(w, r, logger, results)

			return
		}

		uploaded, failed, firstErr := summarize(results)
		// Single-file form upload that failed: surface the pipeline 4xx
		// directly so the host sees a precise reason instead of bouncing
		// through a banner. For multi-file batches the banner carries the
		// per-file split, so an error page that hides the rest of the
		// library on an all-fail batch is the wrong UX.
		if uploaded == 0 && len(files) == 1 {
			writeUploadError(w, r, logger, firstErr)

			return
		}

		dest := fmt.Sprintf("/admin/quizzes/%d", quizID) + buildUploadQuery(uploaded, failed, 0) + "#images"
		http.Redirect(w, r, dest, http.StatusSeeOther) //nolint:gosec // dest is built from server-side ids and counts.
	})
}

// checkQuizMediaLimit reports whether storing incoming more files keeps the
// quiz at or under the per-quiz library ceiling for one media type, where
// incoming is the number of files in this request. The ceiling is per-type: an
// image upload counts only images and an audio upload counts only audio, so the
// two kinds never draw down each other's cap (#1059). mediaType is both the type
// counted and the noun in the host-facing 409 message ("image" or "audio"). A
// limit of zero disables the cap. On an over-limit batch it writes a 409 and
// returns false; a real store error is logged and surfaced as 500. Runs before
// the budget charge so a 409 never leaves a charge behind (#988).
func checkQuizMediaLimit(
	w http.ResponseWriter, r *http.Request, logger *slog.Logger,
	svc MediaService, quizID int64, incoming, limit int, mediaType string,
) bool {
	if limit <= 0 {
		return true
	}
	existing, err := svc.CountByQuizAndType(r.Context(), quizID, mediaType)
	if err != nil {
		logger.ErrorContext(r.Context(), "error counting quiz media for library cap", slog.Any("err", err))
		http.Error(w, internalErrorMessage, http.StatusInternalServerError)

		return false
	}
	if existing+int64(incoming) > int64(limit) {
		http.Error(
			w,
			fmt.Sprintf("this quiz has reached its %s limit (max %d)", mediaType, limit),
			http.StatusConflict,
		)

		return false
	}

	return true
}

// writeRateLimited writes a 429 with a Retry-After header (whole seconds,
// rounded up, minimum 1) and a short plain-text body. Plain text on every path
// including the JSON upload: the form JS reads the body as text on a non-2xx,
// so the uploadResponseJSON shape is not emitted for a 429 (#988).
func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := max(int64(math.Ceil(retryAfter.Seconds())), 1)
	w.Header().Set("Retry-After", strconv.FormatInt(seconds, retryAfterBase))
	http.Error(w, "upload rate limit reached, slow down", http.StatusTooManyRequests)
}

// collectUploadFiles returns every file part on the request under the
// "images" field, preserving submission order.
func collectUploadFiles(r *http.Request) []*multipart.FileHeader {
	if r.MultipartForm == nil {
		return nil
	}

	return r.MultipartForm.File[uploadFormField]
}

// uploadResult is one file's outcome from a batch upload. Exactly one of
// MediaID or Err is set; Filename is the client-supplied name and is included
// on both branches for the JSON response.
type uploadResult struct {
	Filename string
	MediaID  int64
	Err      error
}

// storeUploadFiles streams each file through media.Service.Store and returns
// a per-file outcome list. A skip (pipeline caller-fault) leaves the file's
// Err set and still lets the rest of the batch run. A [context.Canceled]
// error means the client closed the connection mid-flight (xhr.abort) and is
// not noise; everything else that is not a pipeline rejection is logged.
//
// Fail-fast on persistent server faults (disk full, db down): once a
// non-pipeline non-cancel error fires once, every remaining file in the batch
// would re-trip the same fault, multiplying log lines and cleanup work.
func storeUploadFiles(
	ctx context.Context, logger *slog.Logger, svc MediaService,
	quizID, playerID int64, files []*multipart.FileHeader,
) []uploadResult {
	results := make([]uploadResult, 0, len(files))
	for _, header := range files {
		mediaID, err := storeOneUpload(ctx, svc, quizID, playerID, header)
		isServerFault := err != nil && !isPipelineRejection(err) && !errors.Is(err, context.Canceled)
		if isServerFault {
			logger.ErrorContext(ctx, "error storing uploaded media",
				slog.String("filename", header.Filename), slog.Any("err", err))
		}
		results = append(results, uploadResult{Filename: header.Filename, MediaID: mediaID, Err: err})
		if isServerFault {
			break
		}
	}

	return results
}

// summarize collapses a per-file result list into success/skip counts and the
// first failure error (used to surface a single-file failure directly to the
// host instead of bouncing through a banner).
func summarize(results []uploadResult) (uploaded, failed int, firstErr error) {
	for _, r := range results {
		if r.Err != nil {
			failed++
			if firstErr == nil {
				firstErr = r.Err
			}

			continue
		}
		uploaded++
	}

	return uploaded, failed, firstErr
}

// writeUploadError maps a media.Service.Store error to an HTTP response: the
// pipeline's caller-fault sentinels become 400 with a short message, an
// already-cancelled context surfaces as 499 with no log (the client closed the
// connection, so nothing we write is delivered), anything else is logged and
// returned as 500. Used when a single-file upload failed - so the host sees a
// precise reason instead of bouncing through a banner.
func writeUploadError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, media.ErrUploadTooLarge):
		http.Error(w, "image exceeds the maximum upload size", http.StatusBadRequest)
	case errors.Is(err, media.ErrImageTooLarge):
		http.Error(w, "image dimensions exceed the maximum", http.StatusBadRequest)
	case errors.Is(err, media.ErrEmptyUpload):
		http.Error(w, "image file is empty", http.StatusBadRequest)
	case errors.Is(err, media.ErrUnsupportedImage):
		http.Error(w, "unsupported image format (use jpg or png)", http.StatusBadRequest)
	case errors.Is(err, context.Canceled):
		// nginx-style "client closed request"; the response will not be
		// delivered (TCP is already closed), but the status stamp keeps
		// access-log accounting honest without painting it as a 500.
		w.WriteHeader(httpStatusClientClosedRequest)
	default:
		logger.ErrorContext(r.Context(), "error storing uploaded media", slog.Any("err", err))
		http.Error(w, internalErrorMessage, http.StatusInternalServerError)
	}
}

// storeOneUpload stores one file and returns its media id. The ctx.Err()
// check is the only abort point: media.Service.Store is not context-aware.
func storeOneUpload(
	ctx context.Context, svc MediaService, quizID, playerID int64, header *multipart.FileHeader,
) (mediaID int64, err error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, fmt.Errorf("upload cancelled before store of %q: %w", header.Filename, ctxErr)
	}
	file, err := header.Open()
	if err != nil {
		return 0, fmt.Errorf("opening upload part %q: %w", header.Filename, err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("closing upload part %q: %w", header.Filename, cerr)
		}
	}()

	stored, err := svc.StoreImage(ctx, quizID, playerID, file)
	if err != nil {
		return 0, fmt.Errorf("storing upload part %q: %w", header.Filename, err)
	}

	return stored.ID, nil
}

// wantsJSON reports whether the client signalled it would rather have the
// upload result as JSON than via the form-redirect. The progressive-enhancement
// JS on the quiz view sets Accept: application/json so each per-file upload
// can render its own progress row instead of bouncing through a redirect; a
// plain browser form submit has no JSON in its Accept header and still gets
// the 303.
func wantsJSON(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	if accept == "" {
		return false
	}
	for part := range strings.SplitSeq(accept, ",") {
		mime, _, _ := strings.Cut(part, ";")
		if strings.EqualFold(strings.TrimSpace(mime), "application/json") {
			return true
		}
	}

	return false
}

// uploadResultJSON is the per-file shape on the wire. Filename echoes back
// what the client sent so a progress row can be matched up by name even when
// the responses arrive out of order. ID and Reason are mutually exclusive: ID
// is set on success (the new media row's id, also the URL suffix), Reason on
// a pipeline rejection.
type uploadResultJSON struct {
	Filename string `json:"filename"`
	ID       int64  `json:"id,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// uploadResponseJSON is the wire shape of a successful upload submission. Both
// arrays may be empty when nothing was sent (the handler rejects that earlier,
// so in practice at least one of the two carries an entry).
type uploadResponseJSON struct {
	Uploaded []uploadResultJSON `json:"uploaded"`
	Failed   []uploadResultJSON `json:"failed"`
}

// writeUploadJSON emits per-file outcomes as JSON. Server-fault errors land in
// failed[] alongside pipeline rejections (uploadFailureReason maps unknown
// errors to "upload failed") so a client gets the per-file split even when
// some files hit a transient backend issue - escalating the whole batch to a
// 500 would hide files that already stored cleanly from a retrying client and
// produce duplicate uploads. The handler-side log already pins the detail of
// the server fault for operators. Encoding to a buffer keeps an encoder
// failure from committing a truncated 200.
func writeUploadJSON(w http.ResponseWriter, r *http.Request, logger *slog.Logger, results []uploadResult) {
	resp := uploadResponseJSON{
		Uploaded: make([]uploadResultJSON, 0, len(results)),
		Failed:   make([]uploadResultJSON, 0, len(results)),
	}
	for _, res := range results {
		if res.Err != nil {
			resp.Failed = append(resp.Failed, uploadResultJSON{
				Filename: res.Filename,
				Reason:   uploadFailureReason(res.Err),
			})

			continue
		}
		resp.Uploaded = append(resp.Uploaded, uploadResultJSON{
			Filename: res.Filename,
			ID:       res.MediaID,
		})
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(resp); err != nil {
		logger.ErrorContext(r.Context(), "error encoding upload response", slog.Any("err", err))
		http.Error(w, internalErrorMessage, http.StatusInternalServerError)

		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if _, err := w.Write(buf.Bytes()); err != nil {
		logger.ErrorContext(r.Context(), "error writing upload response", slog.Any("err", err))
	}
}

// uploadFailureReason maps a per-file pipeline error to the short, host-facing
// label that the JSON response carries. Anything that is not a known pipeline
// rejection is reported as a generic upload error; the handler logs the
// detail.
func uploadFailureReason(err error) string {
	switch {
	case errors.Is(err, media.ErrUploadTooLarge):
		return "file exceeds the maximum upload size"
	case errors.Is(err, media.ErrImageTooLarge):
		return "image dimensions exceed the maximum"
	case errors.Is(err, media.ErrEmptyUpload):
		return "file is empty"
	case errors.Is(err, media.ErrUnsupportedImage):
		return "unsupported image format (use jpg or png)"
	default:
		return "upload failed"
	}
}

// buildUploadQuery returns the query suffix that tells the quiz view how to
// render its post-upload banner. Empty when every count is zero (a degenerate
// case the handler treats as no banner needed).
func buildUploadQuery(uploaded, failed, cancelled int) string {
	if uploaded == 0 && failed == 0 && cancelled == 0 {
		return ""
	}

	return "?uploaded=" + strconv.Itoa(uploaded) +
		"&failed=" + strconv.Itoa(failed) +
		"&cancelled=" + strconv.Itoa(cancelled)
}

// isPipelineRejection is true for the media-pipeline caller-fault sentinels:
// these are skips a host can recover from by adjusting their input (smaller
// file, supported format). Everything else is a server fault worth logging.
func isPipelineRejection(err error) bool {
	return errors.Is(err, media.ErrUploadTooLarge) ||
		errors.Is(err, media.ErrImageTooLarge) ||
		errors.Is(err, media.ErrEmptyUpload) ||
		errors.Is(err, media.ErrUnsupportedImage)
}

// authorizeQuizEdit loads the quiz and gates the request on the creator-or-admin
// edit rule (mirrors admin.canEditQuiz): the player must be the quiz's creator
// or an admin. A missing quiz yields 404, a non-owner non-admin a 403. Returns
// whether to proceed.
func authorizeQuizEdit(
	w http.ResponseWriter, r *http.Request,
	logger *slog.Logger, quizzes QuizEditLookup, quizID int64, player *auth.Player,
) bool {
	qz, err := quizzes.GetQuiz(r.Context(), quizID)
	if err != nil {
		if errors.Is(err, quiz.ErrQuizNotFound) {
			http.NotFound(w, r)

			return false
		}
		logger.ErrorContext(r.Context(), "error loading quiz for media upload gate", slog.Any("err", err))
		http.Error(w, internalErrorMessage, http.StatusInternalServerError)

		return false
	}

	if !player.IsAdmin() && player.ID != qz.CreatedByPlayerID {
		http.Error(w, "you do not have permission to upload media to this quiz", http.StatusForbidden)

		return false
	}

	return true
}

// MaxMultipartFormMiddleware caps the request body at maxUploadRequestBytes and
// parses the multipart form before the next handler runs. It is mounted in
// front of the CSRF middleware on the upload route: the CSRF validator reads the
// token from PostForm, which for a multipart submission is only populated once
// the multipart body has been parsed. Parsing here (with the large cap) makes
// the token visible to the CSRF layer and leaves the parsed file parts ready
// for the handler's FormFile calls. A body over the cap or a malformed form
// yields 400 before the token check, so an oversized upload cannot slip past
// as a CSRF failure.
func MaxMultipartFormMiddleware(next http.Handler) http.Handler {
	return MaxMultipartFormMiddlewareWithLimit(maxUploadRequestBytes, next)
}

// MaxMultipartFormMiddlewareWithLimit is MaxMultipartFormMiddleware with a
// caller-supplied body cap. The quiz-archive import route mounts it with
// MEDIA_IMPORT_MAX_BYTES so a much larger (whole-library) upload is bounded by
// its own knob rather than the image-upload cap (#1113); a maxBytes of zero or
// less means "no cap" (rely on the server-wide limits). It otherwise behaves
// identically: it bumps the read deadline, parses the multipart form so the CSRF
// token is visible, and cleans up any spilled temp files.
func MaxMultipartFormMiddlewareWithLimit(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if maxBytes > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		// The server-wide ReadTimeout is short so an ordinary slow handler
		// gets killed quickly. Uploads can legitimately stream for minutes on
		// a slow phone connection though, so bump the per-connection read
		// deadline here. If the underlying ResponseWriter does not support a
		// deadline (unlikely under net/http) the call is a silent no-op and
		// the request still uses the global ReadTimeout.
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(uploadReadDeadline))
		// MaxBytesReader above bounds the body, so the parse cannot exhaust
		// memory despite gosec's G120 flag on the bare ParseMultipartForm call.
		if err := r.ParseMultipartForm(multipartMemoryBytes); err != nil { //nolint:gosec // body capped above
			http.Error(w, "invalid or oversized upload", http.StatusBadRequest)

			return
		}
		// Parts larger than multipartMemoryBytes spill to temp files that
		// net/http never removes on its own; clean them up once the handler is
		// done reading the upload (best-effort).
		defer func() {
			if r.MultipartForm != nil {
				_ = r.MultipartForm.RemoveAll()
			}
		}()
		next.ServeHTTP(w, r)
	})
}
