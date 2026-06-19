// Package config provides configuration for the application.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/starquake/topbanana/internal/request"
)

// ErrDBURINotSetInProduction is returned when DB_URI is not set in production. We need this to prevent accidental
// production deployments without a database.
var ErrDBURINotSetInProduction = errors.New("DB_URI must be set in production")

// ErrSessionKeyRequired is returned when SESSION_KEY is not set in an
// environment that requires an explicit key. Every APP_ENV except development
// requires one so session cookies survive process restarts; development falls
// back to an ephemeral random key so localhost runs need no configuration.
var ErrSessionKeyRequired = errors.New("SESSION_KEY must be set")

// ErrSessionKeyTooShort is returned when an explicit SESSION_KEY is set but is
// shorter than the minimum. The same key HMAC-signs session cookies, CSRF
// tokens, OAuth state, and signed flashes; a short, low-entropy key makes those
// signatures offline-forgeable (an auth bypass), so a minimum length is
// enforced rather than trusting any non-empty value (#782).
var ErrSessionKeyTooShort = fmt.Errorf("SESSION_KEY must be at least %d bytes", sessionKeyByteLength)

// ErrRevealDelayNegative is returned when REVEAL_DELAY parses to a negative
// duration. The reveal beat sits in the future on every question, so a
// negative value would silently break the gameplay timing contract.
var ErrRevealDelayNegative = errors.New("REVEAL_DELAY must not be negative")

// ErrLoginCooldownNegative is returned when LOGIN_COOLDOWN parses to a
// negative duration. The cooldown is a minimum gap between POST /login
// attempts, so a negative value is meaningless; reject it rather than
// silently treating it as "no cooldown".
var ErrLoginCooldownNegative = errors.New("LOGIN_COOLDOWN must not be negative")

// ErrSessionRunnerBeatNegative is returned when SESSION_RUNNER_BEAT parses to
// a negative duration. It shrinks the live-session runner's round-intro,
// reveal, and between-rounds beats, so a negative value is meaningless.
var ErrSessionRunnerBeatNegative = errors.New("SESSION_RUNNER_BEAT must not be negative")

// ErrSessionRevealBeatNegative is returned when SESSION_REVEAL_BEAT parses to a
// negative duration.
var ErrSessionRevealBeatNegative = errors.New("SESSION_REVEAL_BEAT must not be negative")

// ErrSessionRoundIntroBeatNegative is returned when SESSION_ROUND_INTRO_BEAT
// parses to a negative duration.
var ErrSessionRoundIntroBeatNegative = errors.New("SESSION_ROUND_INTRO_BEAT must not be negative")

// ErrSessionStartCountdownNegative is returned when SESSION_START_COUNTDOWN
// parses to a negative duration. It is the length of the host-armed last-call
// countdown, so a negative value is meaningless; reject it rather than
// silently treating it as "start immediately".
var ErrSessionStartCountdownNegative = errors.New("SESSION_START_COUNTDOWN must not be negative")

// ErrSessionIdleCloseNegative is returned when SESSION_IDLE_CLOSE parses to a
// negative duration. It is how long a room may sit idle (host gone, no active
// players) before the runner closes it, so a negative value is meaningless.
var ErrSessionIdleCloseNegative = errors.New("SESSION_IDLE_CLOSE must not be negative")

// ErrMediaUploadBudgetNegative is returned when MEDIA_UPLOAD_BUDGET parses to a
// negative integer. It is the per-host file allowance over the rolling window,
// so a negative value is meaningless; zero is allowed and disables the limiter
// (every charge admits) for tests and trusted single-tenant deployments.
var ErrMediaUploadBudgetNegative = errors.New("MEDIA_UPLOAD_BUDGET must not be negative")

// ErrMediaUploadBudgetWindowNegative is returned when MEDIA_UPLOAD_BUDGET_WINDOW
// parses to a negative duration. It is the rolling window the per-host upload
// budget is measured over, so a negative value is meaningless.
var ErrMediaUploadBudgetWindowNegative = errors.New("MEDIA_UPLOAD_BUDGET_WINDOW must not be negative")

// ErrMediaQuizImageLimitNegative is returned when MEDIA_QUIZ_IMAGE_LIMIT parses
// to a negative integer. It is the per-quiz library ceiling, so a negative
// value is meaningless; zero is allowed and disables the cap.
var ErrMediaQuizImageLimitNegative = errors.New("MEDIA_QUIZ_IMAGE_LIMIT must not be negative")

// ErrMediaAudioMaxBytesNegative is returned when MEDIA_AUDIO_MAX_BYTES parses to
// a negative integer. It caps a stored audio upload's raw size, so a negative
// value is meaningless; zero is allowed and disables the cap.
var ErrMediaAudioMaxBytesNegative = errors.New("MEDIA_AUDIO_MAX_BYTES must not be negative")

// ErrMediaImageMaxBytesNegative is returned when MEDIA_IMAGE_MAX_BYTES parses to
// a negative integer. It caps a stored image upload's raw size, so a negative
// value is meaningless; zero is allowed and disables the cap.
var ErrMediaImageMaxBytesNegative = errors.New("MEDIA_IMAGE_MAX_BYTES must not be negative")

