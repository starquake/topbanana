// Package config provides configuration for the application.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// ErrDBURINotSetInProduction is returned when DB_URI is not set in production. We need this to prevent accidental
// production deployments without a database.
var ErrDBURINotSetInProduction = errors.New("DB_URI must be set in production")

// ErrSessionKeyNotSetInProduction is returned when SESSION_KEY is not set in production. A stable key is required so
// session cookies survive process restarts.
var ErrSessionKeyNotSetInProduction = errors.New("SESSION_KEY must be set in production")

const (
	// AppEnvironmentDefault is the default application environment.
	AppEnvironmentDefault = "development"
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

	// Strict validation for types
	if val := getenv("DB_MAX_OPEN_CONNS"); val != "" {
		var err error
		c.DBMaxOpenConns, err = strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("invalid DB_MAX_OPEN_CONNS: %q, err: %w", val, err)
		}
	}

	if val := getenv("DB_MAX_IDLE_CONNS"); val != "" {
		var err error
		c.DBMaxIdleConns, err = strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("invalid DB_MAX_IDLE_CONNS: %q, err: %w", val, err)
		}
	}

	if val := getenv("DB_CONN_MAX_LIFETIME"); val != "" {
		var err error
		c.DBConnMaxLifetime, err = time.ParseDuration(val)
		if err != nil {
			return nil, fmt.Errorf("invalid DB_CONN_MAX_LIFETIME: %q, err: %w", val, err)
		}
	}

	// Mandatory fields
	if c.AppEnvironment == "production" && getenv("DB_URI") == "" {
		return nil, ErrDBURINotSetInProduction
	}

	key, err := resolveSessionKey(getenv("SESSION_KEY"), c.AppEnvironment)
	if err != nil {
		return nil, err
	}
	c.SessionKey = key

	return &c, nil
}

// resolveSessionKey returns the session key for cookie signing. In production an explicit value is required; in
// development a random ephemeral key is generated when none is provided.
func resolveSessionKey(envValue, appEnvironment string) (string, error) {
	if envValue != "" {
		return envValue, nil
	}
	if appEnvironment == "production" {
		return "", ErrSessionKeyNotSetInProduction
	}

	b := make([]byte, sessionKeyByteLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating ephemeral session key: %w", err)
	}

	return hex.EncodeToString(b), nil
}

// IsProduction returns true if the application is running in production.
func (c *Config) IsProduction() bool {
	return c.AppEnvironment == "production"
}
