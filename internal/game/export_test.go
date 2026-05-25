package game

// Test-only re-exports of internal helpers (#: blackbox-test sweep
// against the "prefer dot-import blackbox tests" rule in the
// backend-dev agent). The wrapped identifiers stay unexported so the
// production surface is unchanged; only the external game_test
// package sees the Export* names.
var (
	ExportClampTappedAt       = clampTappedAt
	ExportResolveAnswerWindow = resolveAnswerWindow
	ExportDefaultExpiration   = defaultExpiration
)
