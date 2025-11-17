// Package api everything related to the API
package server

import (
	"net/http"

	"github.com/starquake/topbanana/internal/logging"
)

// NewServer creates a new API server.
func NewServer(
	logger *logging.Logger,
) http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, logger)
	var handler http.Handler = mux

	return handler
}