// ErrSMTPConfigIncomplete is returned when SMTP env vars are partially
// populated. SMTP is opt-in (an unconfigured instance still boots and
// the no-op mailer kicks in), but a partial configuration is almost
// always an operator typo - failing fast at startup keeps a half-wired
// mailer from silently swallowing every send.
var ErrSMTPConfigIncomplete = errors.New("SMTP configuration is incomplete")

// ErrSMTPPortInvalid is returned when SMTP_PORT is set but does not
// parse as a TCP port in the range 1..65535. Same fail-fast rationale
// as ErrSMTPConfigIncomplete.
var ErrSMTPPortInvalid = errors.New("SMTP_PORT must be an integer in 1..65535")

// ErrSMTPAuthIncomplete is returned when one of SMTP_USERNAME /
// SMTP_PASSWORD is set without the other. The go-mail PLAIN auth
// negotiation needs both; setting just one is always an operator typo,
// so fail-fast at startup rather than dialing with an empty
// password (or worse, an empty username paired with a real password
// leaking into the logs).
var ErrSMTPAuthIncomplete = errors.New("SMTP_USERNAME and SMTP_PASSWORD must both be set or both empty")

// ErrSMTPAuthOverCleartext is returned when SMTP credentials are
// configured but SMTP_TLS is false, which would send the username and
// password as PLAIN auth over an unencrypted connection. The local
// Mailpit setup (SMTP_TLS=false with no credentials) stays allowed.
var ErrSMTPAuthOverCleartext = errors.New(
	"smtp_username and smtp_password require smtp_tls=true; refusing to send credentials over cleartext")

const (
	// AppEnvironmentDefault is the default application environment.
	AppEnvironmentDefault = "development"
	// AppEnvironmentProduction is the production environment value. Several
	// behaviours flip on this (see [Config.SecureCookies] and the DB_URI /
	// SESSION_KEY validation in [Parse]).
	AppEnvironmentProduction = "production"
	// ClientDirDefault specifies the default directory for the player-client static files.
	ClientDirDefault = ""
	// WebStaticDirDefault is the default override for the shared static-asset
	// directory served at /static/. Empty means "serve from the embedded FS"; a
	// development override (e.g. WEB_STATIC_DIR=internal/assets/static) makes
	// `make tailwind` regens visible without a binary restart, mirroring
	// CLIENT_DIR for the player-client half.
	WebStaticDirDefault = ""

	// MediaDirDefault is the default filesystem directory for uploaded media
	// (#936). Dev writes into ./media in the working directory; staging and
	// production must point this at a persistent writable volume (the
	// distroless image has no writable app FS by default) or uploads are lost
	// on every container restart.
	MediaDirDefault = "./media"

	// HostDefault is the default host to listen on. Can be an IP address or hostname.
	HostDefault = "localhost"
	// PortDefault is the default port to listen on.
	PortDefault = "8080"

	// DBDriverDefault is the default database driver. Currently, only sqlite is supported.
	DBDriverDefault = "sqlite"
	// DBURIDefault is the default database URI. Default is topbanana.sqlite in the current directory.
	DBURIDefault = "file:topbanana.sqlite?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_txlock=immediate"
	// DBMaxOpenConnsDefault is the default maximum number of open database connections.
	DBMaxOpenConnsDefault = 10
	// DBMaxIdleConnsDefault is the default maximum number of idle database connections.
	DBMaxIdleConnsDefault = 10
	// DBConnMaxLifetimeDefault is the default maximum lifetime of a database connection.
	DBConnMaxLifetimeDefault = 5 * time.Minute

	// LoginCooldownDefault is the default per-IP minimum gap between POST
	// /login attempts. Mirrors auth.loginCooldown; config does not import
	// auth (the dependency runs the other way through the server wiring),
	// so the value is duplicated here and the server wiring passes this
	// field into auth.NewLoginRateLimiter.
	LoginCooldownDefault = 3 * time.Second

	// MediaUploadBudgetDefault is the default per-host file allowance over
	// MediaUploadBudgetWindow (#988). Set generously for a real host folder
	// upload (the per-request cap is 10 files, so this is six full batches a
	// minute) while still bounding a runaway one-request-per-file flood. The
	// form JS fires one request per picked file, so this charges the file count
	// per request to keep many single-file POSTs on the same budget as one big
	// batch.
	MediaUploadBudgetDefault = 60

	// MediaUploadBudgetWindowDefault is the default rolling window the per-host
	// upload budget is measured over. One minute pairs with the 60-file default
	// so a host can sustain roughly one image a second without hitting the cap.
	MediaUploadBudgetWindowDefault = time.Minute

	// MediaQuizImageLimitDefault is the default per-quiz image library ceiling
	// (#988). Generous for a real quiz (a question typically uses one image)
	// while bounding the disk and row growth one host can drive on a single
	// quiz.
	MediaQuizImageLimitDefault = 200

	// MediaAudioMaxBytesDefault is the default cap on a stored audio upload's raw
	// size (~20 MB, #1059). Audio is stored as-is (no transcoding), so the cap
	// bounds the bytes one clip can occupy; 20 MB comfortably holds a typical
	// short question sound while rejecting an oversized file.
	MediaAudioMaxBytesDefault int64 = 20 << 20

	// MediaImageMaxBytesDefault is the default cap on a stored image upload's raw
	// size (~10 MB, #1059). It mirrors media.MaxUploadBytes; the literal is
	// repeated here rather than imported so config does not depend on the media
	// package.
	MediaImageMaxBytesDefault int64 = 10 << 20

	// sessionKeyByteLength is the length in bytes of an ephemeral session key generated for development.
	sessionKeyByteLength = 32
)

