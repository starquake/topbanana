package app

// Re-exports of the package-private ResetPassword sentinel errors so the
// external test package (app_test) can match on them via [errors.Is]
// without resorting to fragile string-substring assertions on err.Error().
// Production callers go through CLI exit codes and messages and do not
// need these in the public API.
var (
	// ErrResetEmailRequired re-exports errResetEmailRequired for tests.
	ErrResetEmailRequired = errResetEmailRequired
	// ErrResetPasswordTooShort re-exports errResetPasswordTooShort for tests.
	ErrResetPasswordTooShort = errResetPasswordTooShort
	// ErrResetPasswordTooLong re-exports errResetPasswordTooLong for tests.
	ErrResetPasswordTooLong = errResetPasswordTooLong
	// ErrResetUserNotFound re-exports errResetUserNotFound for tests.
	ErrResetUserNotFound = errResetUserNotFound
	// ErrResetEmptyInput re-exports errResetEmptyInput for tests.
	ErrResetEmptyInput = errResetEmptyInput
	// ErrResetPasswordsDontMatch re-exports errResetPasswordsDontMatch for tests.
	ErrResetPasswordsDontMatch = errResetPasswordsDontMatch
	// ErrPromoteUsernameRequired re-exports errPromoteUsernameRequired for tests.
	ErrPromoteUsernameRequired = errPromoteUsernameRequired
	// ErrPromoteUserNotFound re-exports errPromoteUserNotFound for tests.
	ErrPromoteUserNotFound = errPromoteUserNotFound
)

// RunTokenSweep exposes the unexported background-sweep loop so the
// external app_test package can pin its tick-and-cancel behaviour
// without standing up the full server (#472).
var RunTokenSweep = runTokenSweep

// BuildMailer exposes the unexported mailer-construction helper so
// the external app_test package can pin the WARN-when-BASE_URL-is-
// missing log behaviour (#495) without standing up the full server.
var BuildMailer = buildMailer

// TokenSweeper / ResetTokenSweeper re-export the narrow interfaces
// the sweep depends on so tests can build stubs against the same
// shape the production wiring uses.
type (
	TokenSweeper      = tokenSweeper
	ResetTokenSweeper = resetTokenSweeper
)
