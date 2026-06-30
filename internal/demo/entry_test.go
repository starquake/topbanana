package demo_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/demo"
	"github.com/starquake/topbanana/internal/store"
)

func TestGuard_ServesEntryPage(t *testing.T) {
	t.Setenv("DEMO_MODE_ENABLED", "true")
	g := demo.Guard(okHandler(), demo.Deps{Cfg: guardCfg()})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/demo", nil)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("GET /demo code = %d, want %d", got, want)
	}
	if got := rec.Body.String(); !strings.Contains(got, "/demo/enter") {
		t.Error("entry page missing the enter form action /demo/enter")
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