// Config represents the application configuration.
type Config struct {
	AppEnvironment string

	Host string
	Port string

	DBDriver string
	DBURI    string

	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration

	// MediaDir is the filesystem directory uploaded media is written under,
	// in a per-quiz subdirectory (#936). Defaults to ./media; staging and
	// production point it at a persistent volume since the distroless image
	// has no writable app FS by default. Created at startup if missing.
	MediaDir string

	ClientDir string

	// WebStaticDir overrides the on-disk path served at /static/ for the
	// shared static assets. Empty means "serve from the embedded FS"
	// (the production default); set to e.g. internal/assets/static in dev
	// so a `make tailwind` regen lands without a binary restart. Honoured
	// only when AppEnvironment == "development", matching ClientDir.
	WebStaticDir string

	SessionKey string

	// AdminEmails is the allowlist of email addresses promoted to admin once the
	// address is proven verified (at email-verify-token consume or OAuth callback),
	// not at registration. Parsed from the comma-separated ADMIN_EMAILS env var.
	// Matching against the verified email keeps admin status pinned to the identity
	// we already authenticate, not the display name a player can change.
	AdminEmails []string

	// RegistrationEnabled gates the /register routes. Defaults to false so registration is
	// opt-in per deployment. Parsed from the REGISTRATION_ENABLED env var via strconv.ParseBool.
	RegistrationEnabled bool

	// RevealDelay overrides the per-question reveal beat (#247). Zero means
	// "use the built-in default" (3 s). Parsed from the REVEAL_DELAY env var
	// via time.ParseDuration; e2e and load-test deployments shrink this to a
	// few hundred ms to speed up runs without losing the visual reveal phase.
	RevealDelay time.Duration

	// SessionRunnerBeat overrides the live-session runner's round-intro,
	// reveal, and between-rounds beats (MP-5 / #682). Zero means "use the
	// built-in defaults" (3s / 4s / 6s). Parsed from the SESSION_RUNNER_BEAT
	// env var via time.ParseDuration; the e2e and integration suites shrink it
	// to a few milliseconds so a hosted game marches through its phases without
	// slowing the suite.
	SessionRunnerBeat time.Duration

	// SessionRevealBeat overrides only the post-answer reveal beat, on top of
	// SessionRunnerBeat. Zero means "track SessionRunnerBeat". Parsed from
	// SESSION_REVEAL_BEAT. The e2e suite shrinks SessionRunnerBeat to a few
	// hundred ms to keep the suite fast, but that leaves the reveal phase too
	// brief for a slow browser to observe the revealed answer before it
	// advances; this knob keeps the reveal comfortably observable without
	// slowing the other beats (mirrors REVEAL_DELAY for the pre-answer beat).
	SessionRevealBeat time.Duration

	// SessionRoundIntroBeat overrides only the round-intro beat, on top of
	// SessionRunnerBeat. Zero means "track SessionRunnerBeat". Parsed from
	// SESSION_ROUND_INTRO_BEAT. Like SessionRevealBeat, the e2e suite shrinks
	// SessionRunnerBeat for speed, but that leaves the round-intro card (round
	// title + "Round N of M" eyebrow) too brief for a loaded browser to observe
	// before the phase advances; this knob keeps it observable without slowing
	// the other beats (#859).
	SessionRoundIntroBeat time.Duration

	// SessionStartCountdown is the length of the host-armed last-call countdown
	// (#735): the host arms "Start in 60s" and the runner starts the game when
	// it elapses. Zero means "use the built-in default" (60s). Parsed from the
	// SESSION_START_COUNTDOWN env var via time.ParseDuration; the e2e suite
	// shrinks it to a couple of seconds so the armed-start spec does not pay the
	// production dwell time.
	SessionStartCountdown time.Duration

	// SessionIdleClose is how long a hosted room may sit idle - its host gone
	// (no host heartbeat) AND no active players - before the runner closes it
	// (#836). Zero means "use the built-in default" (30m). Parsed from the
	// SESSION_IDLE_CLOSE env var via time.ParseDuration; the e2e/integration
	// suites shrink it so an idle-close spec does not wait the production window.
	SessionIdleClose time.Duration

	// LoginCooldown is the per-IP minimum gap between POST /login attempts,
	// passed into auth.NewLoginRateLimiter (#494). Defaults to 3s (mirrors
	// auth.loginCooldown via LoginCooldownDefault). Parsed from the
	// LOGIN_COOLDOWN env var via time.ParseDuration; the e2e suite sets it
	// to 0 to disable the limiter for rapid same-IP logins.
	LoginCooldown time.Duration

	// MediaUploadBudget is the maximum number of image files one host may upload
	// within MediaUploadBudgetWindow, charged by file count per request so the
	// one-request-per-file upload JS cannot bypass the per-request count cap
	// (#988). Defaults to 60; the e2e/integration suites shrink it via
	// MEDIA_UPLOAD_BUDGET to exercise the 429 path without a real flood. Zero
	// disables the limiter (every upload admits).
	MediaUploadBudget int

	// MediaUploadBudgetWindow is the rolling window MediaUploadBudget is measured
	// over. Defaults to 1 minute. Parsed from MEDIA_UPLOAD_BUDGET_WINDOW via
	// parseNonNegativeDuration.
	MediaUploadBudgetWindow time.Duration

	// MediaQuizImageLimit is the per-quiz image library ceiling: an upload that
	// would push a quiz's stored image count over this is rejected (#988).
	// Defaults to 200. Parsed from MEDIA_QUIZ_IMAGE_LIMIT; the e2e/integration
	// suites shrink it to exercise the 409 path. Zero disables the cap.
	MediaQuizImageLimit int

	// MediaAudioMaxBytes caps a stored audio upload's raw size in bytes (#1059).
	// Defaults to MediaAudioMaxBytesDefault (~20 MB). Parsed from
	// MEDIA_AUDIO_MAX_BYTES; zero disables the cap.
	MediaAudioMaxBytes int64

	// MediaImageMaxBytes caps a stored image upload's raw size in bytes (#1059).
	// Defaults to MediaImageMaxBytesDefault (~10 MB). Parsed from
	// MEDIA_IMAGE_MAX_BYTES; zero disables the cap.
	MediaImageMaxBytes int64

	// GoogleClientID, GoogleClientSecret, and GoogleRedirectURL are the
	// Google OAuth 2.0 credentials issued in the Google Cloud Console.
	// All three must be set for the /login/google routes to register; if
	// any is empty the feature stays off (the button hides, the routes
	// 404). Mirrors the RegistrationEnabled opt-in pattern so a fresh
	// deployment does not surprise operators with extra surface.
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string

	// GoogleIssuerURL overrides the OIDC issuer base URL. Tests point
	// this at an httptest.Server so the OIDC verifier fetches its
	// discovery and JWKS from the mock instead of Google. Empty in
	// production; the handler falls back to Google's documented
	// issuer ("https://accounts.google.com") when this is unset.
	GoogleIssuerURL string

	// SMTPHost / SMTPPort / SMTPUsername / SMTPPassword / SMTPFrom /
	// SMTPTLS are the mailer connection knobs (#321). SMTP is optional:
	// a deployment with none of these vars set still boots and the
	// no-op mailer satisfies the mailer.Mailer interface so consumer
	// endpoints surface a clear "email not configured" instead of a
	// 500. A partial config (host + from but no port, for example) is
	// rejected at startup by [Parse] - half-wired SMTP is almost
	// always an operator typo and silently swallowing every send is
	// worse than a fail-fast boot.
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	// SMTPTLS reports whether the mailer should require STARTTLS on
	// the SMTP connection. Defaults to true so a production deploy
	// gets encryption-in-transit by default; the local Mailpit dev
	// setup flips this off via SMTP_TLS=false.
	SMTPTLS bool

	// BaseURL is the absolute URL prefix used when an outgoing email
	// has to embed a link back into the app (#290 verify, #318 invite,
	// etc). Empty when unset - the mailer-using consumer is expected
	// to either fall back to the request's absolute URL or refuse to
	// render the link if BaseURL is required and absent.
	BaseURL string

	// TrustedProxyCIDRs is the parsed allow-list of upstream reverse
	// proxies whose X-Forwarded-For header the per-IP rate limiters
	// should honour. Empty (the default) means "no proxy in front" -
	// XFF is ignored entirely and limiters bucket on RemoteAddr only,
	// which is the only fail-secure default when the binary is
	// exposed directly. Parsed from the TRUSTED_PROXY_IPS env var as
	// a comma-separated CIDR list; see #463.
	TrustedProxyCIDRs []*net.IPNet
}

