package demo_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/starquake/topbanana/internal/dbtest"
	. "github.com/starquake/topbanana/internal/demo"
	"github.com/starquake/topbanana/internal/session"
	"github.com/starquake/topbanana/internal/store"
)

func TestHandleEnter_LogsInDemoHostAndRedirects(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	sessionMgr := session.New([]byte("test-session-key-test-session-ke"), false)
	stores := store.New(dbtest.Open(t), logger)

	if _, err := stores.Players.CreateAnonymousPlayer(t.Context(), "Demo Host"); err != nil {
		t.Fatalf("seed host err = %v", err)
	}

	h := HandleEnter(sessionMgr, stores.Players, logger)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/demo/enter", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("POST /demo/enter code = %d, want %d", got, want)
	}
	var sawSession bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.CookieName && c.Value != "" {
			sawSession = true
		}
	}
	if !sawSession {
		t.Error("POST /demo/enter set no session cookie, want one")
	}
}
