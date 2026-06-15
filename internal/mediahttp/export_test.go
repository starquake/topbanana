package mediahttp

// WantsJSON exposes the unexported Accept-header sniff so the external
// mediahttp_test package can pin the wantsJSON / forces-redirect branch
// without driving the upload handler (#951).
var WantsJSON = wantsJSON

// UploadFailureReason exposes the unexported error-to-message map so the
// external mediahttp_test package can pin one human-readable string per
// pipeline sentinel.
var UploadFailureReason = uploadFailureReason

// BuildUploadQuery exposes the unexported counts-to-query-string builder so
// the external mediahttp_test package can pin the "?uploaded=N&failed=M"
// shape and the empty-string-for-zero-counts branch.
var BuildUploadQuery = buildUploadQuery

// UploadResult is the per-file outcome type [Summarize] consumes.
type UploadResult = uploadResult

// Summarize exposes the unexported per-file-result collapser so the
// external mediahttp_test package can pin the uploaded/failed/firstErr
// projection.
var Summarize = summarize

// WriteUploadJSON exposes the unexported JSON-branch writer so the test can
// pin the "any non-pipeline error -> 500" guard added in #951 without driving
// the full multipart pipeline.
var WriteUploadJSON = writeUploadJSON
