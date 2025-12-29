// Package health provides health check endpoints.
package health

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/encoding"
	"github.com/starquake/topbanana/internal/store"
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
			health.Status = "degraded"
			health.Checks["database"] = fmt.Sprintf("unhealthy: %v", err)
			httpStatus = http.StatusServiceUnavailable
		} else {
			health.Checks["database"] = "healthy"
		}

		logger.InfoContext(ctx, "Health check performed")
		w.Header().Set("Content-Type", "application/json")
		err := encoding.Encode(w, httpStatus, health)
		if err != nil {
			panic(err)
		}
	}
}