// DatabaseConfig holds only the database settings setupDB needs. The
// break-glass CLI tools (reset-password, promote-admin) resolve just
// these via [ParseDatabase] so they run in a half-configured or
// locked-out environment without the full-server validation (SESSION_KEY,
// SMTP, OAuth, ...) standing in the way.
type DatabaseConfig struct {
	Driver          string
	URI             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// DatabaseConfig returns the database-only view of the full config so
// server-boot callers and the break-glass tools share one path into
// setupDB.
func (c *Config) DatabaseConfig() DatabaseConfig {
	return DatabaseConfig{
		Driver:          c.DBDriver,
		URI:             c.DBURI,
		MaxOpenConns:    c.DBMaxOpenConns,
		MaxIdleConns:    c.DBMaxIdleConns,
		ConnMaxLifetime: c.DBConnMaxLifetime,
	}
}

// ParseDatabase resolves only the database settings, applying the same
// rules as [Parse] (DB_URI from env falling back to DBURIDefault outside
// production, ErrDBURINotSetInProduction when unset in production, driver
// and pool defaults plus their env overrides) without requiring
// SESSION_KEY or any other server field. This keeps the break-glass CLI
// tools runnable when the rest of the config is missing or broken.
func ParseDatabase(getenv func(string) string) (DatabaseConfig, error) {
	appEnvironment := getenv("APP_ENV")

	dbc := DatabaseConfig{
		Driver:          DBDriverDefault,
		URI:             DBURIDefault,
		MaxOpenConns:    DBMaxOpenConnsDefault,
		MaxIdleConns:    DBMaxIdleConnsDefault,
		ConnMaxLifetime: DBConnMaxLifetimeDefault,
	}
	if err := resolveDatabaseConfig(getenv, appEnvironment, &dbc); err != nil {
		return DatabaseConfig{}, err
	}

	return dbc, nil
}

// resolveDatabaseConfig applies the DB_URI fallback / production guard and
// the pool-setting env overrides onto dbc. Shared by [Parse] and
// [ParseDatabase] so the two cannot drift.
func resolveDatabaseConfig(getenv func(string) string, appEnvironment string, dbc *DatabaseConfig) error {
	if val := getenv("DB_URI"); val != "" {
		dbc.URI = val
	}
	if appEnvironment == AppEnvironmentProduction && getenv("DB_URI") == "" {
		return ErrDBURINotSetInProduction
	}

	if val := getenv("DB_MAX_OPEN_CONNS"); val != "" {
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("invalid DB_MAX_OPEN_CONNS: %q, err: %w", val, err)
		}
		dbc.MaxOpenConns = n
	}

	if val := getenv("DB_MAX_IDLE_CONNS"); val != "" {
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("invalid DB_MAX_IDLE_CONNS: %q, err: %w", val, err)
		}
		dbc.MaxIdleConns = n
	}

	if val := getenv("DB_CONN_MAX_LIFETIME"); val != "" {
		d, err := time.ParseDuration(val)
		if err != nil {
			return fmt.Errorf("invalid DB_CONN_MAX_LIFETIME: %q, err: %w", val, err)
		}
		dbc.ConnMaxLifetime = d
	}

	return nil
}

