// Package server contains everything related to the Server
package server

import (
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/store"
)

// New creates a new server.
func New(logger *slog.Logger, stores *store.Stores, gameService *game.Service, cfg *config.Config) http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, logger, stores, gameService, cfg)
	var handler http.Handler = mux

	return handler
}
