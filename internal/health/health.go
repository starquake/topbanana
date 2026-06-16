// Package health provides health check endpoints.
package health

import (
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/store"
	"github.com/starquake/topbanana/internal/version"
)

// HandleHealthz returns a handler that serves health check responses.
func HandleHealthz(logger *slog.Logger, stores *store.Stores) http.HandlerFunc {
	type healthStatus struct {
		Status  string            `json:"status"`
		Checks  map[string]string `json:"checks,omitempty"`
		Version string            `json:"version,omitempty"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		httpStatus := http.StatusOK
		health := healthStatus{
			Status: "ok",
			Checks: make(map[string]string),
		}

		if err := stores.Quizzes.Ping(ctx); err != nil {
			// Log the raw error (it can leak the DB path / DSN) but return a
			// generic status to the unauthenticated caller.
			logger.ErrorContext(ctx, "health check database ping failed", slog.Any("err", err))
			health.Status = "degraded"
			health.Checks["database"] = "unhealthy"
			httpStatus = http.StatusServiceUnavailable
		} else {
			health.Checks["database"] = "healthy"
		}

		if err := handlers.EncodeJSON(w, httpStatus, health); err != nil {
			logger.ErrorContext(ctx, "error encoding health response", slog.Any("err", err))
		}
	}
}

// HandleVersion serves the build stamp as JSON for uptime checks and
// humans. Unauthenticated and side-effect free: it exposes only the
// environment plus the version/commit/date already shown in the admin
// footer, none of which is sensitive. Version is the stamped release
// (or "dev" when un-stamped); commit is the resolved short sha (with a
// "-dirty" marker for a local build); date is the stamped build time
// (empty in an un-stamped build).
func HandleVersion(logger *slog.Logger) http.HandlerFunc {
	type versionResponse struct {
		Env     string `json:"env"`
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		res := versionResponse{
			Env:     version.Env(),
			Version: version.Release(),
			Commit:  version.CommitLabel(),
			Date:    version.Date,
		}
		if err := handlers.EncodeJSON(w, http.StatusOK, res); err != nil {
			logger.ErrorContext(r.Context(), "error encoding version response", slog.Any("err", err))
		}
	}
}
