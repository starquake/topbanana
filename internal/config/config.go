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
// a negative duration. It shrinks the live-session runner's round-intro and
// reveal beats and auto-start window, so a negative value is meaningless.
var ErrSessionRunnerBeatNegative = errors.New("SESSION_RUNNER_BEAT must not be negative")

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
	// WebStaticDirDefault is the default override for the admin/auth/home static-asset
	// directory. Empty means "serve from the embedded FS"; a development override
	// (e.g. WEB_STATIC_DIR=internal/web/static) makes `make tailwind` regens visible
	// without a binary restart, mirroring CLIENT_DIR for the player-client half.
	WebStaticDirDefault = ""

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

	ClientDir string

	// WebStaticDir overrides the on-disk path served at /assets/ for the
	// admin/auth/home shell. Empty means "serve from the embedded FS"
	// (the production default); set to e.g. internal/web/static in dev
	// so a `make tailwind` regen lands without a binary restart. Honoured
	// only when AppEnvironment == "development", matching ClientDir.
	WebStaticDir string

	SessionKey string

	// AdminEmails is the list of email addresses that are promoted to admin on registration.
	// Parsed from the comma-separated ADMIN_EMAILS env var. Match against the verified
	// email keeps admin status pinned to the identity we already authenticate, not the
	// display name a player can change.
	AdminEmails []string

	// RegistrationEnabled gates the /register routes. Defaults to false so registration is
	// opt-in per deployment. Parsed from the REGISTRATION_ENABLED env var via strconv.ParseBool.
	RegistrationEnabled bool

	// RevealDelay overrides the per-question reveal beat (#247). Zero means
	// "use the built-in default" (3 s). Parsed from the REVEAL_DELAY env var
	// via time.ParseDuration; e2e and load-test deployments shrink this to a
	// few hundred ms to speed up runs without losing the visual reveal phase.
	RevealDelay time.Duration

	// SessionRunnerBeat overrides the live-session runner's round-intro and
	// reveal beats and its auto-start ready window (MP-5 / #682). Zero means
	// "use the built-in defaults" (3s / 4s / 5s). Parsed from the
	// SESSION_RUNNER_BEAT env var via time.ParseDuration; the e2e and
	// integration suites shrink it to a few milliseconds so a hosted game
	// marches through its phases without slowing the suite.
	SessionRunnerBeat time.Duration

	// LoginCooldown is the per-IP minimum gap between POST /login attempts,
	// passed into auth.NewLoginRateLimiter (#494). Defaults to 3s (mirrors
	// auth.loginCooldown via LoginCooldownDefault). Parsed from the
	// LOGIN_COOLDOWN env var via time.ParseDuration; the e2e suite sets it
	// to 0 to disable the limiter for rapid same-IP logins.
	LoginCooldown time.Duration

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

// Parse parses environment variables into the config.
func Parse(getenv func(string) string) (*Config, error) {
	c := Config{
		ClientDir:         ClientDirDefault,
		WebStaticDir:      WebStaticDirDefault,
		Host:              HostDefault,
		Port:              PortDefault,
		DBDriver:          DBDriverDefault,
		DBURI:             DBURIDefault,
		DBMaxOpenConns:    DBMaxOpenConnsDefault,
		DBMaxIdleConns:    DBMaxIdleConnsDefault,
		DBConnMaxLifetime: DBConnMaxLifetimeDefault,
		LoginCooldown:     LoginCooldownDefault,
	}
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

	return parseNonNegativeDuration(getenv, "LOGIN_COOLDOWN", ErrLoginCooldownNegative, &c.LoginCooldown)
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
