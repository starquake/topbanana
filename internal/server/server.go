// Package server contains everything related to the Server
package server

import (
	"net/http"

	"github.com/starquake/topbanana/internal/logging"
	"github.com/starquake/topbanana/internal/store"
)

// NewServer creates a new server.
func NewServer(logger *logging.Logger, stores *store.Stores) http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, logger, stores)
	var handler http.Handler = mux

	return handler
}