// SMTPConfigured reports whether enough SMTP env vars are populated
// for the real mailer to dial out. Used by [cmd/server/app.app] to
// pick between the go-mail-backed mailer and the no-op stub. Mirrors
// the GoogleLoginEnabled boolean so a deployment opts into SMTP by
// setting credentials rather than flipping a separate switch.
func (c *Config) SMTPConfigured() bool {
	return c.SMTPHost != "" && c.SMTPPort > 0 && c.SMTPFrom != ""
}

// SecureCookies reports whether session and CSRF cookies should be issued
// with the Secure attribute. Default is true; only the explicit
// `development` env opts out so the dev server is reachable from any
// LAN hostname over plain HTTP (chip.local, 192.168.x.x, devtunnels, ...) -
// browsers reject Secure cookies on non-HTTPS contexts and the rejection
// cascades into "forbidden: invalid CSRF token" failures otherwise (#205).
// Every other env (production, staging, demo, qa, unset) gets the flag so
// a credential-bearing cookie can't accidentally leak over plain HTTP on
// a non-production deploy (#340). Unset is intentionally fail-secure -
// Parse leaves AppEnvironment as the empty string when APP_ENV is unset
// so a bare-binary boot in a production-like context defaults to Secure.
func (c *Config) SecureCookies() bool {
	return c.AppEnvironment != AppEnvironmentDefault
}

// EnvTitleTag returns a bracketed environment label for page titles,
// or the empty string on production. Renders as e.g. "[staging] " so a
// templated title can prefix it unconditionally and the production case
// disappears. An empty AppEnvironment (fail-secure boot with APP_ENV
// unset) renders as "[unknown] " so an operator never confuses an
// accidentally-bare deploy with production.
func (c *Config) EnvTitleTag() string {
	switch c.AppEnvironment {
	case AppEnvironmentProduction:
		return ""
	case "":
		return "[unknown] "
	default:
		return "[" + c.AppEnvironment + "] "
	}
}

