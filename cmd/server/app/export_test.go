package app

// Re-exports of the package-private ResetPassword sentinel errors so the
// external test package (app_test) can match on them via [errors.Is]
// without resorting to fragile string-substring assertions on err.Error().
// Production callers go through CLI exit codes and messages and do not
// need these in the public API.
var (
	// ErrResetEmailRequired re-exports errResetEmailRequired for tests.
	ErrResetEmailRequired = errResetEmailRequired
	// ErrPasswordTooShort re-exports errPasswordTooShort for tests.
	ErrPasswordTooShort = errPasswordTooShort
	// ErrPasswordTooLong re-exports errPasswordTooLong for tests.
	ErrPasswordTooLong = errPasswordTooLong
	// ErrResetUserNotFound re-exports errResetUserNotFound for tests.
	ErrResetUserNotFound = errResetUserNotFound
	// ErrEmptyInput re-exports errEmptyInput for tests.
	ErrEmptyInput = errEmptyInput
	// ErrPasswordsDontMatch re-exports errPasswordsDontMatch for tests.
	ErrPasswordsDontMatch = errPasswordsDontMatch
	// ErrPromoteEmailRequired re-exports errPromoteEmailRequired for tests.
	ErrPromoteEmailRequired = errPromoteEmailRequired
	// ErrPromoteEmailNotFound re-exports errPromoteEmailNotFound for tests.
	ErrPromoteEmailNotFound = errPromoteEmailNotFound
	// ErrVerifyEmailRequired re-exports errVerifyEmailRequired for tests.
	ErrVerifyEmailRequired = errVerifyEmailRequired
	// ErrVerifyEmailNotFound re-exports errVerifyEmailNotFound for tests.
	ErrVerifyEmailNotFound = errVerifyEmailNotFound
	// ErrCreateAdminEmailRequired re-exports errCreateAdminEmailRequired for tests.
	ErrCreateAdminEmailRequired = errCreateAdminEmailRequired
	// ErrCreateAdminEmailExists re-exports errCreateAdminEmailExists for tests.
	ErrCreateAdminEmailExists = errCreateAdminEmailExists
	// ErrCreateAdminInvalidEmail re-exports errCreateAdminInvalidEmail for tests.
	ErrCreateAdminInvalidEmail = errCreateAdminInvalidEmail
	// ErrInitialAdminInvalidEmail re-exports errInitialAdminInvalidEmail for tests.
	ErrInitialAdminInvalidEmail = errInitialAdminInvalidEmail
	// ErrInitialAdminPasswordTooShort re-exports errInitialAdminPasswordTooShort for tests.
	ErrInitialAdminPasswordTooShort = errInitialAdminPasswordTooShort
	// ErrInitialAdminPasswordTooLong re-exports errInitialAdminPasswordTooLong for tests.
	ErrInitialAdminPasswordTooLong = errInitialAdminPasswordTooLong
	// ErrSeedDemoDisabled re-exports errSeedDemoDisabled for tests.
	ErrSeedDemoDisabled = errSeedDemoDisabled
	// ErrSeedDemoArchiveNotSet re-exports errSeedDemoArchiveNotSet for tests.
	ErrSeedDemoArchiveNotSet = errSeedDemoArchiveNotSet
	// ErrEmptyMediaDir re-exports errEmptyMediaDir for tests.
	ErrEmptyMediaDir = errEmptyMediaDir
)

// BootstrapInitialAdmin exposes the unexported first-boot admin bootstrap so
// the external app_test package can pin its create / skip / validate behaviour
// (#1206) without standing up the full server.
var BootstrapInitialAdmin = bootstrapInitialAdmin

// MkMediaDir exposes the pure media-directory creation helper so the external
// app_test package can pin that it creates the directory and rejects an empty
// path (#936) without standing up the full server or a logger.
var MkMediaDir = mkMediaDir

// RunTokenSweep exposes the unexported background-sweep loop so the
// external app_test package can pin its tick-and-cancel behaviour
// without standing up the full server (#472).
var RunTokenSweep = runTokenSweep

// RunRetentionSweep exposes the unexported data-retention sweep helper so
// the external app_test package can pin its warn-and-continue behaviour
// without standing up the full server (#626).
var RunRetentionSweep = runRetentionSweep

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

// RunHTTPServer exposes the unexported serve+graceful-shutdown loop so the
// external app_test package can pin that shutdown drains the detached
// email-dispatch tracker before returning (and thus before Run closes the DB)
// without standing up the full server (#740).
var RunHTTPServer = runHTTPServer
