package profile

// ValidatePasswordChangeInput exposes the unexported
// validatePasswordChangeInput helper so the external profile_test
// package can pin the length and confirm-match rules without spinning
// up the full HTTP handler.
var ValidatePasswordChangeInput = validatePasswordChangeInput

// ExportValidateEmailChange exposes validateEmailChange so the
// external test package can pin its rule table from input strings
// without staging an HTTP request.
var ExportValidateEmailChange = validateEmailChange

// AdminNextPath exposes adminNextPath so the external test package can
// pin the return-link allowlist (admin paths only, no open redirects).
var AdminNextPath = adminNextPath
