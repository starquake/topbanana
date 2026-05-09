package app

// Re-exports of the package-private ResetPassword sentinel errors so the
// external test package (app_test) can match on them via [errors.Is]
// without resorting to fragile string-substring assertions on err.Error().
// Production callers go through CLI exit codes and messages and do not
// need these in the public API.
var (
	// ErrResetUsernameRequired re-exports errResetUsernameRequired for tests.
	ErrResetUsernameRequired = errResetUsernameRequired
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
)
