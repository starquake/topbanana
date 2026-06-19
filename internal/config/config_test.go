package config_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

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
		"SESSION_KEY":          "test-session-key-test-session-key",
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
			"SESSION_KEY":          "test-session-key-test-session-key",
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
				// APP_ENV unset must NOT silently fall back to
				// "development" - that would make Secure cookies and
				// the SESSION_KEY requirement opt-in rather than the
				// fail-secure default. Parse leaves AppEnvironment as
				// the empty string instead; the Makefile defaults
				// APP_ENV=development for local dev targets so the
				// loose behaviour stays opt-in via explicit config.
				name:   "fallback App Environment is empty (fail-secure)",
				key:    "APP_ENV",
				wantFn: func(c Config) bool { return c.AppEnvironment == "" },
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
				"SESSION_KEY": "test-session-key-test-session-key",
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

	t.Run("web static dir default empty", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			envs := map[string]string{
				"APP_ENV":     "development",
				"SESSION_KEY": "test-session-key-test-session-key",
			}

			return envs[key]
		}

		c, err := Parse(getenv)
		if err != nil {
			t.Fatalf("error parsing config: %v", err)
		}
		if got, want := c.WebStaticDir, ""; got != want {
			t.Errorf("WebStaticDir = %q, want %q", got, want)
		}
	})

	t.Run("web static dir read from env in development", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			envs := map[string]string{
				"APP_ENV":        "development",
				"WEB_STATIC_DIR": "internal/assets/static",
				"SESSION_KEY":    "test-session-key-test-session-key",
			}

			return envs[key]
		}

		c, err := Parse(getenv)
		if err != nil {
			t.Fatalf("error parsing config: %v", err)
		}
		if got, want := c.WebStaticDir, "internal/assets/static"; got != want {
			t.Errorf("WebStaticDir = %q, want %q", got, want)
		}
	})

	t.Run("media dir default when unset", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			envs := map[string]string{
				"APP_ENV":     "development",
				"SESSION_KEY": "test-session-key-test-session-key",
			}

			return envs[key]
		}

		c, err := Parse(getenv)
		if err != nil {
			t.Fatalf("error parsing config: %v", err)
		}
		if got, want := c.MediaDir, MediaDirDefault; got != want {
			t.Errorf("MediaDir = %q, want %q", got, want)
		}
	})

	t.Run("media dir read from env in every environment", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			envs := map[string]string{
				"APP_ENV":     "production",
				"DB_URI":      "file:test.sqlite",
				"MEDIA_DIR":   "/srv/media",
				"SESSION_KEY": "test-session-key-test-session-key",
			}

			return envs[key]
		}

		c, err := Parse(getenv)
		if err != nil {
			t.Fatalf("error parsing config: %v", err)
		}
		if got, want := c.MediaDir, "/srv/media"; got != want {
			t.Errorf("MediaDir = %q, want %q (read in production too)", got, want)
		}
	})

	t.Run("web static dir ignored in production", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			envs := map[string]string{
				"APP_ENV":        "production",
				"WEB_STATIC_DIR": "should/be/overridden",
				"DB_URI":         "file:test.sqlite",
				"SESSION_KEY":    "test-session-key-test-session-key",
			}

			return envs[key]
		}

		c, err := Parse(getenv)
		if err != nil {
			t.Fatalf("error parsing config: %v", err)
		}
		if got, want := c.WebStaticDir, ""; got != want {
			t.Errorf("WebStaticDir = %q, want %q (production must ignore the env var)", got, want)
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
		if got, want := err, ErrSessionKeyRequired; !errors.Is(got, want) {
			t.Fatalf("Parse() err = %v, want %v", got, want)
		}
	})

	t.Run("short SESSION_KEY is rejected", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			envs := map[string]string{
				"APP_ENV":     "production",
				"DB_URI":      "file:test.sqlite",
				"SESSION_KEY": "too-short",
			}

			return envs[key]
		}

		_, err := Parse(getenv)
		if got, want := err, ErrSessionKeyTooShort; !errors.Is(got, want) {
			t.Fatalf("Parse() err = %v, want %v", got, want)
		}
	})

	t.Run("minimum-length SESSION_KEY is accepted", func(t *testing.T) {
		t.Parallel()

		// 32 ASCII bytes clears the minimum the resolver enforces.
		key := strings.Repeat("a", 32)
		getenv := func(k string) string {
			envs := map[string]string{
				"APP_ENV":     "production",
				"DB_URI":      "file:test.sqlite",
				"SESSION_KEY": key,
			}

			return envs[k]
		}

		c, err := Parse(getenv)
		if err != nil {
			t.Fatalf("Parse() err = %v, want nil", err)
		}
		if got, want := c.SessionKey, key; got != want {
			t.Errorf("Parse() SessionKey = %q, want %q", got, want)
		}
	})

	t.Run("empty SESSION_KEY in staging requires explicit key", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			envs := map[string]string{
				"APP_ENV":     "staging",
				"DB_URI":      "file:test.sqlite",
				"SESSION_KEY": "",
			}

			return envs[key]
		}

		_, err := Parse(getenv)
		if err == nil {
			t.Fatal("Parse() with empty SESSION_KEY in staging: err = nil, want non-nil")
		}
		if got, want := err, ErrSessionKeyRequired; !errors.Is(got, want) {
			t.Fatalf("Parse() err = %v, want %v", got, want)
		}
		if got, want := err.Error(), `APP_ENV="staging"`; !strings.Contains(got, want) {
			t.Errorf("Parse() err.Error() = %q, should contain %q", got, want)
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
					if key == "APP_ENV" {
						return "development"
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

func TestParse_RevealDelay(t *testing.T) {
	t.Parallel()

	t.Run("valid values", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			value string
			want  time.Duration
		}{
			{"unset defaults to zero", "", 0},
			{"explicit zero parses", "0s", 0},
			{"500ms parses", "500ms", 500 * time.Millisecond},
			{"3s parses", "3s", 3 * time.Second},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				getenv := func(key string) string {
					if key == "REVEAL_DELAY" {
						return tt.value
					}
					if key == "APP_ENV" {
						return "development"
					}

					return ""
				}

				c, err := Parse(getenv)
				if err != nil {
					t.Fatalf("Parse() err = %v, want nil", err)
				}
				if got, want := c.RevealDelay, tt.want; got != want {
					t.Errorf("RevealDelay = %v, want %v", got, want)
				}
			})
		}
	})

	t.Run("unparseable value returns error", func(t *testing.T) {
		t.Parallel()

		_, err := Parse(getenvFailure("REVEAL_DELAY", "fast"))
		if err == nil {
			t.Fatal("Parse() with invalid REVEAL_DELAY: err = nil, want non-nil")
		}
		if got, want := err.Error(), "invalid REVEAL_DELAY"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("negative value returns error", func(t *testing.T) {
		t.Parallel()

		_, err := Parse(getenvFailure("REVEAL_DELAY", "-1s"))
		if got, want := err, ErrRevealDelayNegative; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestParse_SessionRunnerBeat(t *testing.T) {
	t.Parallel()

	t.Run("valid values", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			value string
			want  time.Duration
		}{
			{"unset defaults to zero", "", 0},
			{"explicit zero parses", "0s", 0},
			{"50ms parses", "50ms", 50 * time.Millisecond},
			{"4s parses", "4s", 4 * time.Second},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				getenv := func(key string) string {
					if key == "SESSION_RUNNER_BEAT" {
						return tt.value
					}
					if key == "APP_ENV" {
						return "development"
					}

					return ""
				}

				c, err := Parse(getenv)
				if err != nil {
					t.Fatalf("Parse() err = %v, want nil", err)
				}
				if got, want := c.SessionRunnerBeat, tt.want; got != want {
					t.Errorf("SessionRunnerBeat = %v, want %v", got, want)
				}
			})
		}
	})

	t.Run("unparseable value returns error", func(t *testing.T) {
		t.Parallel()

		_, err := Parse(getenvFailure("SESSION_RUNNER_BEAT", "snappy"))
		if err == nil {
			t.Fatal("Parse() with invalid SESSION_RUNNER_BEAT: err = nil, want non-nil")
		}
		if got, want := err.Error(), "invalid SESSION_RUNNER_BEAT"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("negative value returns error", func(t *testing.T) {
		t.Parallel()

		_, err := Parse(getenvFailure("SESSION_RUNNER_BEAT", "-1s"))
		if got, want := err, ErrSessionRunnerBeatNegative; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestParse_SessionRoundIntroBeat(t *testing.T) {
	t.Parallel()

	t.Run("valid values", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			value string
			want  time.Duration
		}{
			{"unset defaults to zero", "", 0},
			{"explicit zero parses", "0s", 0},
			{"2s parses", "2s", 2 * time.Second},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				getenv := func(key string) string {
					if key == "SESSION_ROUND_INTRO_BEAT" {
						return tt.value
					}
					if key == "APP_ENV" {
						return "development"
					}

					return ""
				}

				c, err := Parse(getenv)
				if err != nil {
					t.Fatalf("Parse() err = %v, want nil", err)
				}
				if got, want := c.SessionRoundIntroBeat, tt.want; got != want {
					t.Errorf("SessionRoundIntroBeat = %v, want %v", got, want)
				}
			})
		}
	})

	t.Run("unparseable value returns error", func(t *testing.T) {
		t.Parallel()

		_, err := Parse(getenvFailure("SESSION_ROUND_INTRO_BEAT", "snappy"))
		if err == nil {
			t.Fatal("Parse() with invalid SESSION_ROUND_INTRO_BEAT: err = nil, want non-nil")
		}
		if got, want := err.Error(), "invalid SESSION_ROUND_INTRO_BEAT"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("negative value returns error", func(t *testing.T) {
		t.Parallel()

		_, err := Parse(getenvFailure("SESSION_ROUND_INTRO_BEAT", "-1s"))
		if got, want := err, ErrSessionRoundIntroBeatNegative; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestParse_SessionStartCountdown(t *testing.T) {
	t.Parallel()

	t.Run("valid values", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			value string
			want  time.Duration
		}{
			{"unset defaults to zero", "", 0},
			{"explicit zero parses", "0s", 0},
			{"2s parses", "2s", 2 * time.Second},
			{"60s parses", "60s", 60 * time.Second},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				getenv := func(key string) string {
					if key == "SESSION_START_COUNTDOWN" {
						return tt.value
					}
					if key == "APP_ENV" {
						return "development"
					}

					return ""
				}

				c, err := Parse(getenv)
				if err != nil {
					t.Fatalf("Parse() err = %v, want nil", err)
				}
				if got, want := c.SessionStartCountdown, tt.want; got != want {
					t.Errorf("SessionStartCountdown = %v, want %v", got, want)
				}
			})
		}
	})

	t.Run("unparseable value returns error", func(t *testing.T) {
		t.Parallel()

		_, err := Parse(getenvFailure("SESSION_START_COUNTDOWN", "soon"))
		if err == nil {
			t.Fatal("Parse() with invalid SESSION_START_COUNTDOWN: err = nil, want non-nil")
		}
		if got, want := err.Error(), "invalid SESSION_START_COUNTDOWN"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("negative value returns error", func(t *testing.T) {
		t.Parallel()

		_, err := Parse(getenvFailure("SESSION_START_COUNTDOWN", "-1s"))
		if got, want := err, ErrSessionStartCountdownNegative; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestParse_SessionIdleClose(t *testing.T) {
	t.Parallel()

	t.Run("valid values", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			value string
			want  time.Duration
		}{
			{"unset defaults to zero", "", 0},
			{"explicit zero parses", "0s", 0},
			{"5s parses", "5s", 5 * time.Second},
			{"30m parses", "30m", 30 * time.Minute},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				getenv := func(key string) string {
					if key == "SESSION_IDLE_CLOSE" {
						return tt.value
					}
					if key == "APP_ENV" {
						return "development"
					}

					return ""
				}

				c, err := Parse(getenv)
				if err != nil {
					t.Fatalf("Parse() err = %v, want nil", err)
				}
				if got, want := c.SessionIdleClose, tt.want; got != want {
					t.Errorf("SessionIdleClose = %v, want %v", got, want)
				}
			})
		}
	})

	t.Run("unparseable value returns error", func(t *testing.T) {
		t.Parallel()

		_, err := Parse(getenvFailure("SESSION_IDLE_CLOSE", "soonish"))
		if err == nil {
			t.Fatal("Parse() with invalid SESSION_IDLE_CLOSE: err = nil, want non-nil")
		}
		if got, want := err.Error(), "invalid SESSION_IDLE_CLOSE"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("negative value returns error", func(t *testing.T) {
		t.Parallel()

		_, err := Parse(getenvFailure("SESSION_IDLE_CLOSE", "-1s"))
		if got, want := err, ErrSessionIdleCloseNegative; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestParse_LoginCooldown(t *testing.T) {
	t.Parallel()

	t.Run("valid values", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			value string
			want  time.Duration
		}{
			{"unset defaults to 3s", "", LoginCooldownDefault},
			{"explicit zero parses", "0s", 0},
			{"500ms parses", "500ms", 500 * time.Millisecond},
			{"5s parses", "5s", 5 * time.Second},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				getenv := func(key string) string {
					if key == "LOGIN_COOLDOWN" {
						return tt.value
					}
					if key == "APP_ENV" {
						return "development"
					}

					return ""
				}

				c, err := Parse(getenv)
				if err != nil {
					t.Fatalf("Parse() err = %v, want nil", err)
				}
				if got, want := c.LoginCooldown, tt.want; got != want {
					t.Errorf("LoginCooldown = %v, want %v", got, want)
				}
			})
		}
	})

	t.Run("unparseable value returns error", func(t *testing.T) {
		t.Parallel()

		_, err := Parse(getenvFailure("LOGIN_COOLDOWN", "slow"))
		if err == nil {
			t.Fatal("Parse() with invalid LOGIN_COOLDOWN: err = nil, want non-nil")
		}
		if got, want := err.Error(), "invalid LOGIN_COOLDOWN"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("negative value returns error", func(t *testing.T) {
		t.Parallel()

		_, err := Parse(getenvFailure("LOGIN_COOLDOWN", "-1s"))
		if got, want := err, ErrLoginCooldownNegative; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestParse_MediaAudioMaxBytes(t *testing.T) {
	t.Parallel()

	t.Run("valid values", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			value string
			want  int64
		}{
			{"unset defaults", "", MediaAudioMaxBytesDefault},
			{"explicit zero disables", "0", 0},
			{"parses a value", "1048576", 1048576},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				getenv := func(key string) string {
					if key == "MEDIA_AUDIO_MAX_BYTES" {
						return tt.value
					}
					if key == "APP_ENV" {
						return "development"
					}

					return ""
				}

				c, err := Parse(getenv)
				if err != nil {
					t.Fatalf("Parse() err = %v, want nil", err)
				}
				if got, want := c.MediaAudioMaxBytes, tt.want; got != want {
					t.Errorf("MediaAudioMaxBytes = %d, want %d", got, want)
				}
			})
		}
	})

	t.Run("unparseable value returns error", func(t *testing.T) {
		t.Parallel()

		_, err := Parse(getenvFailure("MEDIA_AUDIO_MAX_BYTES", "huge"))
		if err == nil {
			t.Fatal("Parse() with invalid MEDIA_AUDIO_MAX_BYTES: err = nil, want non-nil")
		}
		if got, want := err.Error(), "invalid MEDIA_AUDIO_MAX_BYTES"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("negative value returns error", func(t *testing.T) {
		t.Parallel()

		_, err := Parse(getenvFailure("MEDIA_AUDIO_MAX_BYTES", "-1"))
		if got, want := err, ErrMediaAudioMaxBytesNegative; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestParse_MediaImageMaxBytes(t *testing.T) {
	t.Parallel()

	t.Run("valid values", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			value string
			want  int64
		}{
			{"unset defaults", "", MediaImageMaxBytesDefault},
			{"explicit zero disables", "0", 0},
			{"parses a value", "1048576", 1048576},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				getenv := func(key string) string {
					if key == "MEDIA_IMAGE_MAX_BYTES" {
						return tt.value
					}
					if key == "APP_ENV" {
						return "development"
					}

					return ""
				}

				c, err := Parse(getenv)
				if err != nil {
					t.Fatalf("Parse() err = %v, want nil", err)
				}
				if got, want := c.MediaImageMaxBytes, tt.want; got != want {
					t.Errorf("MediaImageMaxBytes = %d, want %d", got, want)
				}
			})
		}
	})

	t.Run("unparseable value returns error", func(t *testing.T) {
		t.Parallel()

		_, err := Parse(getenvFailure("MEDIA_IMAGE_MAX_BYTES", "huge"))
		if err == nil {
			t.Fatal("Parse() with invalid MEDIA_IMAGE_MAX_BYTES: err = nil, want non-nil")
		}
		if got, want := err.Error(), "invalid MEDIA_IMAGE_MAX_BYTES"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("negative value returns error", func(t *testing.T) {
		t.Parallel()

		_, err := Parse(getenvFailure("MEDIA_IMAGE_MAX_BYTES", "-1"))
		if got, want := err, ErrMediaImageMaxBytesNegative; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestParse_AdminEmails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  []string
	}{
		{"unset defaults to nil", "", nil},
		{"single email", "alice@example.test", []string{"alice@example.test"}},
		{
			"comma separated",
			"alice@example.test,bob@example.test,carol@example.test",
			[]string{"alice@example.test", "bob@example.test", "carol@example.test"},
		},
		{
			"trims whitespace and lowercases",
			"  Alice@Example.test ,BOB@example.test  , carol@example.test",
			[]string{"alice@example.test", "bob@example.test", "carol@example.test"},
		},
		{
			"drops empty entries",
			"alice@example.test,,bob@example.test, ,carol@example.test",
			[]string{"alice@example.test", "bob@example.test", "carol@example.test"},
		},
		{"only commas yields nil", ", ,", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			getenv := func(key string) string {
				if key == "ADMIN_EMAILS" {
					return tt.value
				}
				if key == "APP_ENV" {
					return "development"
				}

				return ""
			}

			c, err := Parse(getenv)
			if err != nil {
				t.Fatalf("Parse() err = %v, want nil", err)
			}
			if got, want := c.AdminEmails, tt.want; !slices.Equal(got, want) {
				t.Errorf("AdminEmails = %v, want %v", got, want)
			}
		})
	}
}

func TestParse_GoogleOAuth(t *testing.T) {
	t.Parallel()

	t.Run("all three vars wire through", func(t *testing.T) {
		t.Parallel()

		envs := map[string]string{
			"APP_ENV":              "development",
			"GOOGLE_CLIENT_ID":     "id-123",
			"GOOGLE_CLIENT_SECRET": "secret-abc",
			"GOOGLE_REDIRECT_URL":  "https://example.test/login/google/callback",
			"GOOGLE_ISSUER_URL":    "https://example.test",
		}
		getenv := func(key string) string { return envs[key] }
		c, err := Parse(getenv)
		if err != nil {
			t.Fatalf("Parse() err = %v, want nil", err)
		}
		if got, want := c.GoogleClientID, envs["GOOGLE_CLIENT_ID"]; got != want {
			t.Errorf("GoogleClientID = %q, want %q", got, want)
		}
		if got, want := c.GoogleClientSecret, envs["GOOGLE_CLIENT_SECRET"]; got != want {
			t.Errorf("GoogleClientSecret = %q, want %q", got, want)
		}
		if got, want := c.GoogleRedirectURL, envs["GOOGLE_REDIRECT_URL"]; got != want {
			t.Errorf("GoogleRedirectURL = %q, want %q", got, want)
		}
		if got, want := c.GoogleIssuerURL, envs["GOOGLE_ISSUER_URL"]; got != want {
			t.Errorf("GoogleIssuerURL = %q, want %q", got, want)
		}
	})
}

func TestParseDatabase(t *testing.T) {
	t.Parallel()

	t.Run("DB_URI unset outside production falls back to default", func(t *testing.T) {
		t.Parallel()

		getenv := func(string) string { return "" }
		dbc, err := ParseDatabase(getenv)
		if err != nil {
			t.Fatalf("ParseDatabase err = %v, want nil", err)
		}
		if got, want := dbc.URI, DBURIDefault; got != want {
			t.Errorf("URI = %q, want %q", got, want)
		}
	})

	t.Run("DB_URI unset in production returns ErrDBURINotSetInProduction", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			if key == "APP_ENV" {
				return "production"
			}

			return ""
		}
		_, err := ParseDatabase(getenv)
		if got, want := err, ErrDBURINotSetInProduction; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("explicit DB_URI is honored", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			if key == "DB_URI" {
				return "file:override.sqlite"
			}

			return ""
		}
		dbc, err := ParseDatabase(getenv)
		if err != nil {
			t.Fatalf("ParseDatabase err = %v, want nil", err)
		}
		if got, want := dbc.URI, "file:override.sqlite"; got != want {
			t.Errorf("URI = %q, want %q", got, want)
		}
	})

	t.Run("driver and pool defaults", func(t *testing.T) {
		t.Parallel()

		getenv := func(string) string { return "" }
		dbc, err := ParseDatabase(getenv)
		if err != nil {
			t.Fatalf("ParseDatabase err = %v, want nil", err)
		}
		if got, want := dbc.Driver, DBDriverDefault; got != want {
			t.Errorf("Driver = %q, want %q", got, want)
		}
		if got, want := dbc.MaxOpenConns, DBMaxOpenConnsDefault; got != want {
			t.Errorf("MaxOpenConns = %d, want %d", got, want)
		}
		if got, want := dbc.MaxIdleConns, DBMaxIdleConnsDefault; got != want {
			t.Errorf("MaxIdleConns = %d, want %d", got, want)
		}
		if got, want := dbc.ConnMaxLifetime, DBConnMaxLifetimeDefault; got != want {
			t.Errorf("ConnMaxLifetime = %v, want %v", got, want)
		}
	})

	t.Run("pool env overrides are honored", func(t *testing.T) {
		t.Parallel()

		envs := map[string]string{
			"DB_MAX_OPEN_CONNS":    "100",
			"DB_MAX_IDLE_CONNS":    "200",
			"DB_CONN_MAX_LIFETIME": "10m",
		}
		getenv := func(key string) string { return envs[key] }
		dbc, err := ParseDatabase(getenv)
		if err != nil {
			t.Fatalf("ParseDatabase err = %v, want nil", err)
		}
		if got, want := dbc.MaxOpenConns, 100; got != want {
			t.Errorf("MaxOpenConns = %d, want %d", got, want)
		}
		if got, want := dbc.MaxIdleConns, 200; got != want {
			t.Errorf("MaxIdleConns = %d, want %d", got, want)
		}
		if got, want := dbc.ConnMaxLifetime, 10*time.Minute; got != want {
			t.Errorf("ConnMaxLifetime = %v, want %v", got, want)
		}
	})

	t.Run("invalid pool override returns error", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			if key == "DB_MAX_OPEN_CONNS" {
				return "lots"
			}

			return ""
		}
		_, err := ParseDatabase(getenv)
		if err == nil {
			t.Fatal("ParseDatabase err = nil, want non-nil")
		}
		if got, want := err.Error(), "invalid DB_MAX_OPEN_CONNS"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("does not require SESSION_KEY outside development", func(t *testing.T) {
		t.Parallel()

		// The whole point: a half-configured / locked-out environment
		// (production-like APP_ENV, no SESSION_KEY) must still resolve the
		// DB so the break-glass tools can run. Parse would reject this with
		// ErrSessionKeyRequired; ParseDatabase must not.
		envs := map[string]string{
			"APP_ENV": "production",
			"DB_URI":  "file:test.sqlite",
		}
		getenv := func(key string) string { return envs[key] }
		dbc, err := ParseDatabase(getenv)
		if err != nil {
			t.Fatalf("ParseDatabase err = %v, want nil (no SESSION_KEY needed)", err)
		}
		if got, want := dbc.URI, "file:test.sqlite"; got != want {
			t.Errorf("URI = %q, want %q", got, want)
		}
	})
}

// TestConfig_DatabaseConfig pins that the full-server config and the
// break-glass tools resolve the same DB values: Parse's result projected
// through DatabaseConfig() must match ParseDatabase on the same env.
func TestConfig_DatabaseConfig(t *testing.T) {
	t.Parallel()

	envs := map[string]string{
		"APP_ENV":              "test",
		"DB_URI":               "file:shared.sqlite",
		"DB_MAX_OPEN_CONNS":    "42",
		"DB_MAX_IDLE_CONNS":    "7",
		"DB_CONN_MAX_LIFETIME": "90s",
		"SESSION_KEY":          "test-session-key-test-session-key",
	}
	getenv := func(key string) string { return envs[key] }

	c, err := Parse(getenv)
	if err != nil {
		t.Fatalf("Parse err = %v, want nil", err)
	}
	dbc, err := ParseDatabase(getenv)
	if err != nil {
		t.Fatalf("ParseDatabase err = %v, want nil", err)
	}
	if got, want := c.DatabaseConfig(), dbc; got != want {
		t.Errorf("c.DatabaseConfig() = %+v, want %+v (Parse and ParseDatabase must agree)", got, want)
	}
}

func TestConfig_GoogleLoginEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		clientID     string
		clientSecret string
		redirectURL  string
		want         bool
	}{
		{name: "all set", clientID: "id", clientSecret: "secret", redirectURL: "url", want: true},
		{name: "no vars", want: false},
		{name: "missing client id", clientSecret: "secret", redirectURL: "url", want: false},
		{name: "missing client secret", clientID: "id", redirectURL: "url", want: false},
		{name: "missing redirect url", clientID: "id", clientSecret: "secret", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &Config{
				GoogleClientID:     tt.clientID,
				GoogleClientSecret: tt.clientSecret,
				GoogleRedirectURL:  tt.redirectURL,
			}
			if got, want := c.GoogleLoginEnabled(), tt.want; got != want {
				t.Errorf("GoogleLoginEnabled() = %v, want %v", got, want)
			}
		})
	}
}

func TestParse_SMTP(t *testing.T) {
	t.Parallel()

	t.Run("unset SMTP block leaves SMTPConfigured false", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			if key == "APP_ENV" {
				return "development"
			}

			return ""
		}
		c, err := Parse(getenv)
		if err != nil {
			t.Fatalf("Parse() err = %v, want nil", err)
		}
		if got, want := c.SMTPConfigured(), false; got != want {
			t.Errorf("SMTPConfigured() = %v, want %v", got, want)
		}
		if got, want := c.SMTPHost, ""; got != want {
			t.Errorf("SMTPHost = %q, want %q", got, want)
		}
		if got, want := c.SMTPPort, 0; got != want {
			t.Errorf("SMTPPort = %d, want %d", got, want)
		}
		// SMTP_TLS defaults to true so production deploys ship
		// STARTTLS-on by default; dev opts out with SMTP_TLS=false.
		if got, want := c.SMTPTLS, true; got != want {
			t.Errorf("SMTPTLS = %v, want %v", got, want)
		}
	})

	t.Run("full SMTP block wires through and SMTPConfigured flips on", func(t *testing.T) {
		t.Parallel()

		envs := map[string]string{
			"APP_ENV":       "development",
			"SMTP_HOST":     "smtp.example.test",
			"SMTP_PORT":     "587",
			"SMTP_USERNAME": "smtpuser",
			"SMTP_PASSWORD": "smtpsecret",
			"SMTP_FROM":     "topbanana@localhost",
			// Credentials require TLS; PLAIN auth over cleartext is
			// refused at parse (see the cleartext-auth case below).
			"SMTP_TLS": "true",
			"BASE_URL": "https://quiz.example.test/",
		}
		getenv := func(key string) string { return envs[key] }
		c, err := Parse(getenv)
		if err != nil {
			t.Fatalf("Parse() err = %v, want nil", err)
		}
		if got, want := c.SMTPHost, "smtp.example.test"; got != want {
			t.Errorf("SMTPHost = %q, want %q", got, want)
		}
		if got, want := c.SMTPPort, 587; got != want {
			t.Errorf("SMTPPort = %d, want %d", got, want)
		}
		if got, want := c.SMTPUsername, "smtpuser"; got != want {
			t.Errorf("SMTPUsername = %q, want %q", got, want)
		}
		if got, want := c.SMTPPassword, "smtpsecret"; got != want {
			t.Errorf("SMTPPassword = %q, want %q", got, want)
		}
		if got, want := c.SMTPFrom, "topbanana@localhost"; got != want {
			t.Errorf("SMTPFrom = %q, want %q", got, want)
		}
		if got, want := c.SMTPTLS, true; got != want {
			t.Errorf("SMTPTLS = %v, want %v", got, want)
		}
		// BaseURL trims a trailing slash so callers can blindly
		// concatenate path components without ending up with "//".
		if got, want := c.BaseURL, "https://quiz.example.test"; got != want {
			t.Errorf("BaseURL = %q, want %q", got, want)
		}
		if got, want := c.SMTPConfigured(), true; got != want {
			t.Errorf("SMTPConfigured() = %v, want %v", got, want)
		}
	})

	t.Run("partial SMTP block returns ErrSMTPConfigIncomplete", func(t *testing.T) {
		t.Parallel()

		// Host + from but no port: the mailer can't dial, so refuse
		// at startup instead of booting a half-wired mailer.
		envs := map[string]string{
			"APP_ENV":   "development",
			"SMTP_HOST": "mailpit",
			"SMTP_FROM": "topbanana@localhost",
		}
		getenv := func(key string) string { return envs[key] }
		_, err := Parse(getenv)
		if err == nil {
			t.Fatal("Parse() err = nil, want non-nil")
		}
		if got, want := err, ErrSMTPConfigIncomplete; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("invalid SMTP_PORT returns ErrSMTPPortInvalid", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			value string
		}{
			{"non-numeric", "not-a-port"},
			{"zero", "0"},
			{"negative", "-1"},
			{"above 65535", "65536"},
			{"way out of range", "99999"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				envs := map[string]string{
					"APP_ENV":   "development",
					"SMTP_HOST": "mailpit",
					"SMTP_PORT": tt.value,
					"SMTP_FROM": "topbanana@localhost",
				}
				getenv := func(key string) string { return envs[key] }
				_, err := Parse(getenv)
				if err == nil {
					t.Fatal("Parse() err = nil, want non-nil")
				}
				if got, want := err, ErrSMTPPortInvalid; !errors.Is(got, want) {
					t.Errorf("err = %v, want %v", got, want)
				}
			})
		}
	})

	t.Run("SMTP_USERNAME without SMTP_PASSWORD returns ErrSMTPAuthIncomplete", func(t *testing.T) {
		t.Parallel()

		envs := map[string]string{
			"APP_ENV":       "development",
			"SMTP_HOST":     "mailpit",
			"SMTP_PORT":     "1025",
			"SMTP_FROM":     "topbanana@localhost",
			"SMTP_USERNAME": "smtpuser",
		}
		getenv := func(key string) string { return envs[key] }
		_, err := Parse(getenv)
		if err == nil {
			t.Fatal("Parse() err = nil, want non-nil")
		}
		if got, want := err, ErrSMTPAuthIncomplete; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("SMTP_PASSWORD without SMTP_USERNAME returns ErrSMTPAuthIncomplete", func(t *testing.T) {
		t.Parallel()

		envs := map[string]string{
			"APP_ENV":       "development",
			"SMTP_HOST":     "mailpit",
			"SMTP_PORT":     "1025",
			"SMTP_FROM":     "topbanana@localhost",
			"SMTP_PASSWORD": "smtpsecret",
		}
		getenv := func(key string) string { return envs[key] }
		_, err := Parse(getenv)
		if err == nil {
			t.Fatal("Parse() err = nil, want non-nil")
		}
		if got, want := err, ErrSMTPAuthIncomplete; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("SMTP credentials with SMTP_TLS=false returns ErrSMTPAuthOverCleartext", func(t *testing.T) {
		t.Parallel()

		envs := map[string]string{
			"APP_ENV":       "development",
			"SMTP_HOST":     "smtp.example.test",
			"SMTP_PORT":     "587",
			"SMTP_FROM":     "topbanana@localhost",
			"SMTP_USERNAME": "smtpuser",
			"SMTP_PASSWORD": "smtpsecret",
			"SMTP_TLS":      "false",
		}
		getenv := func(key string) string { return envs[key] }
		_, err := Parse(getenv)
		if err == nil {
			t.Fatal("Parse() err = nil, want non-nil")
		}
		if got, want := err, ErrSMTPAuthOverCleartext; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("Mailpit local block (SMTP_TLS=false, no auth) is allowed", func(t *testing.T) {
		t.Parallel()

		envs := map[string]string{
			"APP_ENV":   "development",
			"SMTP_HOST": "mailpit",
			"SMTP_PORT": "1025",
			"SMTP_FROM": "topbanana@localhost",
			"SMTP_TLS":  "false",
		}
		getenv := func(key string) string { return envs[key] }
		c, err := Parse(getenv)
		if err != nil {
			t.Fatalf("Parse() err = %v, want nil", err)
		}
		if got, want := c.SMTPTLS, false; got != want {
			t.Errorf("SMTPTLS = %v, want %v", got, want)
		}
		if got, want := c.SMTPConfigured(), true; got != want {
			t.Errorf("SMTPConfigured() = %v, want %v", got, want)
		}
	})

	t.Run("invalid SMTP_TLS returns wrapped error", func(t *testing.T) {
		t.Parallel()

		envs := map[string]string{
			"APP_ENV":  "development",
			"SMTP_TLS": "maybe",
		}
		getenv := func(key string) string { return envs[key] }
		_, err := Parse(getenv)
		if err == nil {
			t.Fatal("Parse() err = nil, want non-nil")
		}
		if got, want := err.Error(), "invalid SMTP_TLS"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("lone SMTP_TLS returns ErrSMTPConfigIncomplete", func(t *testing.T) {
		t.Parallel()

		// SMTP_TLS set in isolation is a typo / partial-rollout signal:
		// the operator clearly intended to wire SMTP but never finished
		// the host/port/from triple. allEmpty must therefore treat an
		// explicit SMTP_TLS as "populated subset" rather than slipping
		// past as a no-op boot.
		tests := []struct {
			name  string
			value string
		}{
			{"false", "false"},
			{"true", "true"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				envs := map[string]string{
					"APP_ENV":  "development",
					"SMTP_TLS": tt.value,
				}
				getenv := func(key string) string { return envs[key] }
				_, err := Parse(getenv)
				if err == nil {
					t.Fatal("Parse() err = nil, want non-nil")
				}
				if got, want := err, ErrSMTPConfigIncomplete; !errors.Is(got, want) {
					t.Errorf("err = %v, want %v", got, want)
				}
			})
		}
	})
}

func TestParse_TrustedProxyCIDRs(t *testing.T) {
	t.Parallel()

	t.Run("unset returns nil slice", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			if key == "APP_ENV" {
				return "development"
			}

			return ""
		}
		c, err := Parse(getenv)
		if err != nil {
			t.Fatalf("Parse() err = %v, want nil", err)
		}
		if got := c.TrustedProxyCIDRs; got != nil {
			t.Errorf("TrustedProxyCIDRs = %v, want nil", got)
		}
	})

	t.Run("single CIDR parses", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			envs := map[string]string{
				"APP_ENV":           "development",
				"TRUSTED_PROXY_IPS": "127.0.0.1/32",
				"SESSION_KEY":       "test-session-key-test-session-key",
			}

			return envs[key]
		}
		c, err := Parse(getenv)
		if err != nil {
			t.Fatalf("Parse() err = %v, want nil", err)
		}
		if got, want := len(c.TrustedProxyCIDRs), 1; got != want {
			t.Errorf("len(TrustedProxyCIDRs) = %d, want %d", got, want)
		}
	})

	t.Run("invalid CIDR returns wrapped error", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			envs := map[string]string{
				"APP_ENV":           "development",
				"TRUSTED_PROXY_IPS": "not-a-cidr",
			}

			return envs[key]
		}
		_, err := Parse(getenv)
		if err == nil {
			t.Fatal("Parse() err = nil, want non-nil")
		}
		if got, want := err.Error(), "invalid TRUSTED_PROXY_IPS"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestConfig_SecureCookies(t *testing.T) {
	t.Parallel()
	// SecureCookies decides whether session + CSRF cookies get the
	// Secure attribute. Production must, development must not - see
	// #205 for why dev drops the flag.

	tests := []struct {
		name string
		env  string
		want bool
	}{
		{name: "production", env: "production", want: true},
		{name: "development", env: "development", want: false},
		// #340: any env that isn't explicit "development" now gets Secure,
		// so a staging / demo / qa deploy doesn't issue credentials in the
		// clear if the operator forgets to set APP_ENV=production.
		{name: "staging", env: "staging", want: true},
		{name: "demo", env: "demo", want: true},
		{name: "unset", env: "", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &Config{AppEnvironment: tt.env}
			if got, want := c.SecureCookies(), tt.want; got != want {
				t.Errorf("SecureCookies() = %v, want %v (AppEnvironment=%q)", got, want, tt.env)
			}
		})
	}
}

func TestConfig_EnvTitleTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  string
		want string
	}{
		{name: "production renders empty", env: "production", want: ""},
		{name: "staging is bracketed", env: "staging", want: "[staging] "},
		{name: "development is bracketed", env: "development", want: "[development] "},
		{name: "demo is bracketed", env: "demo", want: "[demo] "},
		// Empty / unset is fail-secure: surfaces as "[unknown] " so a
		// bare-binary boot doesn't look like a production tab.
		{name: "unset renders unknown", env: "", want: "[unknown] "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &Config{AppEnvironment: tt.env}
			if got, want := c.EnvTitleTag(), tt.want; got != want {
				t.Errorf("EnvTitleTag() = %q, want %q (AppEnvironment=%q)", got, want, tt.env)
			}
		})
	}
}