// defaultConfig returns a Config seeded with the built-in defaults, before any
// environment overrides are applied. AppEnvironment is intentionally left unset
// so an unset APP_ENV fails secure (see Parse).
func defaultConfig() Config {
	return Config{
		ClientDir:               ClientDirDefault,
		WebStaticDir:            WebStaticDirDefault,
		MediaDir:                MediaDirDefault,
		Host:                    HostDefault,
		Port:                    PortDefault,
		DBDriver:                DBDriverDefault,
		DBURI:                   DBURIDefault,
		DBMaxOpenConns:          DBMaxOpenConnsDefault,
		DBMaxIdleConns:          DBMaxIdleConnsDefault,
		DBConnMaxLifetime:       DBConnMaxLifetimeDefault,
		LoginCooldown:           LoginCooldownDefault,
		MediaUploadBudget:       MediaUploadBudgetDefault,
		MediaUploadBudgetWindow: MediaUploadBudgetWindowDefault,
		MediaQuizImageLimit:     MediaQuizImageLimitDefault,
		MediaAudioMaxBytes:      MediaAudioMaxBytesDefault,
		MediaImageMaxBytes:      MediaImageMaxBytesDefault,
	}
}

// Parse parses environment variables into the config.
func Parse(getenv func(string) string) (*Config, error) {
	c := defaultConfig()
	// AppEnvironment is intentionally NOT pre-initialised to the
	// development default: an unset APP_ENV is meant to fail-secure
	// (Secure cookies on, SESSION_KEY required) so a bare-binary boot
	// in a production-like context never silently drops the Secure
	// attribute. Operators opt into the relaxed dev behaviour by
	// setting APP_ENV=development explicitly (the Makefile defaults
	// this for make server / dev / smoke). See [Config.SecureCookies]
	// and #378.
	if val := getenv("APP_ENV"); val != "" {
		c.AppEnvironment = val
	}
	if val := getenv("HOST"); val != "" {
		c.Host = val
	}
	if val := getenv("PORT"); val != "" {
		c.Port = val
	}
	if c.AppEnvironment == "development" {
		if val := getenv("CLIENT_DIR"); val != "" {
			c.ClientDir = val
		}
		if val := getenv("WEB_STATIC_DIR"); val != "" {
			c.WebStaticDir = val
		}
	}
	if val := getenv("MEDIA_DIR"); val != "" {
		c.MediaDir = val
	}

	if err := c.applyDatabaseConfig(getenv); err != nil {
		return nil, err
	}

	if err := parseTypedEnvVars(getenv, &c); err != nil {
		return nil, err
	}

	key, err := resolveSessionKey(getenv("SESSION_KEY"), c.AppEnvironment)
	if err != nil {
		return nil, err
	}
	c.SessionKey = key

	c.AdminEmails = parseAdminEmails(getenv("ADMIN_EMAILS"))

	c.GoogleClientID = getenv("GOOGLE_CLIENT_ID")
	c.GoogleClientSecret = getenv("GOOGLE_CLIENT_SECRET")
	c.GoogleRedirectURL = getenv("GOOGLE_REDIRECT_URL")
	c.GoogleIssuerURL = getenv("GOOGLE_ISSUER_URL")

	if err = parseSMTPConfig(getenv, &c); err != nil {
		return nil, err
	}

	c.BaseURL = strings.TrimRight(getenv("BASE_URL"), "/")

	c.TrustedProxyCIDRs, err = request.ParseTrustedProxyCIDRs(getenv("TRUSTED_PROXY_IPS"))
	if err != nil {
		return nil, fmt.Errorf("invalid TRUSTED_PROXY_IPS: %w", err)
	}

	return &c, nil
}

// GoogleLoginEnabled reports whether all three Google OAuth env vars are
// populated. The Google sign-in routes only register when this returns
// true; the login template hides the button as well. Lets a deployment
// roll out the feature by setting credentials rather than flipping a
// separate REGISTRATION_ENABLED-style switch.
func (c *Config) GoogleLoginEnabled() bool {
	return c.GoogleClientID != "" && c.GoogleClientSecret != "" && c.GoogleRedirectURL != ""
}

// parseTypedEnvVars reads strict-typed env vars (ints, durations, bools) into c. It returns a
// wrapped error if any value fails to parse. The DB pool settings are
// resolved separately via [resolveDatabaseConfig] so the break-glass tools
// can reuse them without the rest of the server config.
func parseTypedEnvVars(getenv func(string) string, c *Config) error {
	if val := getenv("REGISTRATION_ENABLED"); val != "" {
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("invalid REGISTRATION_ENABLED: %q, err: %w", val, err)
		}
		c.RegistrationEnabled = b
	}

	if err := parseNonNegativeDuration(getenv, "REVEAL_DELAY", ErrRevealDelayNegative, &c.RevealDelay); err != nil {
		return err
	}

	if err := parseNonNegativeDuration(
		getenv, "SESSION_RUNNER_BEAT", ErrSessionRunnerBeatNegative, &c.SessionRunnerBeat,
	); err != nil {
		return err
	}

	if err := parseNonNegativeDuration(
		getenv, "SESSION_REVEAL_BEAT", ErrSessionRevealBeatNegative, &c.SessionRevealBeat,
	); err != nil {
		return err
	}

	if err := parseNonNegativeDuration(
		getenv, "SESSION_ROUND_INTRO_BEAT", ErrSessionRoundIntroBeatNegative, &c.SessionRoundIntroBeat,
	); err != nil {
		return err
	}

	if err := parseNonNegativeDuration(
		getenv, "SESSION_START_COUNTDOWN", ErrSessionStartCountdownNegative, &c.SessionStartCountdown,
	); err != nil {
		return err
	}

	if err := parseNonNegativeDuration(
		getenv, "SESSION_IDLE_CLOSE", ErrSessionIdleCloseNegative, &c.SessionIdleClose,
	); err != nil {
		return err
	}

	if err := parseNonNegativeDuration(
		getenv, "LOGIN_COOLDOWN", ErrLoginCooldownNegative, &c.LoginCooldown,
	); err != nil {
		return err
	}

	return parseMediaUploadLimits(getenv, c)
}

