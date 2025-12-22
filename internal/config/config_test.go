package config_test

import (
	"errors"
	"testing"

	"github.com/starquake/topbanana/internal/config"
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

		getenv := func(key string) string {
			envs := map[string]string{
				"APP_ENV":              "test",
				"HOST":                 "localhost",
				"PORT":                 "5432",
				"DB_URI":               "postgres://localhost:5432/topbanana?sslmode=disable",
				"DB_MAX_OPEN_CONNS":    "100",
				"DB_MAX_IDLE_CONNS":    "200",
				"DB_CONN_MAX_LIFETIME": "10m",
			}

			return envs[key]
		}
		_, err := config.Parse(getenv)
		if err != nil {
			t.Fatalf("error parsing config: %v", err)
		}
	})

	t.Run("fallback values", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			key    string
			wantFn func(c config.Config) bool
		}{
			{
				name:   "fallback App Environment",
				key:    "APP_ENV",
				wantFn: func(c config.Config) bool { return c.AppEnvironment == config.AppEnvironmentDefault },
			},
			{
				name:   "fallback Host",
				key:    "HOST",
				wantFn: func(c config.Config) bool { return c.Host == config.HostDefault },
			},
			{
				name:   "fallback Port",
				key:    "PORT",
				wantFn: func(c config.Config) bool { return c.Port == config.PortDefault },
			},
			{
				name:   "fallback DB URI",
				key:    "DB_URI",
				wantFn: func(c config.Config) bool { return c.DBURI == config.DBURIDefault },
			},
			{
				name:   "fallback Max Open Connections",
				key:    "DB_MAX_OPEN_CONNS",
				wantFn: func(c config.Config) bool { return c.DBMaxOpenConns == config.DBMaxOpenConnsDefault },
			},
			{
				name:   "fallback Max Idle Connections",
				key:    "DB_MAX_IDLE_CONNS",
				wantFn: func(c config.Config) bool { return c.DBMaxIdleConns == config.DBMaxIdleConnsDefault },
			},
			{
				name:   "fallback Connection Max Connection Lifetime",
				key:    "DB_CONN_MAX_LIFETIME",
				wantFn: func(c config.Config) bool { return c.DBConnMaxLifetime == config.DBConnMaxLifetimeDefault },
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				getenv := getenvFailure(tt.key, "")
				c, err := config.Parse(getenv)
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
				_, err := config.Parse(tt.getenv)
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

		_, err := config.Parse(getenv)
		if err == nil {
			t.Fatal("expected error parsing config")
		}
		if got, want := err, config.ErrDBUriNotSetInProduction; !errors.Is(got, want) {
			t.Fatalf("got error %v, want %v", got, want)
		}
	})
}

func TestParse_ErrorHandling(t *testing.T) {
	t.Parallel()
}
