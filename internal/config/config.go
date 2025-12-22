// Package config provides configuration for the application.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// ErrDBUriNotSetInProduction is returned when DB_URI is not set in production. We need this to prevent accidental
// production deployments without a database.
var ErrDBUriNotSetInProduction = errors.New("DB_URI must be set in production")

const (
	// AppEnvironmentDefault is the default application environment.
	AppEnvironmentDefault = "development"
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
}

// Parse parses environment variables into the config.
func Parse(getenv func(string) string) (*Config, error) {
	c := Config{
		AppEnvironment:    AppEnvironmentDefault,
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
		return nil, ErrDBUriNotSetInProduction
	}

	return &c, nil
}