// parseMediaUploadLimits reads the upload-backstop env vars (#988) into c: the
// per-host file budget and its window, plus the per-quiz library ceiling. Split
// out of parseTypedEnvVars so that function stays within the function-length
// limit. Each is non-negative; zero disables the corresponding guard.
func parseMediaUploadLimits(getenv func(string) string, c *Config) error {
	if err := parseNonNegativeInt(
		getenv, "MEDIA_UPLOAD_BUDGET", ErrMediaUploadBudgetNegative, &c.MediaUploadBudget,
	); err != nil {
		return err
	}

	if err := parseNonNegativeDuration(
		getenv, "MEDIA_UPLOAD_BUDGET_WINDOW", ErrMediaUploadBudgetWindowNegative, &c.MediaUploadBudgetWindow,
	); err != nil {
		return err
	}

	if err := parseNonNegativeInt(
		getenv, "MEDIA_QUIZ_IMAGE_LIMIT", ErrMediaQuizImageLimitNegative, &c.MediaQuizImageLimit,
	); err != nil {
		return err
	}

	if err := parseNonNegativeInt64(
		getenv, "MEDIA_AUDIO_MAX_BYTES", ErrMediaAudioMaxBytesNegative, &c.MediaAudioMaxBytes,
	); err != nil {
		return err
	}

	return parseNonNegativeInt64(
		getenv, "MEDIA_IMAGE_MAX_BYTES", ErrMediaImageMaxBytesNegative, &c.MediaImageMaxBytes,
	)
}

// parseNonNegativeDuration reads the named env var as a duration into
// dst, leaving dst untouched when the var is unset. An unparseable value
// returns a wrapped error naming the var; a negative value returns the
// supplied sentinel so callers can match it with [errors.Is].
func parseNonNegativeDuration(
	getenv func(string) string, name string, negativeErr error, dst *time.Duration,
) error {
	val := getenv(name)
	if val == "" {
		return nil
	}
	d, err := time.ParseDuration(val)
	if err != nil {
		return fmt.Errorf("invalid %s: %q, err: %w", name, val, err)
	}
	if d < 0 {
		return fmt.Errorf("%w: %q", negativeErr, val)
	}
	*dst = d

	return nil
}

// parseNonNegativeInt reads the named env var as an integer into dst, leaving
// dst untouched when the var is unset. An unparseable value returns a wrapped
// error naming the var; a negative value returns the supplied sentinel so
// callers can match it with [errors.Is]. Zero is accepted (the media upload
// guards treat zero as "disabled").
func parseNonNegativeInt(getenv func(string) string, name string, negativeErr error, dst *int) error {
	val := getenv(name)
	if val == "" {
		return nil
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return fmt.Errorf("invalid %s: %q, err: %w", name, val, err)
	}
	if n < 0 {
		return negativeValueErr(negativeErr, val)
	}
	*dst = n

	return nil
}

// parseNonNegativeInt64 is parseNonNegativeInt for an int64 destination, used by
// the byte-sized caps that can exceed an int's guaranteed range. An unparseable
// value returns a wrapped error naming the var; a negative value returns the
// supplied sentinel so callers can match it with [errors.Is]. Zero is accepted
// (the media caps treat zero as "disabled").
func parseNonNegativeInt64(getenv func(string) string, name string, negativeErr error, dst *int64) error {
	val := getenv(name)
	if val == "" {
		return nil
	}
	n, err := strconv.ParseInt(val, decimalBase, bitSize64)
	if err != nil {
		return fmt.Errorf("invalid %s: %q, err: %w", name, val, err)
	}
	if n < 0 {
		return negativeValueErr(negativeErr, val)
	}
	*dst = n

	return nil
}

// decimalBase and bitSize64 are the base and bit width for parsing an int64 env
// var with [strconv.ParseInt].
const (
	decimalBase = 10
	bitSize64   = 64
)

// negativeValueErr wraps sentinel with the offending value so callers can match
// it with [errors.Is] while still seeing the parsed string.
func negativeValueErr(sentinel error, val string) error {
	return fmt.Errorf("%w: %q", sentinel, val)
}

