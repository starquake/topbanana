// Package server contains everything related to the Server
package server

import (
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/store"
)

// NewServer creates a new server.
func NewServer(logger *slog.Logger, stores *store.Stores) http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, logger, stores)
	var handler http.Handler = mux

	return handler
}
