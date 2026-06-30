package demo_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/demo"
	"github.com/starquake/topbanana/internal/store"
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

	for _, path := range []string{"/profile", "/profile/password"} {
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

	for _, path := range []string{"/", "/client/", "/login", "/admin/quizzes", "/register", "/login/google"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if got, want := rec.Code, http.StatusOK; got != want {
			t.Errorf("unblocked %s code = %d, want %d", path, got, want)
		}
	}
}

func TestGuard_EnterLogsInDemoHost(t *testing.T) {
	t.Setenv("DEMO_MODE_ENABLED", "true")

	logger := slog.New(slog.DiscardHandler)
	stores := store.New(dbtest.Open(t), logger)
	// Seed the host (reuses SeedIfEnabled's host path via a full seed).
	cfg := demoTestConfig()
	cfg.SessionKey = "test-session-key-test-session-ke"
	if _, err := stores.Players.CreateAnonymousPlayer(t.Context(), "Demo Host"); err != nil {
		t.Fatalf("seed host err = %v", err)
	}

	g := demo.Guard(okHandler(), demo.Deps{Cfg: cfg, Players: stores.Players, Logger: logger})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/demo/enter", nil)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("POST /demo/enter code = %d, want %d", got, want)
	}
	var sawSession bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "topbanana_session" && c.Value != "" {
			sawSession = true
		}
	}
	if !sawSession {
		t.Error("POST /demo/enter set no topbanana_session cookie, want one")
	}
}