// parseSMTPConfig reads the SMTP_* env vars into c. SMTP defaults to
// "off" so an empty env block boots; a partially populated block (e.g.
// SMTP_HOST set but SMTP_FROM empty) is rejected with
// ErrSMTPConfigIncomplete so half-wired mailers never silently drop
// mail. SMTPTLS defaults to true; the dev Mailpit setup flips it off
// via SMTP_TLS=false.
func parseSMTPConfig(getenv func(string) string, c *Config) error {
	c.SMTPHost = getenv("SMTP_HOST")
	c.SMTPUsername = getenv("SMTP_USERNAME")
	c.SMTPPassword = getenv("SMTP_PASSWORD")
	c.SMTPFrom = getenv("SMTP_FROM")

	c.SMTPTLS = true
	// tlsExplicit pins whether the operator actually wrote SMTP_TLS.
	// We feed this into the "populated subset" check below so a lone
	// SMTP_TLS=false (a typo'd partial rollout) trips
	// ErrSMTPConfigIncomplete instead of silently booting the no-op
	// mailer. Empty string is treated as unset to match the rest of
	// the SMTP block, which keeps the parser's contract uniform.
	tlsExplicit := false
	if val := getenv("SMTP_TLS"); val != "" {
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("invalid SMTP_TLS: %q, err: %w", val, err)
		}
		c.SMTPTLS = b
		tlsExplicit = true
	}

	if val := getenv("SMTP_PORT"); val != "" {
		const maxTCPPort = 65535
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 || n > maxTCPPort {
			return fmt.Errorf("%w: %q", ErrSMTPPortInvalid, val)
		}
		c.SMTPPort = n
	}

	// Treat the SMTP block as "off" only when every var is empty; any
	// populated subset (including a lone SMTP_TLS) must complete the
	// host/port/from triple, which is the minimum a real mailer needs
	// to dial out.
	allEmpty := c.SMTPHost == "" && c.SMTPPort == 0 && c.SMTPFrom == "" &&
		c.SMTPUsername == "" && c.SMTPPassword == "" && !tlsExplicit
	if allEmpty {
		return nil
	}
	if c.SMTPHost == "" || c.SMTPPort == 0 || c.SMTPFrom == "" {
		return fmt.Errorf(
			"%w: set SMTP_HOST, SMTP_PORT, and SMTP_FROM (got host=%q port=%d from=%q)",
			ErrSMTPConfigIncomplete, c.SMTPHost, c.SMTPPort, c.SMTPFrom,
		)
	}
	// PLAIN auth needs both halves; one without the other is always
	// an operator typo.
	if (c.SMTPUsername == "") != (c.SMTPPassword == "") {
		return fmt.Errorf(
			"%w (got username=%q, password set=%v)",
			ErrSMTPAuthIncomplete, c.SMTPUsername, c.SMTPPassword != "",
		)
	}
	// PLAIN auth over a cleartext connection leaks the credentials on
	// the wire. Refuse the combination rather than dialing insecurely.
	if c.SMTPUsername != "" && !c.SMTPTLS {
		return ErrSMTPAuthOverCleartext
	}

	return nil
}

// parseAdminEmails splits a comma-separated list, trims whitespace,
// lowercases each entry, and drops empty entries. Lowercasing matches
// how the register handler normalises the form value before comparing,
// so an operator-typed mixed-case allowlist entry still matches the
// registrant's verified email.
func parseAdminEmails(raw string) []string {
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.ToLower(strings.TrimSpace(p))
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}

// resolveSessionKey returns the session key for cookie signing. An explicit
// value is required in every environment except development; development
// generates a random ephemeral key so localhost runs need no configuration.
// The previous policy only enforced this in production, which let staging
// silently rotate keys on every container restart and invalidate active
// sessions. See #217.
func resolveSessionKey(envValue, appEnvironment string) (string, error) {
	if envValue != "" {
		if len(envValue) < sessionKeyByteLength {
			return "", fmt.Errorf("%w (got %d)", ErrSessionKeyTooShort, len(envValue))
		}

		return envValue, nil
	}
	if appEnvironment != AppEnvironmentDefault {
		return "", fmt.Errorf("%w: APP_ENV=%q", ErrSessionKeyRequired, appEnvironment)
	}

	b := make([]byte, sessionKeyByteLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating ephemeral session key: %w", err)
	}

	return hex.EncodeToString(b), nil
}

// IsProduction returns true if the application is running in production.
func (c *Config) IsProduction() bool {
	return c.AppEnvironment == AppEnvironmentProduction
}

// applyDatabaseConfig resolves the database settings into c via the
// shared resolveDatabaseConfig helper and writes them back onto the
// DB* fields. Pulled out of Parse so Parse stays within the
// function-length limit; ParseDatabase uses the same helper.
func (c *Config) applyDatabaseConfig(getenv func(string) string) error {
	dbc := c.DatabaseConfig()
	if err := resolveDatabaseConfig(getenv, c.AppEnvironment, &dbc); err != nil {
		return err
	}
	c.DBDriver = dbc.Driver
	c.DBURI = dbc.URI
	c.DBMaxOpenConns = dbc.MaxOpenConns
	c.DBMaxIdleConns = dbc.MaxIdleConns
	c.DBConnMaxLifetime = dbc.ConnMaxLifetime

	return nil
}
