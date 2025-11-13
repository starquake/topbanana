package api

import (
	"net/http"

	"github.com/starquake/topbanana/internal/logging"
)

func addRoutes(
	mux *http.ServeMux,
	logger *logging.Logger,
) {
	mux.Handle("/helloworld", handleHelloWorld(logger))
	mux.Handle("/", http.NotFoundHandler())
}
