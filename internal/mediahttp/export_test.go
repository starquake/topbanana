package mediahttp

// WantsJSON exposes the unexported Accept-header sniff for tests.
var WantsJSON = wantsJSON

// UploadFailureReason exposes the unexported error-to-message map for tests.
var UploadFailureReason = uploadFailureReason

// BuildUploadQuery exposes the unexported query-string builder for tests.
var BuildUploadQuery = buildUploadQuery

// UploadResult is the per-file outcome type [Summarize] consumes.
type UploadResult = uploadResult

// Summarize exposes the unexported per-file-result collapser for tests.
var Summarize = summarize

// WriteUploadJSON exposes the unexported JSON writer for tests.
var WriteUploadJSON = writeUploadJSON

// NewUploadBudgetLimiterWithClock exposes the internal clock-injected upload
// budget limiter constructor so the external mediahttp_test package can pin the
// rolling-window math without sleeping.
var NewUploadBudgetLimiterWithClock = newUploadBudgetLimiterWithClock

// UploadBudgetLimiterEntryCount returns how many players the limiter is
// tracking right now. Lets the unit test pin the prune-stale-entries behaviour
// without exporting the internal map.
func UploadBudgetLimiterEntryCount(l *UploadBudgetLimiter) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.charges)
}
