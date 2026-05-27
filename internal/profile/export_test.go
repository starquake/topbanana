package profile

// ValidatePasswordChangeInput exposes the unexported
// validatePasswordChangeInput helper so the external profile_test
// package can pin the length and confirm-match rules without spinning
// up the full HTTP handler.
var ValidatePasswordChangeInput = validatePasswordChangeInput
