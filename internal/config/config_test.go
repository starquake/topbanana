package config_test

import (
	"errors"
	"testing"

	. "github.com/starquake/topbanana/internal/config"
)

func getenvFailure(failureKey, value string) func(string) string {
	envs := map[string]string{
		"APP_ENV":              "test",
		"HOST":                 "localhost",
		"PORT":                 "5432",
		"DB_URI":               "postgres://localhost:5432/topbanana?sslmode=disable",
		"DB_MAX_OPEN_CONNS":    "100",
		"DB_MAX_IDLE_CONNS":    "200",
		"DB_CONN_MAX_LIFETIME": "10m",
	}

	return func(key string) string {
		if key == failureKey {
			return value
		}

		return envs[key]
	}
}

func TestParse(t *testing.T) {
	t.Parallel()

	t.Run("parse config", func(t *testing.T) {
		t.Parallel()

		envs := map[string]string{
			"APP_ENV":              "test",
			"HOST":                 "localhost",
			"PORT":                 "5432",
			"DB_URI":               "postgres://localhost:5432/topbanana?sslmode=disable",
			"DB_MAX_OPEN_CONNS":    "100",
			"DB_MAX_IDLE_CONNS":    "200",
			"DB_CONN_MAX_LIFETIME": "10m",
		}

		getenv := func(key string) string {
			return envs[key]
		}
		c, err := Parse(getenv)
		if err != nil {
			t.Fatalf("error parsing config: %v", err)
		}
		if c.AppEnvironment != envs["APP_ENV"] {
			t.Errorf("got %v, want %v", c.AppEnvironment, envs["APP_ENV"])
		}
		if c.Host != envs["HOST"] {
			t.Errorf("got %v, want %v", c.Host, envs["HOST"])
		}
		if c.Port != envs["PORT"] {
			t.Errorf("got %v, want %v", c.Port, envs["PORT"])
		}
		if c.DBURI != envs["DB_URI"] {
			t.Errorf("got %v, want %v", c.DBURI, envs["DB_URI"])
		}
	})

	t.Run("fallback values", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			key    string
			wantFn func(c Config) bool
		}{
			{
				name:   "fallback App Environment",
				key:    "APP_ENV",
				wantFn: func(c Config) bool { return c.AppEnvironment == AppEnvironmentDefault },
			},
			{
				name:   "fallback Host",
				key:    "HOST",
				wantFn: func(c Config) bool { return c.Host == HostDefault },
			},
			{
				name:   "fallback Port",
				key:    "PORT",
				wantFn: func(c Config) bool { return c.Port == PortDefault },
			},
			{
				name:   "fallback DB URI",
				key:    "DB_URI",
				wantFn: func(c Config) bool { return c.DBURI == DBURIDefault },
			},
			{
				name:   "fallback Max Open Connections",
				key:    "DB_MAX_OPEN_CONNS",
				wantFn: func(c Config) bool { return c.DBMaxOpenConns == DBMaxOpenConnsDefault },
			},
			{
				name:   "fallback Max Idle Connections",
				key:    "DB_MAX_IDLE_CONNS",
				wantFn: func(c Config) bool { return c.DBMaxIdleConns == DBMaxIdleConnsDefault },
			},
			{
				name:   "fallback Connection Max Connection Lifetime",
				key:    "DB_CONN_MAX_LIFETIME",
				wantFn: func(c Config) bool { return c.DBConnMaxLifetime == DBConnMaxLifetimeDefault },
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				getenv := getenvFailure(tt.key, "")
				c, err := Parse(getenv)
				if err != nil {
					t.Fatalf("error parsing config: %v", err)
				}
				if want := tt.wantFn(*c); !want {
					t.Errorf("got %v, want %v", c, want)
				}
			})
		}
	})

	t.Run("invalid values", func(t *testing.T) {
		tests := []struct {
			name   string
			getenv func(string) string
		}{
			{"DB_MAX_OPEN_CONNS is not an int", getenvFailure("DB_MAX_OPEN_CONNS", "One")},
			{"DB_MAX_IDLE_CONNS is not an int", getenvFailure("DB_MAX_IDLE_CONNS", "Two")},
			{"DB_CONN_MAX_LIFETIME is not a duration", getenvFailure("DB_CONN_MAX_LIFETIME", "Three")},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := Parse(tt.getenv)
				if err == nil {
					t.Fatal("expected error parsing config")
				}
			})
		}
	})

	t.Run("empty DB URI in production", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			envs := map[string]string{
				"APP_ENV":              "production", // Notice: testing for production
				"HOST":                 "localhost",
				"PORT":                 "5432",
				"DB_URI":               "", // Notice: empty DB URI
				"DB_MAX_OPEN_CONNS":    "100",
				"DB_MAX_IDLE_CONNS":    "200",
				"DB_CONN_MAX_LIFETIME": "10m",
			}

			return envs[key]
		}

		_, err := Parse(getenv)
		if err == nil {
			t.Fatal("expected error parsing config")
		}
		if got, want := err, ErrDBUriNotSetInProduction; !errors.Is(got, want) {
			t.Fatalf("got error %v, want %v", got, want)
		}
	})

	t.Run("client dir in production", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			envs := map[string]string{
				"APP_ENV":    "production",
				"CLIENT_DIR": "should/be/overridden",
				"DB_URI":     "file:test.sqlite",
			}

			return envs[key]
		}

		c, err := Parse(getenv)
		if err != nil {
			t.Fatalf("error parsing config: %v", err)
		}
		if c.ClientDir != "" {
			t.Errorf("got %q, want empty string for production default", c.ClientDir)
		}
	})
}

func TestParse_ErrorHandling(t *testing.T) {
	t.Parallel()
}
