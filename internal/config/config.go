// Package config provides configuration for the application.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
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

const (
	// AppEnvironmentDefault is the default application environment.
	AppEnvironmentDefault = "development"
	// AppEnvironmentProduction is the production environment value. Several
	// behaviours flip on this (see [Config.SecureCookies] and the DB_URI /
	// SESSION_KEY validation in [Parse]).
	AppEnvironmentProduction = "production"
	// ClientDirDefault specifies the default directory for client-side static files.
	ClientDirDefault = ""

	// HostDefault is the default host to listen on. Can be an IP address or hostname.
	HostDefault = "localhost"
	// PortDefault is the default port to listen on.
	PortDefault = "8080"

	// DBDriverDefault is the default database driver. Currently, only sqlite is supported.
	DBDriverDefault = "sqlite"
	// DBURIDefault is the default database URI. Default is topbanana.sqlite in the current directory.
	DBURIDefault = "file:topbanana.sqlite?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	// DBMaxOpenConnsDefault is the default maximum number of open database connections.
	DBMaxOpenConnsDefault = 10
	// DBMaxIdleConnsDefault is the default maximum number of idle database connections.
	DBMaxIdleConnsDefault = 10
	// DBConnMaxLifetimeDefault is the default maximum lifetime of a database connection.
	DBConnMaxLifetimeDefault = 5 * time.Minute

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

	SessionKey string

	// AdminUsernames is the list of usernames that are promoted to admin on registration.
	// Parsed from the comma-separated ADMIN_USERNAMES env var.
	AdminUsernames []string

	// RegistrationEnabled gates the /register routes. Defaults to false so registration is
	// opt-in per deployment. Parsed from the REGISTRATION_ENABLED env var via strconv.ParseBool.
	RegistrationEnabled bool

	// RevealDelay overrides the per-question reveal beat (#247). Zero means
	// "use the built-in default" (3 s). Parsed from the REVEAL_DELAY env var
	// via time.ParseDuration; e2e and load-test deployments shrink this to a
	// few hundred ms to speed up runs without losing the visual reveal phase.
	RevealDelay time.Duration
}

// SecureCookies reports whether session and CSRF cookies should be issued
// with the Secure attribute. Returns true only in production. In
// development the flag is dropped so the dev server is reachable from any
// LAN hostname over plain HTTP (chip.local, 192.168.x.x, devtunnels, …) —
// browsers reject Secure cookies on non-HTTPS contexts and the rejection
// cascades into "forbidden: invalid CSRF token" failures otherwise. See
// #205.
func (c *Config) SecureCookies() bool {
	return c.IsProduction()
}

// Parse parses environment variables into the config.
func Parse(getenv func(string) string) (*Config, error) {
	c := Config{
		AppEnvironment:    AppEnvironmentDefault,
		ClientDir:         ClientDirDefault,
		Host:              HostDefault,
		Port:              PortDefault,
		DBDriver:          DBDriverDefault,
		DBURI:             DBURIDefault,
		DBMaxOpenConns:    DBMaxOpenConnsDefault,
		DBMaxIdleConns:    DBMaxIdleConnsDefault,
		DBConnMaxLifetime: DBConnMaxLifetimeDefault,
	}
	// Overwrite defaults with environment variables.
	if val := getenv("APP_ENV"); val != "" {
		c.AppEnvironment = val
	}
	if val := getenv("HOST"); val != "" {
		c.Host = val
	}
	if val := getenv("PORT"); val != "" {
		c.Port = val
	}
	if val := getenv("DB_URI"); val != "" {
		c.DBURI = val
	}
	if c.AppEnvironment == "development" {
		if val := getenv("CLIENT_DIR"); val != "" {
			c.ClientDir = val
		}
	}

	if err := parseTypedEnvVars(getenv, &c); err != nil {
		return nil, err
	}

	// Mandatory fields
	if c.AppEnvironment == AppEnvironmentProduction && getenv("DB_URI") == "" {
		return nil, ErrDBURINotSetInProduction
	}

	key, err := resolveSessionKey(getenv("SESSION_KEY"), c.AppEnvironment)
	if err != nil {
		return nil, err
	}
	c.SessionKey = key

	c.AdminUsernames = parseAdminUsernames(getenv("ADMIN_USERNAMES"))

	return &c, nil
}

// parseTypedEnvVars reads strict-typed env vars (ints, durations, bools) into c. It returns a
// wrapped error if any value fails to parse.
func parseTypedEnvVars(getenv func(string) string, c *Config) error {
	if val := getenv("DB_MAX_OPEN_CONNS"); val != "" {
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("invalid DB_MAX_OPEN_CONNS: %q, err: %w", val, err)
		}
		c.DBMaxOpenConns = n
	}

	if val := getenv("DB_MAX_IDLE_CONNS"); val != "" {
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("invalid DB_MAX_IDLE_CONNS: %q, err: %w", val, err)
		}
		c.DBMaxIdleConns = n
	}

	if val := getenv("DB_CONN_MAX_LIFETIME"); val != "" {
		d, err := time.ParseDuration(val)
		if err != nil {
			return fmt.Errorf("invalid DB_CONN_MAX_LIFETIME: %q, err: %w", val, err)
		}
		c.DBConnMaxLifetime = d
	}

	if val := getenv("REGISTRATION_ENABLED"); val != "" {
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("invalid REGISTRATION_ENABLED: %q, err: %w", val, err)
		}
		c.RegistrationEnabled = b
	}

	if val := getenv("REVEAL_DELAY"); val != "" {
		d, err := time.ParseDuration(val)
		if err != nil {
			return fmt.Errorf("invalid REVEAL_DELAY: %q, err: %w", val, err)
		}
		if d < 0 {
			return fmt.Errorf("%w: %q", ErrRevealDelayNegative, val)
		}
		c.RevealDelay = d
	}

	return nil
}

// parseAdminUsernames splits a comma-separated list, trims whitespace, and drops empty entries.
func parseAdminUsernames(raw string) []string {
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
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
