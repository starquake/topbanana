package config_test

import (
	"errors"
	"slices"
	"strings"
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
		"SESSION_KEY":          "test-session-key",
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
			"SESSION_KEY":          "test-session-key",
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
		if c.SessionKey != envs["SESSION_KEY"] {
			t.Errorf("Parse() SessionKey = %q, want %q", c.SessionKey, envs["SESSION_KEY"])
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
		if got, want := err, ErrDBURINotSetInProduction; !errors.Is(got, want) {
			t.Fatalf("got error %v, want %v", got, want)
		}
	})

	t.Run("client dir in production", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			envs := map[string]string{
				"APP_ENV":     "production",
				"CLIENT_DIR":  "should/be/overridden",
				"DB_URI":      "file:test.sqlite",
				"SESSION_KEY": "test-session-key",
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

	t.Run("empty SESSION_KEY in production", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			envs := map[string]string{
				"APP_ENV":     "production",
				"DB_URI":      "file:test.sqlite",
				"SESSION_KEY": "",
			}

			return envs[key]
		}

		_, err := Parse(getenv)
		if err == nil {
			t.Fatal("Parse() with empty SESSION_KEY in production: err = nil, want non-nil")
		}
		if got, want := err, ErrSessionKeyNotSetInProduction; !errors.Is(got, want) {
			t.Fatalf("Parse() err = %v, want %v", got, want)
		}
	})

	t.Run("empty SESSION_KEY in development generates ephemeral key", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			envs := map[string]string{
				"APP_ENV": "development",
			}

			return envs[key]
		}

		c, err := Parse(getenv)
		if err != nil {
			t.Fatalf("Parse() err = %v, want nil", err)
		}
		if c.SessionKey == "" {
			t.Fatal("Parse() SessionKey = \"\", want a non-empty ephemeral key in development")
		}

		c2, err := Parse(getenv)
		if err != nil {
			t.Fatalf("Parse() (second call) err = %v, want nil", err)
		}
		if c.SessionKey == c2.SessionKey {
			t.Errorf("Parse() SessionKey = %q on both calls, want different ephemeral keys", c.SessionKey)
		}
	})
}

func TestParse_ErrorHandling(t *testing.T) {
	t.Parallel()
}

func TestParse_RegistrationEnabled(t *testing.T) {
	t.Parallel()

	t.Run("valid values", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			value string
			want  bool
		}{
			{"unset defaults to false", "", false},
			{"true string", "true", true},
			{"false string", "false", false},
			{"numeric 1 parses as true", "1", true},
			{"numeric 0 parses as false", "0", false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				getenv := func(key string) string {
					if key == "REGISTRATION_ENABLED" {
						return tt.value
					}

					return ""
				}

				c, err := Parse(getenv)
				if err != nil {
					t.Fatalf("Parse() err = %v, want nil", err)
				}
				if got, want := c.RegistrationEnabled, tt.want; got != want {
					t.Errorf("RegistrationEnabled = %v, want %v", got, want)
				}
			})
		}
	})

	t.Run("invalid value returns error", func(t *testing.T) {
		t.Parallel()

		_, err := Parse(getenvFailure("REGISTRATION_ENABLED", "maybe"))
		if err == nil {
			t.Fatal("Parse() with invalid REGISTRATION_ENABLED: err = nil, want non-nil")
		}
		if got, want := err.Error(), "invalid REGISTRATION_ENABLED"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestParse_AdminUsernames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  []string
	}{
		{"unset defaults to nil", "", nil},
		{"single username", "alice", []string{"alice"}},
		{"comma separated", "alice,bob,carol", []string{"alice", "bob", "carol"}},
		{"trims whitespace", "  alice ,bob  , carol", []string{"alice", "bob", "carol"}},
		{"drops empty entries", "alice,,bob, ,carol", []string{"alice", "bob", "carol"}},
		{"only commas yields nil", ", ,", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			getenv := func(key string) string {
				if key == "ADMIN_USERNAMES" {
					return tt.value
				}

				return ""
			}

			c, err := Parse(getenv)
			if err != nil {
				t.Fatalf("Parse() err = %v, want nil", err)
			}
			if got, want := c.AdminUsernames, tt.want; !slices.Equal(got, want) {
				t.Errorf("AdminUsernames = %v, want %v", got, want)
			}
		})
	}
}
