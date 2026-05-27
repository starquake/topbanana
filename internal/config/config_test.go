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
				// APP_ENV unset must NOT silently fall back to
				// "development" — that would make Secure cookies and
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

	t.Run("web static dir default empty", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			envs := map[string]string{
				"APP_ENV":     "development",
				"SESSION_KEY": "test-session-key",
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
				"WEB_STATIC_DIR": "internal/web/static",
				"SESSION_KEY":    "test-session-key",
			}

			return envs[key]
		}

		c, err := Parse(getenv)
		if err != nil {
			t.Fatalf("error parsing config: %v", err)
		}
		if got, want := c.WebStaticDir, "internal/web/static"; got != want {
			t.Errorf("WebStaticDir = %q, want %q", got, want)
		}
	})

	t.Run("web static dir ignored in production", func(t *testing.T) {
		t.Parallel()

		getenv := func(key string) string {
			envs := map[string]string{
				"APP_ENV":        "production",
				"WEB_STATIC_DIR": "should/be/overridden",
				"DB_URI":         "file:test.sqlite",
				"SESSION_KEY":    "test-session-key",
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
				if key == "APP_ENV" {
					return "development"
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
			"SMTP_HOST":     "mailpit",
			"SMTP_PORT":     "1025",
			"SMTP_USERNAME": "smtpuser",
			"SMTP_PASSWORD": "smtpsecret",
			"SMTP_FROM":     "topbanana@localhost",
			"SMTP_TLS":      "false",
			"BASE_URL":      "https://quiz.example.test/",
		}
		getenv := func(key string) string { return envs[key] }
		c, err := Parse(getenv)
		if err != nil {
			t.Fatalf("Parse() err = %v, want nil", err)
		}
		if got, want := c.SMTPHost, "mailpit"; got != want {
			t.Errorf("SMTPHost = %q, want %q", got, want)
		}
		if got, want := c.SMTPPort, 1025; got != want {
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
		if got, want := c.SMTPTLS, false; got != want {
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

func TestConfig_SecureCookies(t *testing.T) {
	t.Parallel()
	// SecureCookies decides whether session + CSRF cookies get the
	// Secure attribute. Production must, development must not — see
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
