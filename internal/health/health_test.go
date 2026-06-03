//go:build integration

package health_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/dbtest"
	. "github.com/starquake/topbanana/internal/health"
	"github.com/starquake/topbanana/internal/store"
)

// healthzResponse mirrors the JSON the /healthz handler emits.
type healthzResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// serveHealthz drives the real HandleHealthz handler against the given stores
// and decodes the response body.
func serveHealthz(t *testing.T, stores *store.Stores) (*httptest.ResponseRecorder, healthzResponse) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	HandleHealthz(slog.New(slog.DiscardHandler), stores)(w, req)

	var res healthzResponse
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("decode response err = %v, want nil", err)
	}

	return w, res
}

func TestHandleHealthz_OKWhenDatabaseHealthy(t *testing.T) {
	t.Parallel()

	stores := store.New(dbtest.Open(t), slog.New(slog.DiscardHandler))

	w, res := serveHealthz(t, stores)

	if got, want := w.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := res.Status, "ok"; got != want {
		t.Errorf("status = %q, want %q", got, want)
	}
	if got, want := res.Checks["database"], "healthy"; got != want {
		t.Errorf("checks.database = %q, want %q", got, want)
	}
}

func TestHandleHealthz_DegradedWhenDatabasePingFails(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	stores := store.New(db, slog.New(slog.DiscardHandler))
	// Real fault injection: closing the connection makes the store's Ping
	// fail, exercising the degraded-health path without a test double.
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close err = %v, want nil", err)
	}

	w, res := serveHealthz(t, stores)

	if got, want := w.Code, http.StatusServiceUnavailable; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := res.Status, "degraded"; got != want {
		t.Errorf("status = %q, want %q", got, want)
	}
	if got, want := res.Checks["database"], "unhealthy"; !strings.Contains(got, want) {
		t.Errorf("checks.database = %q, should contain %q", got, want)
	}
}
