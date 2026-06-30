package demo_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/demo"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("app"))
	})
}

func guardCfg() *config.Config {
	return &config.Config{AppEnvironment: config.AppEnvironmentDefault, SessionKey: "test-session-key-test-session-ke"}
}

func TestGuard_PassThroughWhenDisabled(t *testing.T) {
	t.Setenv("DEMO_MODE_ENABLED", "false")
	g := demo.Guard(okHandler(), demo.Deps{Cfg: guardCfg()})

	for _, path := range []string{"/profile", "/demo", "/admin/quizzes"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if got, want := rec.Code, http.StatusOK; got != want {
			t.Errorf("disabled Guard %s code = %d, want %d (pass-through)", path, got, want)
		}
	}
}

func TestGuard_BlocksLockedPaths(t *testing.T) {
	t.Setenv("DEMO_MODE_ENABLED", "true")
	g := demo.Guard(okHandler(), demo.Deps{Cfg: guardCfg()})

	for _, path := range []string{"/profile", "/profile/password", "/register", "/login/google", "/login/google/callback"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("blocked %s code = %d, want %d", path, got, want)
		}
	}
}

func TestGuard_PassesUnblocked(t *testing.T) {
	t.Setenv("DEMO_MODE_ENABLED", "true")
	g := demo.Guard(okHandler(), demo.Deps{Cfg: guardCfg()})

	for _, path := range []string{"/", "/client/", "/login", "/admin/quizzes"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if got, want := rec.Code, http.StatusOK; got != want {
			t.Errorf("unblocked %s code = %d, want %d", path, got, want)
		}
	}
}
